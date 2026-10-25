package codec

import (
	"bytes"
	"errors"
	"math"
	"os/exec"
	"runtime"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countdownFailWriter struct {
	remainingBytes int
	err            error
}

func (w *countdownFailWriter) Write(p []byte) (int, error) {
	if w.remainingBytes <= 0 {
		return 0, w.err
	}
	if len(p) > w.remainingBytes {
		n := w.remainingBytes
		w.remainingBytes = 0
		return n, w.err
	}
	w.remainingBytes -= len(p)
	return len(p), nil
}

func makeDeterministicPayload(size int) []byte {
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte((i*31 + i>>9) & 0xff)
	}
	return buf
}

func TestResolveCodecResourcesWith(t *testing.T) {
	t.Parallel()

	const (
		mem128MiB int64 = 128 * 1024 * 1024
		mem512MiB int64 = 512 * 1024 * 1024
	)

	t.Run("unlimited host and unset gomemlimit", func(t *testing.T) {
		t.Parallel()
		res := resolveCodecResourcesWith(8, math.MaxInt64, func() (cgroup.Limits, error) {
			return cgroup.Limits{}, nil
		})
		assert.Equal(t, 8, res.effectiveCPUs)
		assert.False(t, res.hasMemoryCap)
		assert.Zero(t, res.memoryBytes)
	})

	t.Run("clamps cpus and picks tighter cgroup memory limit", func(t *testing.T) {
		t.Parallel()
		res := resolveCodecResourcesWith(16, mem512MiB, func() (cgroup.Limits, error) {
			return cgroup.Limits{
				CPUs:                      2,
				MemoryLimitBytes:          mem128MiB,
				EffectiveMemoryLimitBytes: mem128MiB,
				MemoryLimitFinite:         true,
			}, nil
		})
		assert.Equal(t, 2, res.effectiveCPUs)
		assert.True(t, res.hasMemoryCap)
		assert.Equal(t, mem128MiB, res.memoryBytes)
	})

	t.Run("keeps tighter gomemlimit when smaller than cgroup", func(t *testing.T) {
		t.Parallel()
		res := resolveCodecResourcesWith(0, mem128MiB, func() (cgroup.Limits, error) {
			return cgroup.Limits{
				CPUs:                      4,
				MemoryLimitBytes:          mem512MiB,
				EffectiveMemoryLimitBytes: mem512MiB,
				MemoryLimitFinite:         true,
			}, nil
		})
		assert.Equal(t, cgroup.MinimumCPUs, res.effectiveCPUs)
		assert.True(t, res.hasMemoryCap)
		assert.Equal(t, mem128MiB, res.memoryBytes)
	})

	t.Run("falls back cleanly when cgroup read fails", func(t *testing.T) {
		t.Parallel()
		res := resolveCodecResourcesWith(4, math.MaxInt64, func() (cgroup.Limits, error) {
			return cgroup.Limits{}, errors.New("cgroup unavailable")
		})
		assert.Equal(t, 4, res.effectiveCPUs)
		assert.False(t, res.hasMemoryCap)
	})

	t.Run("zero gomemlimit remains a finite cap", func(t *testing.T) {
		t.Parallel()
		res := resolveCodecResourcesWith(8, 0, func() (cgroup.Limits, error) {
			return cgroup.Limits{}, nil
		})
		assert.True(t, res.hasMemoryCap)
		assert.Zero(t, res.memoryBytes)
		assert.Equal(t, singleThreadConcurrency, computeLZ4Concurrency(lz4DefaultBlockSize*2, res))
		assert.Equal(t, singleThreadConcurrency,
			computeZstdEncodePlan(zstdDefaultSingleBlockLimit*2, zstd.SpeedDefault, false, res).concurrency)
	})
}

func TestComputeLZ4Concurrency(t *testing.T) {
	t.Parallel()

	const (
		mem128MiB int64 = 128 * 1024 * 1024
		mem300MiB int64 = 300 * 1024 * 1024
		mem512MiB int64 = 512 * 1024 * 1024
		size9MiB        = 9 * 1024 * 1024
		size64MiB       = 64 * 1024 * 1024
	)

	cases := []struct {
		name       string
		payloadLen int
		res        codecResources
		want       int
	}{
		{"empty payload", 0, codecResources{effectiveCPUs: 8}, singleThreadConcurrency},
		{"single 4MiB block", lz4DefaultBlockSize, codecResources{effectiveCPUs: 8}, singleThreadConcurrency},
		{"two blocks on 4 CPUs", lz4DefaultBlockSize + 1, codecResources{effectiveCPUs: 4}, 2},
		{"three blocks on 8 CPUs", size9MiB, codecResources{effectiveCPUs: 8}, 3},
		{"sixteen blocks capped at 8", size64MiB, codecResources{effectiveCPUs: 16}, maxLZ4BlockConcurrency},
		{"single CPU host", size9MiB, codecResources{effectiveCPUs: 1}, singleThreadConcurrency},
		{
			"tight 128MiB container",
			size9MiB,
			codecResources{effectiveCPUs: 8, memoryBytes: mem128MiB, hasMemoryCap: true},
			singleThreadConcurrency,
		},
		{
			"payload exhausts worker headroom",
			230 * 1024 * 1024,
			codecResources{effectiveCPUs: 8, memoryBytes: mem300MiB, hasMemoryCap: true},
			singleThreadConcurrency,
		},
		{
			"memory headroom caps workers",
			400 * 1024 * 1024,
			codecResources{effectiveCPUs: 8, memoryBytes: mem512MiB, hasMemoryCap: true},
			3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, computeLZ4Concurrency(tc.payloadLen, tc.res))
		})
	}
}

func TestComputeZstdEncodePlan(t *testing.T) {
	t.Parallel()

	const mem128MiB int64 = 128 * 1024 * 1024

	cpu4 := codecResources{effectiveCPUs: 4}
	cpu1 := codecResources{effectiveCPUs: 1}
	tightMem := codecResources{effectiveCPUs: 4, memoryBytes: mem128MiB, hasMemoryCap: true}
	planSync := zstdEncodePlan{singleShot: false, concurrency: 1}
	planSingle := zstdEncodePlan{singleShot: true, concurrency: 1}
	planPipe := zstdEncodePlan{singleShot: false, concurrency: 2}

	cases := []struct {
		name       string
		payloadLen int
		level      zstd.EncoderLevel
		hasDict    bool
		res        codecResources
		want       zstdEncodePlan
	}{
		{"negative length", -1, zstd.SpeedDefault, false, cpu4, planSync},
		{"empty payload", 0, zstd.SpeedBetterCompression, false, cpu4, planSync},
		{"fastest below 64KiB", zstdFastestSingleBlockLimit - 1, zstd.SpeedFastest, false, cpu4, planSingle},
		{"fastest at 64KiB", zstdFastestSingleBlockLimit, zstd.SpeedFastest, false, cpu4, planPipe},
		{"better below 128KiB", zstdDefaultSingleBlockLimit - 1, zstd.SpeedBetterCompression, true, cpu4, planSingle},
		{"better at 128KiB", zstdDefaultSingleBlockLimit, zstd.SpeedBetterCompression, false, cpu4, planPipe},
		{"single CPU multi-block", zstdDefaultSingleBlockLimit, zstd.SpeedBestCompression, false, cpu1, planSync},
		{"tight memory without dict", zstdDefaultSingleBlockLimit, zstd.SpeedBetterCompression, false, tightMem, planSync},
		{"tight memory with dict keeps pipeline parity", zstdDefaultSingleBlockLimit, zstd.SpeedBetterCompression, true, tightMem, planPipe},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, computeZstdEncodePlan(tc.payloadLen, tc.level, tc.hasDict, tc.res))
		})
	}
}

func TestLZ4EncoderWorkspaceBytes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		payloadLen int64
		want       int64
	}{
		{"negative payload", -1, lz4BytesPerWorker},
		{"empty payload", 0, lz4BytesPerWorker},
		{"single block", lz4DefaultBlockSize, lz4BytesPerWorker},
		{"two blocks and pending buffer", lz4DefaultBlockSize + 1, 3 * lz4BytesPerWorker},
		{"eight blocks and pending buffer", 8 * lz4DefaultBlockSize, 9 * lz4BytesPerWorker},
		{"nine blocks and pending buffer", 9 * lz4DefaultBlockSize, 10 * lz4BytesPerWorker},
		{"huge payload remains bounded", math.MaxInt64, 10 * lz4BytesPerWorker},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, LZ4EncoderWorkspaceBytes(tc.payloadLen))
		})
	}
}

func TestLiveAdmissionResolversSingleAndMultiCPU(t *testing.T) {
	// Not parallel: temporarily adjusts process GOMAXPROCS.
	origProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(origProcs)

	assert.Equal(t, singleThreadConcurrency, resolveLZ4Concurrency(lz4DefaultBlockSize))
	assert.Equal(t, singleThreadConcurrency, resolveLZ4Concurrency(lz4DefaultBlockSize+1024))
	assert.Equal(t, zstdEncodePlan{singleShot: false, concurrency: 1}, resolveZstdEncodePlan(0, zstd.SpeedBetterCompression, false))
	assert.Equal(t, zstdEncodePlan{singleShot: true, concurrency: 1}, resolveZstdEncodePlan(1024, zstd.SpeedBetterCompression, false))
	assert.Equal(
		t,
		zstdEncodePlan{singleShot: false, concurrency: 1},
		resolveZstdEncodePlan(zstdDefaultSingleBlockLimit*2, zstd.SpeedBetterCompression, false),
	)

	runtime.GOMAXPROCS(4)
	assert.GreaterOrEqual(t, resolveLZ4Concurrency(lz4DefaultBlockSize*2), singleThreadConcurrency)
	plan := resolveZstdEncodePlan(zstdDefaultSingleBlockLimit*2, zstd.SpeedBetterCompression, false)
	assert.False(t, plan.singleShot)
	assert.GreaterOrEqual(t, plan.concurrency, singleThreadConcurrency)
}

func TestZstdSingleShotAndStreamingByteParityAndErrors(t *testing.T) {
	t.Parallel()

	const (
		dictSamplesCount   = 40
		dictLinesPerSample = 50
		dictTargetBytes    = 32 * 1024
		multiBlockBytes    = 256 * 1024
	)

	samples := make([][]byte, dictSamplesCount)
	for i := range samples {
		var sb bytes.Buffer
		for j := range dictLinesPerSample {
			_, _ = sb.WriteString("GO_MICROFAT_RUNTIME_SYMBOL_TABLE_ENTRY_")
			_, _ = sb.WriteString(string(rune('A' + i%26)))
			sb.WriteByte('_')
			sb.WriteByte(byte('0' + (i/10)%10))
			sb.WriteByte(byte('0' + i%10))
			sb.WriteByte('_')
			sb.WriteByte(byte('0' + (j/10)%10))
			sb.WriteByte(byte('0' + j%10))
			_, _ = sb.WriteString("_common_static_dictionary_builder_line\n")
		}
		samples[i] = sb.Bytes()
	}
	dict, err := TrainDictionary(samples, dictTargetBytes, "better")
	require.NoError(t, err)

	fullPayload := makeDeterministicPayload(DefaultLatencyUncompressedThreshold * 2)
	zCodec := NewZstdCodec()

	sizes := []int{
		0,
		1,
		512,
		zstdFastestSingleBlockLimit - 1,
		zstdFastestSingleBlockLimit,
		zstdDefaultSingleBlockLimit - 1,
		zstdDefaultSingleBlockLimit,
		DefaultLatencyUncompressedThreshold,
		len(fullPayload),
	}

	for _, withDict := range []bool{false, true} {
		for _, level := range []string{"fastest", "default", "better", "best"} {
			encLevel, mapErr := zCodec.MapLevel(level)
			require.NoError(t, mapErr)
			for _, sz := range sizes {
				src := fullPayload[:sz]
				var want bytes.Buffer
				opts := []zstd.EOption{zstd.WithEncoderLevel(encLevel), zstd.WithEncoderConcurrency(zstdPipelineConcurrency)}
				if withDict {
					opts = append(opts, zstd.WithEncoderDict(dict))
				}
				refW, refErr := zstd.NewWriter(&want, opts...)
				require.NoError(t, refErr)
				_, err = refW.Write(src)
				require.NoError(t, err)
				require.NoError(t, refW.Close())

				var got, roundtrip bytes.Buffer
				var activeDict []byte
				if withDict {
					activeDict = dict
				}
				require.NoError(t, zCodec.CompressWithDict(&got, src, level, activeDict))
				assert.True(t, bytes.Equal(want.Bytes(), got.Bytes()), "dict=%v level=%s size=%d", withDict, level, sz)

				require.NoError(t, zCodec.DecompressWithDict(&roundtrip, bytes.NewReader(got.Bytes()), int64(len(src)), activeDict))
				assert.True(t, bytes.Equal(src, roundtrip.Bytes()))
			}
		}
	}

	corruptDict := []byte{0x37, 0xa4, 0x30, 0xec, 0x01}
	var sink bytes.Buffer
	err = zCodec.CompressWithDict(&sink, fullPayload[:1024], "fastest", corruptDict)
	require.ErrorContains(t, err, "initializing zstd encoder")

	err = zCodec.CompressWithDict(&sink, fullPayload[:multiBlockBytes], "better", corruptDict)
	require.ErrorContains(t, err, "initializing zstd writer")

	writeBoom := errors.New("write boom")
	err = zCodec.CompressWithDict(&countdownFailWriter{remainingBytes: 0, err: writeBoom}, nil, "fastest", nil)
	require.ErrorIs(t, err, writeBoom)

	err = zCodec.CompressWithDict(&countdownFailWriter{remainingBytes: 0, err: writeBoom}, fullPayload[:multiBlockBytes], "fastest", nil)
	require.ErrorIs(t, err, writeBoom)

	err = zCodec.CompressWithDict(&countdownFailWriter{remainingBytes: 4, err: writeBoom}, fullPayload[:multiBlockBytes], "better", nil)
	require.ErrorIs(t, err, writeBoom)
}

func TestLZ4ConcurrentBlockByteParityAndCompatibility(t *testing.T) {
	t.Parallel()

	const multiBlockPayloadSize = 9 * 1024 * 1024 // 3 independent 4 MiB blocks

	fullPayload := makeDeterministicPayload(multiBlockPayloadSize)
	lzCodec := NewLZ4Codec()

	sizes := []int{
		0,
		1,
		DefaultLatencyUncompressedThreshold - 1,
		DefaultLatencyUncompressedThreshold,
		lz4DefaultBlockSize,
		lz4DefaultBlockSize + 1,
		multiBlockPayloadSize,
	}

	for _, level := range []string{"fast", "better", "best"} {
		mapped, err := lzCodec.MapLevel(level)
		require.NoError(t, err)

		for _, sz := range sizes {
			if level == "best" && sz > lz4DefaultBlockSize+1 {
				continue
			}
			src := fullPayload[:sz]

			var singleBuf bytes.Buffer
			refW := lz4.NewWriter(&singleBuf)
			require.NoError(t, refW.Apply(lz4.CompressionLevelOption(mapped), lz4.ConcurrencyOption(1)))
			_, err = refW.Write(src)
			require.NoError(t, err)
			require.NoError(t, refW.Close())

			var gotBuf, decodedBuf, rawDecoded bytes.Buffer
			require.NoError(t, lzCodec.Compress(&gotBuf, src, level))
			assert.True(t, bytes.Equal(singleBuf.Bytes(), gotBuf.Bytes()), "level=%s size=%d", level, sz)

			require.NoError(t, lzCodec.Decompress(&decodedBuf, bytes.NewReader(gotBuf.Bytes()), int64(len(src))))
			assert.True(t, bytes.Equal(src, decodedBuf.Bytes()))

			_, err = rawDecoded.ReadFrom(lz4.NewReader(bytes.NewReader(gotBuf.Bytes())))
			require.NoError(t, err)
			assert.True(t, bytes.Equal(src, rawDecoded.Bytes()))
		}
	}

	if lz4Bin, lookErr := exec.LookPath("lz4"); lookErr == nil {
		var frame bytes.Buffer
		require.NoError(t, lzCodec.Compress(&frame, fullPayload, "fast"))
		cmd := exec.Command(lz4Bin, "-d", "-c")
		cmd.Stdin = bytes.NewReader(frame.Bytes())
		out, runErr := cmd.Output()
		require.NoError(t, runErr)
		assert.Equal(t, fullPayload, out)
	}

	const lz4FrameHeaderBytes = 7
	lzWriteErr := errors.New("lz4 close write error")
	err := lzCodec.Compress(&countdownFailWriter{remainingBytes: lz4FrameHeaderBytes, err: lzWriteErr}, fullPayload[:1024], "fast")
	require.ErrorIs(t, err, lzWriteErr)
}
