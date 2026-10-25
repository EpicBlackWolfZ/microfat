package codec_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/stretchr/testify/require"
)

const (
	benchPayloadSubThreshold        = 256 * 1024
	benchPayloadStandard            = 1024 * 1024
	benchDictSampleBytes            = 256 * 1024
	benchDictVariantCount           = 3
	benchSharedStride               = 64
	benchVariantPeriod              = 4
	benchSeedBase            uint64 = 0x9e3779b97f4a7c15
	benchSeedStep            uint64 = 0xbf58476d1ce4e5b9
	benchShiftA                     = 30
	benchShiftB                     = 27
	benchShiftC                     = 31
	benchMixMult             uint64 = 0x94d049bb133111eb
	benchLevelDefault               = "default"
	benchLevelBetter                = "better"
)

func buildBenchELFSamples(sampleSize, variantCount int) [][]byte {
	shared := make([]byte, sampleSize)
	state := benchSeedBase
	for idx := 0; idx < sampleSize; idx += benchSharedStride {
		state = mixBenchState(state + uint64(idx))
		end := min(idx+benchSharedStride, sampleSize)
		fillStructuredChunk(shared[idx:end], state)
	}

	samples := make([][]byte, variantCount)
	for v := range variantCount {
		buf := bytes.Clone(shared)
		vState := benchSeedBase ^ (benchSeedStep * uint64(v+1))
		for block := 0; block*benchSharedStride < sampleSize; block++ {
			if block%benchVariantPeriod != 0 {
				continue
			}
			vState = mixBenchState(vState)
			start := block * benchSharedStride
			end := min(start+benchSharedStride, sampleSize)
			fillStructuredChunk(buf[start:end], vState)
		}
		samples[v] = buf
	}
	return samples
}

func mixBenchState(x uint64) uint64 {
	x = (x ^ (x >> benchShiftA)) * benchSeedStep
	x = (x ^ (x >> benchShiftB)) * benchMixMult
	return x ^ (x >> benchShiftC)
}

func fillStructuredChunk(dst []byte, seed uint64) {
	const wordBytes = 8
	var tmp [wordBytes]byte
	// Structured x86-64/ELF-like motif interleaved with seeded immediate words.
	binary.LittleEndian.PutUint64(tmp[:], seed)
	for i := range dst {
		if i < len(dst)/2 {
			dst[i] = byte((i * 13) ^ int(tmp[i%wordBytes]&0x0f))
		} else {
			dst[i] = tmp[i%wordBytes] ^ byte(i)
		}
	}
}

type codecBenchSpec struct {
	name  string
	algo  string
	level string
}

func codecMatrixSpecs() []codecBenchSpec {
	return []codecBenchSpec{
		{name: "none", algo: codec.AlgorithmNone, level: ""},
		{name: "lz4/fastest", algo: codec.AlgorithmLZ4, level: testLevelFastest},
		{name: "lz4/better", algo: codec.AlgorithmLZ4, level: benchLevelBetter},
		{name: "lz4/best", algo: codec.AlgorithmLZ4, level: testLevelBest},
		{name: "zstd/fastest", algo: codec.AlgorithmZstd, level: testLevelFastest},
		{name: "zstd/default", algo: codec.AlgorithmZstd, level: benchLevelDefault},
		{name: "zstd/better", algo: codec.AlgorithmZstd, level: benchLevelBetter},
		{name: "zstd/best", algo: codec.AlgorithmZstd, level: testLevelBest},
	}
}

func benchDictLevels() []string {
	return []string{testLevelFastest, benchLevelDefault, benchLevelBetter, testLevelBest}
}

func TestCodecBenchmarkMatrixFixtures(t *testing.T) {
	t.Parallel()

	samples := buildBenchELFSamples(benchDictSampleBytes, benchDictVariantCount)
	dict, err := codec.TrainDictionary(samples, codec.DefaultDictSize, testLevelBest)
	require.NoError(t, err)
	require.NotEmpty(t, dict)

	payload := samples[0]
	for _, spec := range codecMatrixSpecs() {
		t.Run(spec.name, func(t *testing.T) {
			t.Parallel()
			c, getErr := codec.Get(spec.algo)
			require.NoError(t, getErr)

			var compressed, decoded bytes.Buffer
			require.NoError(t, c.Compress(&compressed, payload, spec.level))
			require.NoError(t, c.Decompress(&decoded, bytes.NewReader(compressed.Bytes()), int64(len(payload))))
			require.Equal(t, payload, decoded.Bytes())
		})
	}

	zstdCodec := codec.NewZstdCodec()
	for _, level := range benchDictLevels() {
		t.Run("dict/"+level, func(t *testing.T) {
			t.Parallel()
			var compressed, decoded bytes.Buffer
			require.NoError(t, zstdCodec.CompressWithDict(&compressed, payload, level, dict))
			require.NoError(t, zstdCodec.DecompressWithDict(&decoded, bytes.NewReader(compressed.Bytes()), int64(len(payload)), dict))
			require.Equal(t, payload, decoded.Bytes())
		})
	}
}

// BenchmarkCodecCompressMatrix measures isolated in-memory encoding throughput and allocations
// across supported codecs, explicit levels, and profile resolutions (distinct from full-process startup/p99).
func BenchmarkCodecCompressMatrix(b *testing.B) {
	payload := buildBenchELFSamples(benchPayloadStandard, 1)[0]
	subThreshold := buildBenchELFSamples(benchPayloadSubThreshold, 1)[0]

	for _, spec := range codecMatrixSpecs() {
		b.Run(spec.name, func(b *testing.B) {
			c, err := codec.Get(spec.algo)
			if err != nil {
				b.Fatalf("codec.Get(%q): %v", spec.algo, err)
			}
			var out bytes.Buffer
			out.Grow(len(payload))
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			var frameBytes int
			for b.Loop() {
				out.Reset()
				if err := c.Compress(&out, payload, spec.level); err != nil {
					b.Fatalf("Compress(%s): %v", spec.name, err)
				}
				frameBytes = out.Len()
			}
			b.ReportMetric(float64(frameBytes), "frame-bytes")
		})
	}

	profileCases := []struct {
		name    string
		profile string
		data    []byte
	}{
		{name: "profile/latency-256k", profile: codec.ProfileLatency, data: subThreshold},
		{name: "profile/latency-1m", profile: codec.ProfileLatency, data: payload},
		{name: "profile/balanced-1m", profile: codec.ProfileBalanced, data: payload},
		{name: "profile/size-1m", profile: codec.ProfileSize, data: payload},
	}
	for _, tc := range profileCases {
		b.Run(tc.name, func(b *testing.B) {
			c, level, err := codec.ResolveCompression(tc.profile, "", "", int64(len(tc.data)))
			if err != nil {
				b.Fatalf("ResolveCompression(%q): %v", tc.profile, err)
			}
			var out bytes.Buffer
			out.Grow(len(tc.data))
			b.SetBytes(int64(len(tc.data)))
			b.ReportAllocs()
			for b.Loop() {
				out.Reset()
				if err := c.Compress(&out, tc.data, level); err != nil {
					b.Fatalf("Compress(%s): %v", tc.name, err)
				}
			}
		})
	}
}

// BenchmarkCodecDecompressMatrix measures isolated in-memory stream decoding across codecs and levels.
func BenchmarkCodecDecompressMatrix(b *testing.B) {
	payload := buildBenchELFSamples(benchPayloadStandard, 1)[0]
	for _, spec := range codecMatrixSpecs() {
		b.Run(spec.name, func(b *testing.B) {
			c, err := codec.Get(spec.algo)
			if err != nil {
				b.Fatalf("codec.Get(%q): %v", spec.algo, err)
			}
			var frame bytes.Buffer
			if err := c.Compress(&frame, payload, spec.level); err != nil {
				b.Fatalf("Compress(%s): %v", spec.name, err)
			}
			compBytes := frame.Bytes()
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			for b.Loop() {
				if err := c.Decompress(io.Discard, bytes.NewReader(compBytes), int64(len(payload))); err != nil {
					b.Fatalf("Decompress(%s): %v", spec.name, err)
				}
			}
		})
	}
}

// BenchmarkCodecDictMatrix measures Zstandard dictionary compression and decompression across levels.
func BenchmarkCodecDictMatrix(b *testing.B) {
	samples := buildBenchELFSamples(benchDictSampleBytes, benchDictVariantCount)
	dict, err := codec.TrainDictionary(samples, codec.DefaultDictSize, testLevelBest)
	if err != nil {
		b.Fatalf("TrainDictionary: %v", err)
	}
	payload := samples[benchDictVariantCount-1]
	zstdCodec := codec.NewZstdCodec()

	for _, level := range benchDictLevels() {
		b.Run("CompressWithDict/"+level, func(b *testing.B) {
			var out bytes.Buffer
			out.Grow(len(payload))
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			var frameBytes int
			for b.Loop() {
				out.Reset()
				if err := zstdCodec.CompressWithDict(&out, payload, level, dict); err != nil {
					b.Fatalf("CompressWithDict(%s): %v", level, err)
				}
				frameBytes = out.Len()
			}
			b.ReportMetric(float64(frameBytes), "frame-bytes")
		})

		b.Run("DecompressWithDict/"+level, func(b *testing.B) {
			var frame bytes.Buffer
			if err := zstdCodec.CompressWithDict(&frame, payload, level, dict); err != nil {
				b.Fatalf("CompressWithDict(%s): %v", level, err)
			}
			compBytes := frame.Bytes()
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			for b.Loop() {
				if err := zstdCodec.DecompressWithDict(io.Discard, bytes.NewReader(compBytes), int64(len(payload)), dict); err != nil {
					b.Fatalf("DecompressWithDict(%s): %v", level, err)
				}
			}
		})
	}
}

// BenchmarkTrainDictionaryMatrix measures Zstandard shared dictionary training across encoder levels.
func BenchmarkTrainDictionaryMatrix(b *testing.B) {
	samples := buildBenchELFSamples(benchDictSampleBytes, benchDictVariantCount)
	var totalSampleBytes int64
	for _, s := range samples {
		totalSampleBytes += int64(len(s))
	}
	for _, level := range benchDictLevels() {
		b.Run(level, func(b *testing.B) {
			b.SetBytes(totalSampleBytes)
			b.ReportAllocs()
			for b.Loop() {
				dict, err := codec.TrainDictionary(samples, codec.DefaultDictSize, level)
				if err != nil || len(dict) == 0 {
					b.Fatalf("TrainDictionary(%s): %v", level, err)
				}
			}
		})
	}
}
