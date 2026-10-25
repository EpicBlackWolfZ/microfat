package codec

import (
	"math"
	"runtime"
	"runtime/debug"
	"sync"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/klauspost/compress/zstd"
)

const (
	// lz4DefaultBlockSize matches pierrec/lz4/v4's default 4 MiB independent block size.
	lz4DefaultBlockSize = 4 * 1024 * 1024

	// maxLZ4BlockConcurrency bounds per-call LZ4 worker goroutines on large payloads.
	maxLZ4BlockConcurrency = 8

	// zstdFastestSingleBlockLimit matches klauspost/compress/zstd SpeedFastest block size (64 KiB).
	zstdFastestSingleBlockLimit = 64 * 1024

	// zstdDefaultSingleBlockLimit matches klauspost/compress/zstd maxCompressedBlockSize (128 KiB).
	zstdDefaultSingleBlockLimit = 128 * 1024

	// zstdPipelineConcurrency bounds streaming Zstd to its 2-goroutine match/entropy pipeline.
	zstdPipelineConcurrency = 2

	// singleThreadConcurrency disables asynchronous worker goroutines on constrained or single-block paths.
	singleThreadConcurrency = 1

	// lowMemorySingleThreadCeiling forces single-threaded encoding under tight (< 256 MiB) memory limits.
	lowMemorySingleThreadCeiling int64 = 256 * 1024 * 1024

	// lz4HeadroomReserveBytes reserves non-worker headroom before admitting concurrent LZ4 block workers.
	lz4HeadroomReserveBytes int64 = 64 * 1024 * 1024

	// lz4BytesPerWorker conservatively budgets 16 MiB per concurrent 4 MiB LZ4 block worker.
	lz4BytesPerWorker int64 = 16 * 1024 * 1024

	// singleShotCapDivisor and singleShotOverheadBytes size the initial destination slice for single-block Zstd.
	singleShotCapDivisor    = 2
	singleShotOverheadBytes = 64
)

type codecResources struct {
	effectiveCPUs int
	memoryBytes   int64
	hasMemoryCap  bool
}

type zstdEncodePlan struct {
	singleShot  bool
	concurrency int
}

var readProcessCgroupLimits = sync.OnceValues(cgroup.ReadLimits)

func currentCodecResources() codecResources {
	return resolveCodecResourcesWith(runtime.GOMAXPROCS(0), debug.SetMemoryLimit(-1), readProcessCgroupLimits)
}

func resolveCodecResourcesWith(gomaxprocs int, goMemLimit int64, readLimits func() (cgroup.Limits, error)) codecResources {
	cpus := max(gomaxprocs, cgroup.MinimumCPUs)

	var memCap int64
	hasMem := false
	if goMemLimit > 0 && goMemLimit < math.MaxInt64 {
		memCap = goMemLimit
		hasMem = true
	}

	limits, err := readLimits()
	if err == nil {
		if limits.CPUs > 0 && limits.CPUs < cpus {
			cpus = limits.CPUs
		}
		if cgMem, ok := limits.EffectiveMemoryLimit(); ok {
			if !hasMem || cgMem < memCap {
				memCap = cgMem
				hasMem = true
			}
		}
	}

	return codecResources{
		effectiveCPUs: max(cpus, cgroup.MinimumCPUs),
		memoryBytes:   memCap,
		hasMemoryCap:  hasMem,
	}
}

func resolveLZ4Concurrency(payloadLen int) int {
	if payloadLen <= lz4DefaultBlockSize {
		return singleThreadConcurrency
	}
	if runtime.GOMAXPROCS(0) <= singleThreadConcurrency {
		return singleThreadConcurrency
	}
	return computeLZ4Concurrency(payloadLen, currentCodecResources())
}

func computeLZ4Concurrency(payloadLen int, res codecResources) int {
	if payloadLen <= lz4DefaultBlockSize || res.effectiveCPUs <= singleThreadConcurrency {
		return singleThreadConcurrency
	}

	cpus := res.effectiveCPUs
	if res.hasMemoryCap {
		if res.memoryBytes < lowMemorySingleThreadCeiling {
			return singleThreadConcurrency
		}
		avail := res.memoryBytes - int64(payloadLen) - lz4HeadroomReserveBytes
		if avail < lz4BytesPerWorker {
			return singleThreadConcurrency
		}
		cpus = min(cpus, int(avail/lz4BytesPerWorker))
	}

	blocks := (payloadLen + lz4DefaultBlockSize - 1) / lz4DefaultBlockSize
	return max(min(cpus, blocks, maxLZ4BlockConcurrency), singleThreadConcurrency)
}

func zstdSingleBlockLimit(level zstd.EncoderLevel) int {
	if level == zstd.SpeedFastest {
		return zstdFastestSingleBlockLimit
	}
	return zstdDefaultSingleBlockLimit
}

func resolveZstdEncodePlan(payloadLen int, level zstd.EncoderLevel, hasDict bool) zstdEncodePlan {
	limit := zstdSingleBlockLimit(level)
	if payloadLen == 0 {
		return zstdEncodePlan{singleShot: false, concurrency: singleThreadConcurrency}
	}
	if payloadLen > 0 && payloadLen < limit {
		return zstdEncodePlan{singleShot: true, concurrency: singleThreadConcurrency}
	}
	if runtime.GOMAXPROCS(0) <= singleThreadConcurrency {
		return zstdEncodePlan{singleShot: false, concurrency: singleThreadConcurrency}
	}
	return computeZstdEncodePlan(payloadLen, level, hasDict, currentCodecResources())
}

func computeZstdEncodePlan(payloadLen int, level zstd.EncoderLevel, hasDict bool, res codecResources) zstdEncodePlan {
	if payloadLen <= 0 {
		return zstdEncodePlan{singleShot: false, concurrency: singleThreadConcurrency}
	}
	if payloadLen < zstdSingleBlockLimit(level) {
		return zstdEncodePlan{singleShot: true, concurrency: singleThreadConcurrency}
	}
	if res.effectiveCPUs <= singleThreadConcurrency {
		return zstdEncodePlan{singleShot: false, concurrency: singleThreadConcurrency}
	}
	if !hasDict && res.hasMemoryCap && res.memoryBytes < lowMemorySingleThreadCeiling {
		return zstdEncodePlan{singleShot: false, concurrency: singleThreadConcurrency}
	}
	return zstdEncodePlan{singleShot: false, concurrency: zstdPipelineConcurrency}
}
