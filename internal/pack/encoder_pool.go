package pack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/klauspost/compress/zstd"
)

const (
	maxPoolWorkers   = 4
	maxPoolInner     = 4
	maxPoolCPUBudget = 16
	maxPoolMemory    = int64(1 << 30)
	maxPoolWorkspace = int64(256 << 20)
)

var (
	ErrEncoderPoolOptions = errors.New("invalid encoder pool options")
	ErrEncoderPoolBudget  = errors.New("encoder pool admission exceeds memory budget")
)

// EncoderPoolOptions configures an opt-in payload encoder pool. Every field must
// be positive. WorkspaceBytesPerEncoder is a caller-declared reservation for one
// inner encoder; it is multiplied by InnerConcurrency for each active job.
// These reservations constrain admission, not actual codec allocations or RSS.
type EncoderPoolOptions struct {
	MaxWorkers               int
	InnerConcurrency         int
	CPUBudget                int
	MemoryBudgetBytes        int64
	WorkspaceBytesPerEncoder int64
}

// EncoderPoolStats describes logical payload-stage admission only. SharedBytes
// charges the stub and trained dictionary; each job charges its raw snapshot
// length plus its declared workspace reservation. Preparation (including ELF/ABI
// validation and dictionary training), Go heap overhead, and file cache are not
// accounted here. JobsCompleted counts successful jobs, JobsStarted all admitted
// jobs. Current counters are zero on return, including failure and cancellation.
type EncoderPoolStats struct {
	JobsTotal                int
	JobsStarted              int
	JobsCompleted            int
	ActiveWorkers            int
	PeakActiveWorkers        int
	AdmittedBytes            int64
	PeakAdmittedBytes        int64
	SharedBytes              int64
	CPUBudget                int
	InnerConcurrency         int
	MemoryBudgetBytes        int64
	WorkspaceBytesPerEncoder int64
}

// PackWithEncoderPool preserves Pack's snapshot, validation, dictionary and
// atomic publication contract, while compressing payloads into private disk
// spools with explicit Zstd inner concurrency. Pack retains its default path.
// Cancellation is cooperative at I/O boundaries; all workers finish before
// cleanup or return. No output is published until successful spool cleanup.
func PackWithEncoderPool(ctx context.Context, opts Options, pool EncoderPoolOptions) (*format.Index, *EncoderPoolStats, error) {
	return packWithEncoderPool(ctx, opts, pool, encoderPoolHooks{})
}

// Hooks are per invocation, immutable and internal; tests never mutate globals.
type encoderPoolHooks struct {
	beforeJob    func(context.Context, string, string) error
	createSpool  func(string, string) (*os.File, error)
	spoolWriter  func(*os.File) io.Writer
	removeSpools func(string) error
}

func packWithEncoderPool(
	ctx context.Context, opts Options, pool EncoderPoolOptions, hooks encoderPoolHooks,
) (*format.Index, *EncoderPoolStats, error) {
	stats := &EncoderPoolStats{
		CPUBudget: pool.CPUBudget, InnerConcurrency: pool.InnerConcurrency,
		MemoryBudgetBytes: pool.MemoryBudgetBytes, WorkspaceBytesPerEncoder: pool.WorkspaceBytesPerEncoder,
	}
	if ctx == nil {
		return nil, stats, fmt.Errorf("%w: context must not be nil", ErrEncoderPoolOptions)
	}
	if err := validateEncoderPool(pool); err != nil {
		return nil, stats, err
	}
	stage := func(ctx context.Context, out *os.File, levels []string, opts *Options, offset int64, dict []byte) (
		[]format.VariantEntry, int64, error,
	) {
		return writePooledPayloads(ctx, out, levels, opts, offset, dict, pool, stats, hooks)
	}
	idx, err := packWithPayloadStage(ctx, opts, stage)
	return idx, stats, err
}

func validateEncoderPool(pool EncoderPoolOptions) error {
	switch {
	case pool.MaxWorkers < 1 || pool.MaxWorkers > maxPoolWorkers:
		return fmt.Errorf("%w: MaxWorkers must be in [1,%d]", ErrEncoderPoolOptions, maxPoolWorkers)
	case pool.InnerConcurrency < 1 || pool.InnerConcurrency > maxPoolInner:
		return fmt.Errorf("%w: InnerConcurrency must be in [1,%d]", ErrEncoderPoolOptions, maxPoolInner)
	case pool.CPUBudget < 1 || pool.CPUBudget > maxPoolCPUBudget:
		return fmt.Errorf("%w: CPUBudget must be in [1,%d]", ErrEncoderPoolOptions, maxPoolCPUBudget)
	case pool.MaxWorkers*pool.InnerConcurrency > pool.CPUBudget:
		return fmt.Errorf("%w: MaxWorkers * InnerConcurrency exceeds CPUBudget", ErrEncoderPoolOptions)
	case pool.MemoryBudgetBytes < 1 || pool.MemoryBudgetBytes > maxPoolMemory:
		return fmt.Errorf("%w: MemoryBudgetBytes must be in [1,%d]", ErrEncoderPoolOptions, maxPoolMemory)
	case pool.WorkspaceBytesPerEncoder < 1 || pool.WorkspaceBytesPerEncoder > maxPoolWorkspace:
		return fmt.Errorf("%w: WorkspaceBytesPerEncoder must be in [1,%d]", ErrEncoderPoolOptions, maxPoolWorkspace)
	}
	// Upper bounds above make the subsequent workspace multiplication and
	// raw-size/shared additions representable in int64 before any allocation.
	return nil
}

type encoderPoolJob struct {
	level       string
	path        string
	rawSize     int64
	reservation int64
	codec       codec.Codec
	codecLevel  string
}

type encoderPoolResult struct {
	position int
	path     string
	entry    format.VariantEntry
	err      error
}

func prepareEncoderPoolJobs(
	levels []string, opts *Options, shared int64, pool EncoderPoolOptions,
) ([]encoderPoolJob, error) {
	if shared < 0 || shared > pool.MemoryBudgetBytes {
		return nil, fmt.Errorf("%w: shared bytes %d", ErrEncoderPoolBudget, shared)
	}
	jobs := make([]encoderPoolJob, 0, len(levels))
	workspace := pool.WorkspaceBytesPerEncoder * int64(pool.InnerConcurrency)
	for _, level := range levels {
		path := opts.Variants[level]
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("stat snapshotted variant %s: %w", level, err)
		}
		if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > format.MaxPayloadSize {
			return nil, fmt.Errorf("%w: invalid snapshot size/type for %s", ErrInvalidELF, level)
		}
		reservation := info.Size() + workspace
		if reservation > pool.MemoryBudgetBytes-shared {
			return nil, fmt.Errorf("%w: variant %s needs shared %d + job %d bytes", ErrEncoderPoolBudget, level, shared, reservation)
		}
		c, codecLevel, err := resolveVariantCompression(opts, level, info.Size())
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, encoderPoolJob{level, path, info.Size(), reservation, c, codecLevel})
	}
	return jobs, nil
}

func writePooledPayloads(
	ctx context.Context, out *os.File, levels []string, opts *Options, offset int64, dict []byte,
	pool EncoderPoolOptions, stats *EncoderPoolStats, hooks encoderPoolHooks,
) (entries []format.VariantEntry, nextOffset int64, returnErr error) {
	stats.JobsTotal = len(levels)
	stats.SharedBytes = offset
	jobs, err := prepareEncoderPoolJobs(levels, opts, offset, pool)
	if err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	spoolDir, err := os.MkdirTemp(filepath.Dir(out.Name()), ".microfat-spools-*")
	if err != nil {
		return nil, 0, fmt.Errorf("creating encoder spool directory: %w", err)
	}
	defer func() {
		remove := hooks.removeSpools
		if remove == nil {
			remove = os.RemoveAll
		}
		if err := remove(spoolDir); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("removing encoder spools %s: %w", spoolDir, err))
		}
	}()
	stats.AdmittedBytes = offset
	stats.PeakAdmittedBytes = offset
	defer func() { stats.AdmittedBytes = 0 }()
	results, err := runEncoderPool(ctx, jobs, dict, spoolDir, pool, stats, hooks)
	if err != nil {
		return nil, 0, err
	}
	entries = make([]format.VariantEntry, 0, len(results))
	for _, result := range results {
		entry := result.entry
		entry.Offset = offset
		if err := appendEncoderSpool(ctx, out, result.path, entry.CompressedSize); err != nil {
			return nil, 0, err
		}
		offset += entry.CompressedSize
		entries = append(entries, entry)
	}
	return entries, offset, ctx.Err()
}

func runEncoderPool(
	ctx context.Context, jobs []encoderPoolJob, dict []byte, dir string,
	pool EncoderPoolOptions, stats *EncoderPoolStats, hooks encoderPoolHooks,
) ([]encoderPoolResult, error) {
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ready := make(chan encoderPoolResult, pool.MaxWorkers)
	var workers sync.WaitGroup
	defer workers.Wait()
	results := make([]encoderPoolResult, len(jobs))
	next := 0
	var runErr error
	for next < len(jobs) || stats.ActiveWorkers > 0 {
		if err := workerCtx.Err(); err != nil && runErr == nil {
			runErr = err
		}
		for runErr == nil && next < len(jobs) && stats.ActiveWorkers < pool.MaxWorkers &&
			jobs[next].reservation <= pool.MemoryBudgetBytes-stats.AdmittedBytes {
			position, job := next, jobs[next]
			stats.ActiveWorkers++
			stats.JobsStarted++
			stats.AdmittedBytes += job.reservation
			stats.PeakActiveWorkers = max(stats.PeakActiveWorkers, stats.ActiveWorkers)
			stats.PeakAdmittedBytes = max(stats.PeakAdmittedBytes, stats.AdmittedBytes)
			next++
			workers.Add(1)
			go func() {
				defer workers.Done()
				ready <- encodePoolJob(workerCtx, position, job, dict, dir, pool.InnerConcurrency, hooks)
			}()
		}
		if stats.ActiveWorkers == 0 {
			break
		}
		// Even after cancellation, drain every worker before touching its spools.
		result := <-ready
		stats.ActiveWorkers--
		stats.AdmittedBytes -= jobs[result.position].reservation
		results[result.position] = result
		if result.err != nil {
			runErr = errors.Join(runErr, result.err)
			cancel()
		} else {
			stats.JobsCompleted++
		}
	}
	return results, errors.Join(runErr, ctx.Err())
}

func encodePoolJob(
	ctx context.Context, position int, job encoderPoolJob, dict []byte, dir string, inner int, hooks encoderPoolHooks,
) (result encoderPoolResult) {
	result.position = position
	if err := ctx.Err(); err != nil {
		result.err = err
		return result
	}
	if hooks.beforeJob != nil {
		if err := hooks.beforeJob(ctx, format.NormalizeVariant(job.level), job.path); err != nil {
			result.err = err
			return result
		}
	}
	if err := ctx.Err(); err != nil {
		result.err = err
		return result
	}
	raw, err := readBoundedInput(job.path, job.rawSize)
	if err != nil {
		result.err = fmt.Errorf("reading snapshotted variant %s: %w", job.level, err)
		return result
	}
	if int64(len(raw)) != job.rawSize {
		result.err = fmt.Errorf("%w: snapshot %s changed size", ErrSizeMismatch, job.level)
		return result
	}
	create := hooks.createSpool
	if create == nil {
		create = os.CreateTemp
	}
	spool, err := create(dir, "payload-*")
	if err != nil {
		result.err = fmt.Errorf("creating variant %s spool: %w", job.level, err)
		return result
	}
	defer func() { result.err = errors.Join(result.err, spool.Close()) }()
	writer := io.Writer(spool)
	if hooks.spoolWriter != nil {
		writer = hooks.spoolWriter(spool)
	}
	if err := compressPoolPayload(contextPoolWriter{ctx, writer}, raw, job.codec, job.codecLevel, dict, inner); err != nil {
		result.err = fmt.Errorf("compressing variant %s with codec %s: %w", job.level, job.codec.Name(), err)
		return result
	}
	info, err := spool.Stat()
	if err != nil {
		result.err = fmt.Errorf("stat variant %s spool: %w", job.level, err)
		return result
	}
	hash := sha256.Sum256(raw)
	result.path = spool.Name()
	result.entry = format.VariantEntry{
		Level: format.NormalizeVariant(job.level), CompressedSize: info.Size(), UncompressedSize: job.rawSize,
		SHA256: hex.EncodeToString(hash[:]), Compression: job.codec.Name(),
	}
	result.err = ctx.Err()
	return result
}

func compressPoolPayload(w io.Writer, raw []byte, c codec.Codec, level string, dict []byte, inner int) error {
	if zc, ok := c.(*codec.ZstdCodec); ok {
		mapped, err := zc.MapLevel(level)
		if err != nil {
			return err
		}
		opts := []zstd.EOption{zstd.WithEncoderLevel(mapped), zstd.WithEncoderConcurrency(inner)}
		if len(dict) > 0 {
			opts = append(opts, zstd.WithEncoderDict(dict))
		}
		encoder, err := zstd.NewWriter(w, opts...)
		if err != nil {
			return err
		}
		_, writeErr := encoder.Write(raw)
		return errors.Join(writeErr, encoder.Close())
	}
	if dc, ok := c.(codec.DictCodec); ok && len(dict) > 0 {
		return dc.CompressWithDict(w, raw, level, dict)
	}
	return c.Compress(w, raw, level)
}

func appendEncoderSpool(ctx context.Context, out io.Writer, path string, size int64) (returnErr error) {
	spool, err := os.Open(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("opening encoder spool: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, spool.Close()) }()
	n, err := io.Copy(contextPoolWriter{ctx, out}, contextPoolReader{ctx, spool})
	if err != nil {
		return fmt.Errorf("assembling encoder spool: %w", err)
	}
	if n != size {
		return fmt.Errorf("%w: encoder spool expected %d bytes, copied %d", ErrSizeMismatch, size, n)
	}
	return nil
}

type contextPoolWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w contextPoolWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.w.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

type contextPoolReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextPoolReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
