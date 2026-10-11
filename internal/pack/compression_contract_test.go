package pack

import (
	"bytes"
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	compressionAliasV1 = "amd64_v1"
	compressionBest    = "best"
)

func compressionContractInput(t *testing.T) (string, []byte) {
	t.Helper()
	path := createDummyELF(t, t.TempDir(), "input", uint16(elf.EM_X86_64), byte(elf.ELFCLASS64))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	data = append(data, bytes.Repeat([]byte("compressible payload with repeated symbols\n"), 1024)...)
	require.NoError(t, os.WriteFile(path, data, 0o755))
	return path, data
}

func assertPackedCompression(t *testing.T, opts Options, payload []byte, algorithm, level string) {
	t.Helper()
	idx, err := Pack(opts)
	require.NoError(t, err)
	require.Len(t, idx.Variants, 1)
	assert.Equal(t, algorithm, idx.Variants[0].Compression)
	data, err := os.ReadFile(opts.OutputPath)
	require.NoError(t, err)
	verified, results, err := VerifyBinary(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	assert.Equal(t, algorithm, verified.Variants[0].Compression)
	require.Len(t, results, 1)
	assert.True(t, results[0].Valid)
	assert.NoError(t, results[0].Error)
	c, err := codec.Get(algorithm)
	require.NoError(t, err)
	var control bytes.Buffer
	require.NoError(t, c.Compress(&control, payload, level))
	entry := idx.Variants[0]
	assert.Equal(t, control.Bytes(), data[entry.Offset:entry.Offset+entry.CompressedSize])
}

func TestPackCompressionOverrideAliases(t *testing.T) {
	t.Parallel()
	input, payload := compressionContractInput(t)
	aliases := []struct{ variant, override string }{
		{"v1", compressionAliasV1}, {"v1", "V1"}, {"V1", "AMD64_V1"},
		{compressionAliasV1, "V1"}, {"AMD64_V1", "v1"}, {"v1", "v1"},
	}
	overrides := []struct {
		name, algorithm, level string
		options                VariantCompressionOptions
	}{
		{"codec", codec.AlgorithmNone, "", VariantCompressionOptions{Compression: codec.AlgorithmNone}},
		{"profile", codec.AlgorithmZstd, compressionBest, VariantCompressionOptions{Profile: codec.ProfileSize}},
		{"level", codec.AlgorithmZstd, testCompressionFastest, VariantCompressionOptions{Level: testCompressionFastest}},
	}
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, alias := range aliases {
			for _, override := range overrides {
				t.Run(fmt.Sprintf("format%d/%s/%s/%s", version, alias.variant, alias.override, override.name), func(t *testing.T) {
					t.Parallel()
					opts := DefaultOptions()
					opts.StubPath, opts.OutputPath = input, filepath.Join(t.TempDir(), "output")
					opts.FormatVersion = version
					opts.Variants = map[string]string{alias.variant: input}
					opts.VariantCompression = map[string]VariantCompressionOptions{alias.override: override.options}
					assertPackedCompression(t, opts, payload, override.algorithm, override.level)
				})
			}
		}
	}
}

func TestPackCompressionRejectsConflictingAliases(t *testing.T) {
	t.Parallel()
	input, _ := compressionContractInput(t)
	for _, alias := range []string{"V1", compressionAliasV1} {
		t.Run(alias, func(t *testing.T) {
			t.Parallel()
			opts := DefaultOptions()
			opts.StubPath, opts.OutputPath = input, filepath.Join(t.TempDir(), "output")
			opts.Variants = map[string]string{"v1": input}
			opts.VariantCompression = map[string]VariantCompressionOptions{
				"v1": {Compression: codec.AlgorithmZstd}, alias: {Compression: codec.AlgorithmNone},
			}
			require.NoError(t, os.WriteFile(opts.OutputPath, []byte("keep"), 0o600))
			_, err := Pack(opts)
			require.ErrorIs(t, err, ErrDuplicateVariant)
			data, err := os.ReadFile(opts.OutputPath)
			require.NoError(t, err)
			assert.Equal(t, "keep", string(data))
			assert.Len(t, opts.VariantCompression, 2, "caller-owned map remains unchanged")
		})
	}
}

func TestPackCompressionLevelPrecedence(t *testing.T) {
	t.Parallel()
	input, payload := compressionContractInput(t)
	cases := []struct {
		name, rootAlgo, rootLevel, wantAlgo, wantLevel string
		override                                       VariantCompressionOptions
	}{
		{"inline beats root level", codec.AlgorithmZstd, compressionBest, codec.AlgorithmZstd, testCompressionFastest,
			VariantCompressionOptions{Compression: "zstd:fastest"}},
		{"separate beats inline", codec.AlgorithmZstd, compressionBest, codec.AlgorithmZstd, "default",
			VariantCompressionOptions{Compression: "zstd:fastest", Level: "default"}},
		{"separate beats root", codec.AlgorithmZstd, compressionBest, codec.AlgorithmZstd, testCompressionFastest,
			VariantCompressionOptions{Level: testCompressionFastest}},
		{"algorithm retains root level", codec.AlgorithmZstd, compressionBest, codec.AlgorithmZstd, compressionBest,
			VariantCompressionOptions{Compression: codec.AlgorithmZstd}},
		{"root inline retained", "zstd:best", "", codec.AlgorithmZstd, compressionBest, VariantCompressionOptions{}},
		{"root separate beats root inline", "zstd:best", testCompressionFastest, codec.AlgorithmZstd, testCompressionFastest,
			VariantCompressionOptions{}},
		{"codec transition inline", codec.AlgorithmZstd, compressionBest, codec.AlgorithmLZ4, testCompressionFastest,
			VariantCompressionOptions{Compression: "lz4:fastest"}},
		{"codec transition separate", codec.AlgorithmZstd, compressionBest, codec.AlgorithmLZ4, testCompressionFastest,
			VariantCompressionOptions{Compression: codec.AlgorithmLZ4, Level: testCompressionFastest}},
		{"none transition", codec.AlgorithmZstd, compressionBest, codec.AlgorithmNone, compressionBest,
			VariantCompressionOptions{Compression: codec.AlgorithmNone}},
	}
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("format%d/%s", version, tc.name), func(t *testing.T) {
				t.Parallel()
				opts := DefaultOptions()
				opts.StubPath, opts.OutputPath = input, filepath.Join(t.TempDir(), "output")
				opts.FormatVersion, opts.Compression, opts.CompressionLevel = version, tc.rootAlgo, tc.rootLevel
				opts.Variants = map[string]string{"v1": input}
				opts.VariantCompression = map[string]VariantCompressionOptions{"v1": tc.override}
				assertPackedCompression(t, opts, payload, tc.wantAlgo, tc.wantLevel)
			})
		}
	}
}
