package pack

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
)

const (
	packMebiByte                  int64 = 1024 * 1024
	packKibiByte                  int64 = 1024
	packBaseMemoryReserveBytes    int64 = 64 * packMebiByte
	packUsableMemoryNumerator     int64 = 17
	packUsableMemoryDenominator   int64 = 20
	packRawPayloadGCFactor        int64 = 2
	packWorkspaceNoneBytes        int64 = 4 * packMebiByte
	packWorkspaceLZ4FastBytes     int64 = 16 * packMebiByte
	packWorkspaceLZ4HCBytes       int64 = 32 * packMebiByte
	packWorkspaceZstdFastestBytes int64 = 32 * packMebiByte
	packWorkspaceZstdDefaultBytes int64 = 64 * packMebiByte
	packWorkspaceZstdBetterBytes  int64 = 112 * packMebiByte
	packWorkspaceZstdBestBytes    int64 = 224 * packMebiByte
	packLZ4HCThresholdLevel             = 4
	packMinParallelVariants             = 2
	packMeminfoPath                     = "/proc/meminfo"
	packMemAvailablePrefix              = "MemAvailable:"
	packStageTempPattern                = ".microfat-pack-var-*.tmp"
)

var (
	readPackCgroupLimitsFunc = cgroup.ReadLimits
	readPackGOMEMLIMITFunc   = func() int64 { return debug.SetMemoryLimit(-1) }
	readPackGOMAXPROCSFunc   = func() int { return runtime.GOMAXPROCS(0) }
	readHostMemAvailableFunc = func() (int64, bool) { return parseMemAvailableFromFile(packMeminfoPath) }
)

type variantWorkPlan struct {
	level          string
	normalizedLvl  string
	cleanPath      string
	statSize       int64
	codecImpl      codec.Codec
	resolvedLevel  string
	estimatedBytes int64
}

type parallelVariantResult struct {
	entry      format.VariantEntry
	stagedPath string
	err        error
}

func writeAllVariantPayloads(
	tmpFile *os.File,
	outDir string,
	levels []string,
	opts *Options,
	currentOffset int64,
	dictBytes []byte,
) ([]format.VariantEntry, int64, error) {
	plans, err := prepareVariantWorkPlans(levels, opts)
	if err != nil {
		return nil, 0, err
	}

	workers := resolvePackWorkerCount(plans)
	if workers < packMinParallelVariants {
		entries := make([]format.VariantEntry, 0, len(plans))
		offset := currentOffset
		for i := range plans {
			entry, nextOffset, writeErr := writeVariantPayload(
				tmpFile, plans[i].level, plans[i].cleanPath, opts, offset, dictBytes,
			)
			if writeErr != nil {
				return nil, 0, writeErr
			}
			offset = nextOffset
			entries = append(entries, entry)
		}
		return entries, offset, nil
	}

	return writeVariantPayloadsParallel(tmpFile, outDir, plans, opts, currentOffset, dictBytes, workers)
}

func prepareVariantWorkPlans(levels []string, opts *Options) ([]variantWorkPlan, error) {
	plans := make([]variantWorkPlan, 0, len(levels))
	for _, lvl := range levels {
		cleanPath := filepath.Clean(opts.Variants[lvl])
		stat, err := os.Stat(cleanPath)
		if err != nil {
			return nil, fmt.Errorf("%w: %s (%w)", ErrVariantNotFound, cleanPath, err)
		}
		c, resolvedLevel, err := resolveVariantCompression(opts, lvl, stat.Size())
		if err != nil {
			return nil, err
		}
		plans = append(plans, variantWorkPlan{
			level:          lvl,
			normalizedLvl:  format.NormalizeVariant(lvl),
			cleanPath:      cleanPath,
			statSize:       stat.Size(),
			codecImpl:      c,
			resolvedLevel:  resolvedLevel,
			estimatedBytes: estimateVariantWorkerMemoryBytes(stat.Size(), c.Name(), resolvedLevel),
		})
	}
	return plans, nil
}

func resolvePackWorkerCount(plans []variantWorkPlan) int {
	if len(plans) < packMinParallelVariants {
		return 1
	}

	cpuBudget := readPackGOMAXPROCSFunc()
	limits, limitsErr := readPackCgroupLimitsFunc()
	if limitsErr == nil && limits.CPUs > 0 && limits.CPUs < cpuBudget {
		cpuBudget = limits.CPUs
	}
	if cpuBudget < packMinParallelVariants {
		return 1
	}

	maxWorkers := min(len(plans), cpuBudget)
	memCeiling, hasCeiling := effectivePackMemoryCeiling(limits, limitsErr == nil)
	if !hasCeiling {
		return maxWorkers
	}
	return admitWorkersByMemory(plans, maxWorkers, memCeiling)
}

func effectivePackMemoryCeiling(limits cgroup.Limits, limitsValid bool) (int64, bool) {
	var ceiling int64
	hasCeiling := false

	if limitsValid {
		if cgroupBytes, finite := limits.EffectiveMemoryLimit(); finite {
			ceiling = cgroupBytes
			hasCeiling = true
		}
	}

	gomemlimit := readPackGOMEMLIMITFunc()
	if gomemlimit >= 0 && gomemlimit < math.MaxInt64 {
		if !hasCeiling || gomemlimit < ceiling {
			ceiling = gomemlimit
			hasCeiling = true
		}
	}

	if availBytes, ok := readHostMemAvailableFunc(); ok && availBytes >= 0 {
		if !hasCeiling || availBytes < ceiling {
			ceiling = availBytes
			hasCeiling = true
		}
	}

	return ceiling, hasCeiling
}

func admitWorkersByMemory(plans []variantWorkPlan, maxWorkers int, ceilingBytes int64) int {
	if ceilingBytes <= 0 || maxWorkers < packMinParallelVariants {
		return 1
	}
	usableBytes := (ceilingBytes / packUsableMemoryDenominator) * packUsableMemoryNumerator
	if usableBytes <= packBaseMemoryReserveBytes {
		return 1
	}
	workerBudget := usableBytes - packBaseMemoryReserveBytes

	estimates := make([]int64, len(plans))
	for i := range plans {
		estimates[i] = plans[i].estimatedBytes
	}
	slices.SortFunc(estimates, func(a, b int64) int {
		switch {
		case a > b:
			return -1
		case a < b:
			return 1
		default:
			return 0
		}
	})

	admitted := 1
	var cumulative int64
	for k := 1; k <= maxWorkers; k++ {
		wCost := estimates[k-1]
		if wCost > workerBudget-cumulative {
			break
		}
		cumulative += wCost
		if k >= packMinParallelVariants {
			admitted = k
		}
	}
	return admitted
}

func estimateVariantWorkerMemoryBytes(uncompressedSize int64, codecName, resolvedLevel string) int64 {
	rawSize := max(uncompressedSize, int64(0))
	workspace := encoderWorkspaceBytes(codecName, resolvedLevel)
	if codecName == codec.AlgorithmLZ4 {
		// Include every block buffer the codec can keep in flight. Each
		// outer variant worker gets its own inner compression pipeline.
		workspace = max(workspace, codec.LZ4EncoderWorkspaceBytes(rawSize))
	}
	if rawSize > (math.MaxInt64-workspace)/packRawPayloadGCFactor {
		return math.MaxInt64
	}
	return rawSize*packRawPayloadGCFactor + workspace
}

func encoderWorkspaceBytes(codecName, resolvedLevel string) int64 {
	switch codecName {
	case codec.AlgorithmNone:
		return packWorkspaceNoneBytes
	case codec.AlgorithmLZ4:
		clean := strings.ToLower(strings.TrimSpace(resolvedLevel))
		switch clean {
		case "better", "best", "max":
			return packWorkspaceLZ4HCBytes
		}
		if num, err := codec.ParseLevelInt(clean, 0); err == nil && num >= packLZ4HCThresholdLevel {
			return packWorkspaceLZ4HCBytes
		}
		return packWorkspaceLZ4FastBytes
	case codec.AlgorithmZstd:
		zCodec := codec.NewZstdCodec()
		lvl, err := zCodec.MapLevel(resolvedLevel)
		if err != nil {
			return packWorkspaceZstdDefaultBytes
		}
		return zstdLevelWorkspaceBytes(lvl.String())
	default:
		return packWorkspaceZstdDefaultBytes
	}
}

func zstdLevelWorkspaceBytes(mapped string) int64 {
	lower := strings.ToLower(mapped)
	switch {
	case strings.Contains(lower, "fastest"):
		return packWorkspaceZstdFastestBytes
	case strings.Contains(lower, "better"):
		return packWorkspaceZstdBetterBytes
	case strings.Contains(lower, "best"):
		return packWorkspaceZstdBestBytes
	default:
		return packWorkspaceZstdDefaultBytes
	}
}

func parseMemAvailableFromFile(path string) (int64, bool) {
	// #nosec G304 -- reads fixed /proc/meminfo or test fixture path.
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, packMemAvailablePrefix) {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, packMemAvailablePrefix))
		if len(fields) == 0 {
			return 0, false
		}
		kb, parseErr := strconv.ParseInt(fields[0], 10, 64)
		if parseErr != nil || kb < 0 || kb > math.MaxInt64/packKibiByte {
			return 0, false
		}
		return kb * packKibiByte, true
	}
	return 0, false
}

func writeVariantPayloadsParallel(
	tmpFile *os.File,
	outDir string,
	plans []variantWorkPlan,
	opts *Options,
	currentOffset int64,
	dictBytes []byte,
	workers int,
) ([]format.VariantEntry, int64, error) {
	results := make([]parallelVariantResult, len(plans))
	defer func() {
		for i := range results {
			if results[i].stagedPath != "" {
				_ = os.Remove(results[i].stagedPath)
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	jobCh := make(chan int, len(plans))
	for i := range plans {
		jobCh <- i
	}
	close(jobCh)

	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for idx := range jobCh {
				if ctx.Err() != nil {
					continue
				}
				res := stageCompressedVariant(outDir, &plans[idx], opts, dictBytes)
				results[idx] = res
				if res.err != nil {
					cancel()
				}
			}
		}()
	}
	wg.Wait()

	for i := range results {
		if results[i].err != nil {
			return nil, 0, results[i].err
		}
	}

	entries := make([]format.VariantEntry, 0, len(plans))
	offset := currentOffset
	for i := range results {
		copied, copyErr := appendStagedVariantFrame(tmpFile, results[i].stagedPath, results[i].entry.CompressedSize)
		if copyErr != nil {
			return nil, 0, copyErr
		}
		_ = os.Remove(results[i].stagedPath)
		results[i].stagedPath = ""

		entry := results[i].entry
		entry.Offset = offset
		offset += copied
		entries = append(entries, entry)
	}
	return entries, offset, nil
}

func stageCompressedVariant(
	outDir string,
	plan *variantWorkPlan,
	opts *Options,
	dictBytes []byte,
) parallelVariantResult {
	variantBytes, err := readBoundedInput(plan.cleanPath, format.MaxPayloadSize)
	if err != nil {
		return parallelVariantResult{
			err: fmt.Errorf("%w: %s (%w)", ErrVariantNotFound, plan.cleanPath, err),
		}
	}

	rawHash := sha256.Sum256(variantBytes)
	rawHashHex := hex.EncodeToString(rawHash[:])
	uncompressedSize := int64(len(variantBytes))

	c := plan.codecImpl
	resolvedLevel := plan.resolvedLevel
	if uncompressedSize != plan.statSize {
		var resolveErr error
		c, resolvedLevel, resolveErr = resolveVariantCompression(opts, plan.level, uncompressedSize)
		if resolveErr != nil {
			return parallelVariantResult{err: resolveErr}
		}
	}

	stageFile, err := os.CreateTemp(outDir, packStageTempPattern)
	if err != nil {
		return parallelVariantResult{
			err: fmt.Errorf("creating variant staging file for %s: %w", plan.level, err),
		}
	}
	stagePath := stageFile.Name()

	compErr := compressVariantIntoWriter(stageFile, plan.level, c, resolvedLevel, variantBytes, dictBytes)
	// Allow GC to reclaim the uncompressed payload slice as soon as encoding finishes.
	clearVariantRef(&variantBytes)
	if compErr != nil {
		_ = stageFile.Close()
		return parallelVariantResult{stagedPath: stagePath, err: compErr}
	}

	compressedSize, seekErr := stageFile.Seek(0, io.SeekCurrent)
	closeErr := stageFile.Close()
	if seekErr != nil {
		return parallelVariantResult{
			stagedPath: stagePath,
			err:        fmt.Errorf("seeking staged variant %s offset: %w", plan.level, seekErr),
		}
	}
	if closeErr != nil {
		return parallelVariantResult{
			stagedPath: stagePath,
			err:        fmt.Errorf("closing staged variant %s: %w", plan.level, closeErr),
		}
	}

	return parallelVariantResult{
		stagedPath: stagePath,
		entry: format.VariantEntry{
			Level:            plan.normalizedLvl,
			CompressedSize:   compressedSize,
			UncompressedSize: uncompressedSize,
			SHA256:           rawHashHex,
			Compression:      c.Name(),
		},
	}
}

func clearVariantRef(buf *[]byte) {
	*buf = nil
}

func compressVariantIntoWriter(
	w io.Writer,
	lvl string,
	c codec.Codec,
	resolvedLevel string,
	variantBytes []byte,
	dict []byte,
) error {
	if len(dict) > 0 {
		if dc, ok := c.(codec.DictCodec); ok {
			if err := dc.CompressWithDict(w, variantBytes, resolvedLevel, dict); err != nil {
				return fmt.Errorf("compressing variant %s with dict: %w", lvl, err)
			}
			return nil
		}
	}
	if err := c.Compress(w, variantBytes, resolvedLevel); err != nil {
		return fmt.Errorf("compressing variant %s with codec %s: %w", lvl, c.Name(), err)
	}
	return nil
}

func appendStagedVariantFrame(dst *os.File, stagedPath string, expectedBytes int64) (int64, error) {
	f, err := inputfile.Open(filepath.Clean(stagedPath))
	if err != nil {
		return 0, fmt.Errorf("opening staged variant frame: %w", err)
	}
	defer func() { _ = f.Close() }()

	copied, err := io.Copy(dst, io.LimitReader(f, expectedBytes+1))
	if err != nil {
		return 0, fmt.Errorf("copying staged variant frame: %w", err)
	}
	if copied != expectedBytes {
		return 0, fmt.Errorf("%w: staged variant frame size changed (expected %d, got %d)",
			ErrSizeMismatch, expectedBytes, copied)
	}
	return copied, nil
}
