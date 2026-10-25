package pack

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	benchELFVariantSub512KiB        = 256 * 1024
	benchELFVariantAbove512K        = 540 * 1024
	benchELFInspectVariant1M        = 1024 * 1024
	benchStubSize                   = 64 * 1024
	benchPh0FileszOffset            = 64 + 32
	benchPh0MemszOffset             = 64 + 40
	benchMemszHeadroom              = 0x1000
	benchBlockStride                = 64
	benchDistinctBlockMod           = 4
	benchELFSeedBase         uint64 = 0x517cc1b727220a95
	benchELFSeedVariant      uint64 = 0x94d049bb133111eb
)

type validatedELFBenchFixture struct {
	stubPath     string
	variants     map[string]string
	levels       []string
	payloadBytes int64
}

func createValidatedELFBenchmarkFixture(tb testing.TB, variantSize int) validatedELFBenchFixture {
	tb.Helper()
	dir := tb.TempDir()

	baseELF := buildSyntheticELF(tb, syntheticELFOpts{
		interp:       testLdLinux,
		dependencies: []string{testLibc, testLibm},
		verneeds: []syntheticVerneed{
			{
				file: testLibc,
				versions: []syntheticVernaux{
					{name: testGlibc225},
					{name: testGlibc214},
				},
			},
		},
	})
	require.Less(tb, len(baseELF), variantSize)

	stubImage := padValidSyntheticELF(baseELF, max(benchStubSize, len(baseELF)), 0)
	stubPath := filepath.Join(dir, "microfat-stub")
	require.NoError(tb, os.WriteFile(stubPath, stubImage, 0o755))

	levels := []string{"v1", "v2", "v3"}
	variants := make(map[string]string, len(levels))
	var totalBytes int64
	for idx, lvl := range levels {
		img := padValidSyntheticELF(baseELF, variantSize, idx+1)
		p := filepath.Join(dir, "app-"+lvl)
		require.NoError(tb, os.WriteFile(p, img, 0o755))
		variants[lvl] = p
		totalBytes += int64(len(img))
	}

	return validatedELFBenchFixture{
		stubPath:     stubPath,
		variants:     variants,
		levels:       levels,
		payloadBytes: totalBytes,
	}
}

func padValidSyntheticELF(base []byte, targetSize, variantID int) []byte {
	img := make([]byte, targetSize)
	copy(img, base)

	tail := img[len(base):]
	sharedSeed := benchELFSeedBase
	for off := 0; off < len(tail); off += benchBlockStride {
		sharedSeed ^= sharedSeed >> 29
		sharedSeed *= benchELFSeedVariant
		end := min(off+benchBlockStride, len(tail))
		blockIdx := off / benchBlockStride
		seed := sharedSeed
		if variantID > 0 && blockIdx%benchDistinctBlockMod == 0 {
			seed ^= uint64(variantID) * benchELFSeedBase
		}
		var word [8]byte
		binary.LittleEndian.PutUint64(word[:], seed)
		for i := off; i < end; i++ {
			tail[i] = word[i%len(word)] ^ byte((i*7)&0x3f)
		}
	}

	unsignedLen := uint64(len(img))
	binary.LittleEndian.PutUint64(img[benchPh0FileszOffset:], unsignedLen)
	binary.LittleEndian.PutUint64(img[benchPh0MemszOffset:], unsignedLen+benchMemszHeadroom)
	return img
}

type packMatrixCase struct {
	name          string
	formatVersion int
	profile       string
	compression   string
	enableDict    bool
	expectDict    bool
	above512KiB   bool
}

func packBenchmarkMatrixCases() []packMatrixCase {
	return []packMatrixCase{
		{name: "v2/latency-sub512k", formatVersion: format.FormatVersion2, profile: codec.ProfileLatency},
		{name: "v2/latency-lz4", formatVersion: format.FormatVersion2, profile: codec.ProfileLatency, above512KiB: true},
		{name: "v2/balanced", formatVersion: format.FormatVersion2, profile: codec.ProfileBalanced},
		{name: "v2/balanced-dict", formatVersion: format.FormatVersion2, profile: codec.ProfileBalanced,
			enableDict: true, expectDict: true},
		{name: "v2/zstd-best-nodict", formatVersion: format.FormatVersion2, profile: codec.ProfileBalanced,
			compression: codec.AlgorithmZstd + ":" + compressionBest},
		{name: "v2/size-dict", formatVersion: format.FormatVersion2, profile: codec.ProfileSize, expectDict: true},
		{name: "v1/balanced", formatVersion: format.FormatVersion1, profile: codec.ProfileBalanced},
		{name: "v1/size-dict", formatVersion: format.FormatVersion1, profile: codec.ProfileSize, expectDict: true},
	}
}

func TestPackValidatedBenchmarkFixtures(t *testing.T) {
	t.Parallel()
	fixtureSmall := createValidatedELFBenchmarkFixture(t, benchELFVariantSub512KiB)
	fixtureLarge := createValidatedELFBenchmarkFixture(t, benchELFVariantAbove512K)

	for _, tc := range packBenchmarkMatrixCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := fixtureSmall
			if tc.above512KiB {
				fx = fixtureLarge
			}
			outPath := filepath.Join(t.TempDir(), "fat-app")
			var abiReport *ArtifactABIReport
			idx, err := Pack(Options{
				StubPath:          fx.stubPath,
				OutputPath:        outPath,
				AppName:           "bench-validated-app",
				TargetOS:          testOSLinux,
				TargetArch:        testArchAMD64,
				FormatVersion:     tc.formatVersion,
				Profile:           tc.profile,
				Compression:       tc.compression,
				EnableDict:        tc.enableDict,
				Variants:          fx.variants,
				ABIReportCallback: func(rep *ArtifactABIReport) { abiReport = rep },
			})
			require.NoError(t, err)
			require.NotNil(t, idx)
			require.NotNil(t, abiReport)
			assert.Equal(t, ComparisonConsistent, abiReport.Status)
			assert.Equal(t, tc.expectDict, idx.DictionarySize > 0)

			f, openErr := os.Open(outPath)
			require.NoError(t, openErr)
			defer func() { _ = f.Close() }()
			st, statErr := f.Stat()
			require.NoError(t, statErr)

			verifiedIdx, results, verifyErr := VerifyBinary(f, st.Size())
			require.NoError(t, verifyErr)
			assert.Equal(t, tc.formatVersion, verifiedIdx.Version)
			require.Len(t, results, len(fx.levels))
			for _, res := range results {
				assert.True(t, res.Valid)
			}
		})
	}
}

// BenchmarkPackValidatedMatrix measures end-to-end in-process Pack across FormatVersion1/FormatVersion2,
// ELF header/platform validation, ABI note/dynamic inspection, and latency/balanced/size profiles
// (distinct from child process cold-start and HTTP service p99 benchmarks).
func BenchmarkPackValidatedMatrix(b *testing.B) {
	fixtureSmall := createValidatedELFBenchmarkFixture(b, benchELFVariantSub512KiB)
	fixtureLarge := createValidatedELFBenchmarkFixture(b, benchELFVariantAbove512K)

	for _, tc := range packBenchmarkMatrixCases() {
		b.Run(tc.name, func(b *testing.B) {
			fx := fixtureSmall
			if tc.above512KiB {
				fx = fixtureLarge
			}
			outDir := b.TempDir()
			outPath := filepath.Join(outDir, "fat-bench")
			opts := Options{
				StubPath:      fx.stubPath,
				OutputPath:    outPath,
				AppName:       "bench-validated-app",
				TargetOS:      testOSLinux,
				TargetArch:    testArchAMD64,
				FormatVersion: tc.formatVersion,
				Profile:       tc.profile,
				Compression:   tc.compression,
				EnableDict:    tc.enableDict,
				Variants:      fx.variants,
			}

			b.SetBytes(fx.payloadBytes)
			b.ReportAllocs()
			var artifactBytes int64
			for b.Loop() {
				if _, err := Pack(opts); err != nil {
					b.Fatalf("Pack(%s): %v", tc.name, err)
				}
				if st, err := os.Stat(outPath); err == nil {
					artifactBytes = st.Size()
				}
			}
			b.ReportMetric(float64(artifactBytes), "artifact-bytes")
		})
	}
}

// BenchmarkPackELFAndABIInspection isolates ELF header validation and multi-variant ABI inspection
// on 1 MiB dynamic ELF binaries so redundant-read / allocation optimizations are directly observable.
func BenchmarkPackELFAndABIInspection(b *testing.B) {
	fx := createValidatedELFBenchmarkFixture(b, benchELFInspectVariant1M)
	opts := Options{
		StubPath:   fx.stubPath,
		AppName:    "bench-abi-app",
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants:   fx.variants,
	}

	b.SetBytes(fx.payloadBytes)
	b.ReportAllocs()
	for b.Loop() {
		for _, lvl := range fx.levels {
			if err := ValidateELFBinary(fx.variants[lvl], opts.TargetOS, opts.TargetArch); err != nil {
				b.Fatalf("ValidateELFBinary(%s): %v", lvl, err)
			}
		}
		if err := validatePayloadABIs(&opts, fx.levels); err != nil {
			b.Fatalf("validatePayloadABIs: %v", err)
		}
	}
}

func TestPadValidSyntheticELFPreservesReader(t *testing.T) {
	t.Parallel()
	base := buildSyntheticELF(t, syntheticELFOpts{interp: testLdLinux})
	padded := padValidSyntheticELF(base, benchStubSize, 1)
	require.Equal(t, benchStubSize, len(padded))
	require.True(t, bytes.HasPrefix(padded, []byte{0x7f, 'E', 'L', 'F'}))
}
