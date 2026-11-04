package pack

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

const (
	// MaxDictProfitabilitySpoolBytes bounds temporary staged frame bytes across
	// no-dictionary and with-dictionary candidate sets during profitability evaluation (2 GiB).
	MaxDictProfitabilitySpoolBytes int64 = 2 * format.MaxPayloadSize
)

type spoolBudgetTracker struct {
	used  atomic.Int64
	limit int64
}

func newSpoolBudgetTracker(customLimit int64) *spoolBudgetTracker {
	limit := customLimit
	if limit <= 0 {
		limit = MaxDictProfitabilitySpoolBytes
	}
	return &spoolBudgetTracker{limit: limit}
}

type spoolBoundedWriter struct {
	w       io.Writer
	tracker *spoolBudgetTracker
}

func (sw *spoolBoundedWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	total := sw.tracker.used.Add(int64(len(p)))
	if total > sw.tracker.limit {
		return 0, fmt.Errorf("%w: staged bytes %d exceed limit %d", ErrSpoolBudgetExceeded, total, sw.tracker.limit)
	}
	return sw.w.Write(p)
}

func assembleProfitableArtifact(
	tmpFile *os.File,
	outDir string,
	levels []string,
	opts *Options,
	stubOffset int64,
	baseIdx *format.Index,
	dictBytes []byte,
	dictSHAHex string,
) (*format.Index, error) {
	plans, err := prepareVariantWorkPlans(levels, opts)
	if err != nil {
		return nil, err
	}

	workers := max(resolvePackWorkerCount(plans), 1)
	tracker := newSpoolBudgetTracker(opts.MaxSpoolBytes)

	var createdPaths []string
	defer func() {
		seen := make(map[string]struct{}, len(createdPaths))
		for _, p := range createdPaths {
			if p == "" {
				continue
			}
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			_ = os.Remove(p)
		}
	}()

	allIndices := make([]int, len(plans))
	var dictIndices []int
	for i := range plans {
		allIndices[i] = i
		if _, ok := plans[i].codecImpl.(codec.DictCodec); ok {
			dictIndices = append(dictIndices, i)
		}
	}

	noDictResults, stageErr := stagePlanSubset(outDir, plans, allIndices, opts, nil, workers, tracker)
	recordStagedPaths(&createdPaths, noDictResults)
	if stageErr != nil {
		return nil, stageErr
	}

	withDictResults := make([]parallelVariantResult, len(plans))
	copy(withDictResults, noDictResults)
	if len(dictIndices) > 0 {
		dictWorkers := min(workers, len(dictIndices))
		dictSubset, dictErr := stagePlanSubset(outDir, plans, dictIndices, opts, dictBytes, dictWorkers, tracker)
		recordStagedPaths(&createdPaths, dictSubset)
		if dictErr != nil {
			return nil, dictErr
		}
		for pos, planIdx := range dictIndices {
			withDictResults[planIdx] = dictSubset[pos]
		}
	}

	noDictIdx, noDictEnd, noDictTotal, err := measureCandidateCompleteBytes(
		baseIdx, stubOffset, nil, "", noDictResults, opts.FormatVersion,
	)
	if err != nil {
		return nil, err
	}

	withDictIdx, withDictEnd, withDictTotal, err := measureCandidateCompleteBytes(
		baseIdx, stubOffset, dictBytes, dictSHAHex, withDictResults, opts.FormatVersion,
	)
	if err != nil {
		return nil, err
	}

	chosenIdx := noDictIdx
	chosenEnd := noDictEnd
	chosenResults := noDictResults
	writeOffset := stubOffset
	if withDictTotal < noDictTotal {
		if _, err := tmpFile.Write(dictBytes); err != nil {
			return nil, fmt.Errorf("writing dictionary payload: %w", err)
		}
		writeOffset += int64(len(dictBytes))
		chosenIdx = withDictIdx
		chosenEnd = withDictEnd
		chosenResults = withDictResults
	}

	for i := range chosenResults {
		copied, copyErr := appendStagedVariantFrame(tmpFile, chosenResults[i].stagedPath, chosenResults[i].entry.CompressedSize)
		if copyErr != nil {
			return nil, copyErr
		}
		writeOffset += copied
	}
	if writeOffset != chosenEnd {
		return nil, fmt.Errorf("%w: profitability frame assembly offset %d != expected %d",
			ErrSizeMismatch, writeOffset, chosenEnd)
	}

	if _, err := format.WriteIndexAndTrailerWithVersion(tmpFile, chosenIdx, writeOffset, opts.FormatVersion); err != nil {
		return nil, fmt.Errorf("writing index and trailer: %w", err)
	}
	return chosenIdx, nil
}

func recordStagedPaths(dst *[]string, results []parallelVariantResult) {
	for i := range results {
		if results[i].stagedPath != "" {
			*dst = append(*dst, results[i].stagedPath)
		}
	}
}

func stagePlanSubset(
	outDir string,
	plans []variantWorkPlan,
	indices []int,
	opts *Options,
	dictBytes []byte,
	workers int,
	tracker *spoolBudgetTracker,
) ([]parallelVariantResult, error) {
	results := make([]parallelVariantResult, len(indices))
	if len(indices) == 0 {
		return results, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	jobCh := make(chan int, len(indices))
	for pos := range indices {
		jobCh <- pos
	}
	close(jobCh)

	workerCount := max(min(workers, len(indices)), 1)
	var wg sync.WaitGroup
	wg.Add(workerCount)
	for range workerCount {
		go func() {
			defer wg.Done()
			for pos := range jobCh {
				if ctx.Err() != nil {
					continue
				}
				planIdx := indices[pos]
				res := stageCompressedVariantBounded(outDir, &plans[planIdx], opts, dictBytes, tracker)
				results[pos] = res
				if res.err != nil {
					cancel()
				}
			}
		}()
	}
	wg.Wait()

	for i := range results {
		if results[i].err != nil {
			return results, results[i].err
		}
	}
	return results, nil
}

func measureCandidateCompleteBytes(
	baseIdx *format.Index,
	stubOffset int64,
	dictBytes []byte,
	dictSHAHex string,
	results []parallelVariantResult,
	formatVersion int,
) (*format.Index, int64, int64, error) {
	cand := &format.Index{
		Version:     formatVersion,
		AppName:     baseIdx.AppName,
		TargetOS:    baseIdx.TargetOS,
		TargetArch:  baseIdx.TargetArch,
		CreatedUnix: baseIdx.CreatedUnix,
		Variants:    make([]format.VariantEntry, len(results)),
	}
	offset := stubOffset
	if len(dictBytes) > 0 {
		cand.DictionaryOffset = offset
		cand.DictionarySize = int64(len(dictBytes))
		cand.DictionarySHA256 = dictSHAHex
		cand.DictionaryID = codec.DefaultDictionaryID
		offset += cand.DictionarySize
	}
	for i := range results {
		entry := results[i].entry
		entry.Offset = offset
		offset += entry.CompressedSize
		cand.Variants[i] = entry
	}
	tailBytes, err := format.WriteIndexAndTrailerWithVersion(io.Discard, cand, offset, formatVersion)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("measuring candidate index and trailer: %w", err)
	}
	return cand, offset, offset + tailBytes, nil
}
