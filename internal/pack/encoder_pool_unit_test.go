package pack

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	poolUnitWorkspace = int64(1024)
	poolUnitMemory    = int64(64 * 1024)
	poolUnitRawSize   = 64
)

func unitEncoderPoolOptions() EncoderPoolOptions {
	return EncoderPoolOptions{MaxWorkers: 2, InnerConcurrency: 1, CPUBudget: 2,
		MemoryBudgetBytes: poolUnitMemory, WorkspaceBytesPerEncoder: poolUnitWorkspace}
}

func unitEncoderPoolFixture(t *testing.T) Options {
	t.Helper()
	dir := t.TempDir()
	opts := DefaultOptions()
	opts.StubPath, opts.OutputPath = filepath.Join(dir, "stub"), filepath.Join(dir, "output")
	opts.SkipELFValidation, opts.Compression = true, codec.AlgorithmNone
	require.NoError(t, os.WriteFile(opts.StubPath, []byte("stub"), 0o600))
	for _, level := range []string{"v1", "v2", "v3"} {
		path := filepath.Join(dir, level)
		require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte(level), poolUnitRawSize/len(level)), 0o600))
		opts.Variants[level] = path
	}
	return opts
}

func TestEncoderPoolOptionBounds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		change func(*EncoderPoolOptions)
	}{
		{"workers zero", func(p *EncoderPoolOptions) { p.MaxWorkers = 0 }},
		{"workers too many", func(p *EncoderPoolOptions) { p.MaxWorkers = maxPoolWorkers + 1 }},
		{"inner zero", func(p *EncoderPoolOptions) { p.InnerConcurrency = 0 }},
		{"inner too many", func(p *EncoderPoolOptions) { p.InnerConcurrency = maxPoolInner + 1 }},
		{"CPU zero", func(p *EncoderPoolOptions) { p.CPUBudget = 0 }},
		{"CPU too many", func(p *EncoderPoolOptions) { p.CPUBudget = maxPoolCPUBudget + 1 }},
		{"CPU oversubscribed", func(p *EncoderPoolOptions) { p.InnerConcurrency = 2 }},
		{"memory zero", func(p *EncoderPoolOptions) { p.MemoryBudgetBytes = 0 }},
		{"memory too large", func(p *EncoderPoolOptions) { p.MemoryBudgetBytes = maxPoolMemory + 1 }},
		{"workspace zero", func(p *EncoderPoolOptions) { p.WorkspaceBytesPerEncoder = 0 }},
		{"workspace too large", func(p *EncoderPoolOptions) { p.WorkspaceBytesPerEncoder = maxPoolWorkspace + 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pool := unitEncoderPoolOptions()
			tc.change(&pool)
			idx, stats, err := PackWithEncoderPool(context.Background(), Options{}, pool)
			require.ErrorIs(t, err, ErrEncoderPoolOptions)
			assert.Nil(t, idx)
			require.NotNil(t, stats)
			assert.Zero(t, stats.JobsStarted)
			assert.Zero(t, stats.AdmittedBytes)
		})
	}
	t.Run("nil context", func(t *testing.T) {
		_, stats, err := PackWithEncoderPool(nil, Options{}, unitEncoderPoolOptions())
		require.ErrorIs(t, err, ErrEncoderPoolOptions)
		require.NotNil(t, stats)
	})
	t.Run("maximum arithmetic bounds", func(t *testing.T) {
		require.NoError(t, validateEncoderPool(EncoderPoolOptions{
			MaxWorkers: maxPoolWorkers, InnerConcurrency: maxPoolInner, CPUBudget: maxPoolCPUBudget,
			MemoryBudgetBytes: maxPoolMemory, WorkspaceBytesPerEncoder: maxPoolWorkspace,
		}))
	})
}

func TestEncoderPoolAdmissionChargesInnerAndSharedBytes(t *testing.T) {
	t.Parallel()
	opts := unitEncoderPoolFixture(t)
	pool := unitEncoderPoolOptions()
	pool.InnerConcurrency, pool.CPUBudget = 2, 4
	stub, err := os.Stat(opts.StubPath)
	require.NoError(t, err)
	jobBytes := int64(poolUnitRawSize) + poolUnitWorkspace*int64(pool.InnerConcurrency)
	pool.MemoryBudgetBytes = stub.Size() + jobBytes
	idx, stats, err := PackWithEncoderPool(context.Background(), opts, pool)
	require.NoError(t, err)
	require.NotNil(t, idx)
	assert.Equal(t, len(opts.Variants), stats.JobsTotal)
	assert.Equal(t, stats.JobsTotal, stats.JobsStarted)
	assert.Equal(t, stats.JobsTotal, stats.JobsCompleted)
	assert.Equal(t, 1, stats.PeakActiveWorkers, "one reservation fits despite two worker slots")
	assert.Equal(t, pool.MemoryBudgetBytes, stats.PeakAdmittedBytes)
	assert.Equal(t, stub.Size(), stats.SharedBytes)
	assert.Zero(t, stats.ActiveWorkers)
	assert.Zero(t, stats.AdmittedBytes)

	pool.MemoryBudgetBytes--
	_, stats, err = PackWithEncoderPool(context.Background(), opts, pool)
	require.ErrorIs(t, err, ErrEncoderPoolBudget)
	assert.Zero(t, stats.JobsStarted, "reject all impossible jobs before any job allocation")
	assert.Zero(t, stats.PeakAdmittedBytes)
	assert.Equal(t, len(opts.Variants), stats.JobsTotal)
}

func TestEncoderPoolPreflightFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		change func(*Options, *EncoderPoolOptions, *int64)
	}{
		{"negative shared", func(_ *Options, _ *EncoderPoolOptions, n *int64) { *n = -1 }},
		{"shared over budget", func(_ *Options, p *EncoderPoolOptions, n *int64) { *n = p.MemoryBudgetBytes + 1 }},
		{"missing snapshot", func(o *Options, _ *EncoderPoolOptions, _ *int64) { o.Variants["v1"] += "-missing" }},
		{"nonregular snapshot", func(o *Options, _ *EncoderPoolOptions, _ *int64) { o.Variants["v1"] = filepath.Dir(o.StubPath) }},
		{"empty snapshot", func(o *Options, _ *EncoderPoolOptions, _ *int64) {
			require.NoError(t, os.Truncate(o.Variants["v1"], 0))
		}},
		{"invalid codec", func(o *Options, _ *EncoderPoolOptions, _ *int64) { o.Compression = "bad-codec" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, pool, shared := unitEncoderPoolFixture(t), unitEncoderPoolOptions(), int64(4)
			tc.change(&opts, &pool, &shared)
			_, err := prepareEncoderPoolJobs([]string{"v1"}, &opts, shared, pool)
			require.Error(t, err)
		})
	}
}

func TestEncoderPoolCompletesOutOfOrderButAssemblesCanonicalOrder(t *testing.T) {
	t.Parallel()
	opts, pool := unitEncoderPoolFixture(t), unitEncoderPoolOptions()
	secondEntered := make(chan struct{})
	var once sync.Once
	idx, stats, err := packWithEncoderPool(context.Background(), opts, pool, encoderPoolHooks{
		beforeJob: func(ctx context.Context, level, _ string) error {
			if level != "v1" {
				once.Do(func() { close(secondEntered) })
				return nil
			}
			select {
			case <-secondEntered:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, stats.PeakActiveWorkers)
	for i, level := range []string{"v1", "v2", "v3"} {
		assert.Equal(t, level, idx.Variants[i].Level)
	}
	assert.Zero(t, stats.ActiveWorkers)
	assert.Zero(t, stats.AdmittedBytes)
}

type shortPoolWriter struct{}

func (shortPoolWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestEncoderPoolContextIOAndAssembly(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (contextPoolWriter{ctx, io.Discard}).Write([]byte("x"))
	require.ErrorIs(t, err, context.Canceled)
	_, err = (contextPoolReader{ctx, bytes.NewReader([]byte("x"))}).Read(make([]byte, 1))
	require.ErrorIs(t, err, context.Canceled)
	_, err = (contextPoolWriter{context.Background(), shortPoolWriter{}}).Write([]byte("x"))
	require.ErrorIs(t, err, io.ErrShortWrite)
	path := filepath.Join(t.TempDir(), "spool")
	require.NoError(t, os.WriteFile(path, []byte("data"), 0o600))
	require.ErrorIs(t, appendEncoderSpool(ctx, io.Discard, path, 4), context.Canceled)
	require.ErrorIs(t, appendEncoderSpool(context.Background(), io.Discard, path, 1), ErrSizeMismatch)
	require.Error(t, appendEncoderSpool(context.Background(), io.Discard, path+"-missing", 4))
	require.NoError(t, appendEncoderSpool(context.Background(), io.Discard, path, 4))
}

func TestEncoderPoolCodecErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, level string
		dict        []byte
		inner       int
	}{
		{"invalid level", "nonsense", nil, 1},
		{"invalid dictionary", "fast", []byte("broken dictionary"), 1},
		{"invalid concurrency", "fast", nil, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, compressPoolPayload(io.Discard, []byte("input"), codec.NewZstdCodec(), tc.level, tc.dict, tc.inner))
		})
	}
}

func TestEncoderPoolCancellationBeforeFinalRename(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "output")
	require.NoError(t, os.WriteFile(out, []byte("previous"), 0o600))
	tmp, err := os.CreateTemp(dir, "new-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(tmp.Name()) })
	_, err = tmp.Write([]byte("new"))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, finalizeOutputFileContext(ctx, tmp, tmp.Name(), out, 0o755), context.Canceled)
	previous, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "previous", string(previous))
	_, err = tmp.Write([]byte("closed"))
	assert.True(t, errors.Is(err, os.ErrClosed), "file was closed before cancellation gate")
}

type closingPoolWriter struct{ file *os.File }

func (w closingPoolWriter) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	return n, errors.Join(err, w.file.Close())
}

func TestEncoderPoolPrivateSpoolFailures(t *testing.T) {
	t.Parallel()
	opts, pool := unitEncoderPoolFixture(t), unitEncoderPoolOptions()
	jobs, err := prepareEncoderPoolJobs([]string{"v1"}, &opts, 0, pool)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := encodePoolJob(ctx, 0, jobs[0], nil, t.TempDir(), 1, encoderPoolHooks{})
	require.ErrorIs(t, result.err, context.Canceled)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	result = encodePoolJob(ctx, 0, jobs[0], nil, t.TempDir(), 1, encoderPoolHooks{
		beforeJob: func(context.Context, string, string) error { cancel(); return nil },
	})
	require.ErrorIs(t, result.err, context.Canceled)
	result = encodePoolJob(context.Background(), 0, jobs[0], nil, t.TempDir(), 1, encoderPoolHooks{
		spoolWriter: func(f *os.File) io.Writer { return closingPoolWriter{f} },
	})
	require.ErrorIs(t, result.err, os.ErrClosed, "closed spool must not be accepted for assembly")
	stats := &EncoderPoolStats{}
	_, err = runEncoderPool(ctx, jobs, nil, t.TempDir(), pool, stats, encoderPoolHooks{})
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, stats.JobsStarted)
}

func TestEncoderPoolStageDirectoryAndAssemblyFailures(t *testing.T) {
	t.Parallel()
	opts, pool := unitEncoderPoolFixture(t), unitEncoderPoolOptions()
	t.Run("removed spool parent", func(t *testing.T) {
		dir := t.TempDir()
		out, err := os.CreateTemp(dir, "output-*")
		require.NoError(t, err)
		t.Cleanup(func() { _ = out.Close() })
		require.NoError(t, os.Remove(out.Name()))
		require.NoError(t, os.Remove(dir))
		_, _, err = writePooledPayloads(context.Background(), out, []string{"v1"}, &opts, 0, nil,
			pool, &EncoderPoolStats{}, encoderPoolHooks{})
		require.Error(t, err)
	})
	t.Run("closed output during assembly", func(t *testing.T) {
		out, err := os.CreateTemp(t.TempDir(), "output-*")
		require.NoError(t, err)
		hooks := encoderPoolHooks{createSpool: func(dir, pattern string) (*os.File, error) {
			if err := out.Close(); err != nil {
				return nil, err
			}
			return os.CreateTemp(dir, pattern)
		}}
		stats := &EncoderPoolStats{}
		_, _, err = writePooledPayloads(context.Background(), out, []string{"v1"}, &opts, 0, nil, pool, stats, hooks)
		require.ErrorIs(t, err, os.ErrClosed)
		assert.Zero(t, stats.ActiveWorkers)
		assert.Zero(t, stats.AdmittedBytes)
		spools, err := filepath.Glob(filepath.Join(filepath.Dir(out.Name()), ".microfat-spools-*"))
		require.NoError(t, err)
		assert.Empty(t, spools)
	})
}

type poolDictionaryForwardingCodec struct {
	*codec.NoneCodec
	seenDictionary []byte
}

func (c *poolDictionaryForwardingCodec) CompressWithDict(w io.Writer, raw []byte, level string, dict []byte) error {
	c.seenDictionary = bytes.Clone(dict)
	return c.Compress(w, raw, level)
}

func (c *poolDictionaryForwardingCodec) DecompressWithDict(w io.Writer, r io.Reader, size int64, _ []byte) error {
	return c.Decompress(w, r, size)
}

func TestEncoderPoolPreservesOtherDictionaryCodecContract(t *testing.T) {
	t.Parallel()
	c := &poolDictionaryForwardingCodec{NoneCodec: codec.NewNoneCodec()}
	var out bytes.Buffer
	require.NoError(t, compressPoolPayload(&out, []byte("raw"), c, "", []byte("dictionary"), 1))
	assert.Equal(t, "dictionary", string(c.seenDictionary))
	assert.Equal(t, "raw", out.String())
}
