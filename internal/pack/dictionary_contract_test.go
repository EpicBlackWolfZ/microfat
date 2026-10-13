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

func dictionaryContractInput(t *testing.T) (string, []byte) {
	t.Helper()
	const (
		patternCount      = 1200
		patternMultiplier = 43
		patternMask       = 0xA5A5A5A5
	)
	path := createDummyELF(t, t.TempDir(), "input", uint16(elf.EM_X86_64), byte(elf.ELFCLASS64))
	payload, err := os.ReadFile(path)
	require.NoError(t, err)
	var data bytes.Buffer
	_, err = data.Write(payload)
	require.NoError(t, err)
	for i := range patternCount {
		_, err = fmt.Fprintf(&data, "runtime_metadata_symbol_entry_%04d_hash_%x\n", i, (i*patternMultiplier)^patternMask)
		require.NoError(t, err)
	}
	require.NoError(t, os.WriteFile(path, data.Bytes(), 0o755))
	return path, data.Bytes()
}

func TestPackAutomaticDictionaryEffectiveCodecs(t *testing.T) {
	t.Parallel()
	const dictSize = 8 * 1024
	input, payload := dictionaryContractInput(t)
	cases := []struct {
		name, rootAlgo, rootLevel string
		overrides                 map[string]VariantCompressionOptions
		algorithms, levels        []string
		wantDict, explicitDict    bool
	}{
		{
			name: "all none overrides",
			overrides: map[string]VariantCompressionOptions{
				"v1": {Compression: codec.AlgorithmNone}, "v3": {Compression: codec.AlgorithmNone},
			},
			algorithms: []string{codec.AlgorithmNone, codec.AlgorithmNone}, levels: []string{"", ""},
		},
		{
			name: "all lz4 overrides",
			overrides: map[string]VariantCompressionOptions{
				"v1": {Compression: codec.AlgorithmLZ4}, "v3": {Compression: codec.AlgorithmLZ4},
			},
			algorithms: []string{codec.AlgorithmLZ4, codec.AlgorithmLZ4}, levels: []string{"", ""},
		},
		{
			name: "mixed none and lz4 overrides",
			overrides: map[string]VariantCompressionOptions{
				"v1": {Compression: codec.AlgorithmNone}, "v3": {Compression: codec.AlgorithmLZ4},
			},
			algorithms: []string{codec.AlgorithmNone, codec.AlgorithmLZ4}, levels: []string{"", ""},
		},
		{
			name: "variant latency profiles replace root size",
			overrides: map[string]VariantCompressionOptions{
				"v1": {Profile: codec.ProfileLatency}, "v3": {Profile: codec.ProfileLatency},
			},
			algorithms: []string{codec.AlgorithmNone, codec.AlgorithmNone}, levels: []string{"", ""},
		},
		{
			name: "root inline zstd replaced by non-dictionary codecs", rootAlgo: "zstd:best",
			overrides: map[string]VariantCompressionOptions{
				"AMD64_V1": {Compression: codec.AlgorithmNone}, "V3": {Compression: "lz4:fastest"},
			},
			algorithms: []string{codec.AlgorithmNone, codec.AlgorithmLZ4}, levels: []string{"", testCompressionFastest},
		},
		{
			name: "one zstd consumer remains",
			overrides: map[string]VariantCompressionOptions{
				"v1": {Compression: codec.AlgorithmNone},
			},
			algorithms: []string{codec.AlgorithmNone, codec.AlgorithmZstd}, levels: []string{"", compressionBest}, wantDict: true,
		},
		{
			name: "root none overridden to zstd", rootAlgo: codec.AlgorithmNone,
			overrides: map[string]VariantCompressionOptions{
				"v3": {Compression: codec.AlgorithmZstd},
			},
			algorithms: []string{codec.AlgorithmNone, codec.AlgorithmZstd}, levels: []string{"", compressionBest}, wantDict: true,
		},
		{
			name: "root lz4 overridden to inline zstd", rootAlgo: codec.AlgorithmLZ4, rootLevel: compressionBest,
			overrides: map[string]VariantCompressionOptions{
				"v1": {Level: testCompressionFastest}, "v3": {Compression: "zstd:fastest"},
			},
			algorithms: []string{codec.AlgorithmLZ4, codec.AlgorithmZstd},
			levels:     []string{testCompressionFastest, testCompressionFastest}, wantDict: true,
		},
		{
			name: "separate variant level outranks inline and root", rootAlgo: codec.AlgorithmNone, rootLevel: compressionBest,
			overrides: map[string]VariantCompressionOptions{
				"v3": {Compression: "zstd:best", Level: testCompressionFastest},
			},
			algorithms: []string{codec.AlgorithmNone, codec.AlgorithmZstd},
			levels:     []string{compressionBest, testCompressionFastest}, wantDict: true,
		},
		{
			name: "explicit dictionary retained for none", rootAlgo: codec.AlgorithmNone,
			algorithms: []string{codec.AlgorithmNone, codec.AlgorithmNone}, levels: []string{"", ""},
			wantDict: true, explicitDict: true,
		},
		{
			name: "explicit dictionary retained for lz4", rootAlgo: codec.AlgorithmLZ4,
			algorithms: []string{codec.AlgorithmLZ4, codec.AlgorithmLZ4}, levels: []string{"", ""},
			wantDict: true, explicitDict: true,
		},
	}
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("format%d/%s", version, tc.name), func(t *testing.T) {
				t.Parallel()
				var warnings []string
				opts := DefaultOptions()
				opts.StubPath, opts.OutputPath = input, filepath.Join(t.TempDir(), "output")
				opts.Profile, opts.Compression, opts.CompressionLevel = codec.ProfileSize, tc.rootAlgo, tc.rootLevel
				opts.FormatVersion, opts.DictSize, opts.EnableDict = version, dictSize, tc.explicitDict
				opts.Variants = map[string]string{"v1": input, "v3": input}
				opts.VariantCompression = tc.overrides
				opts.WarnFunc = func(message string, args ...any) { warnings = append(warnings, fmt.Sprintf(message, args...)) }
				idx, err := Pack(opts)
				require.NoError(t, err)
				require.Empty(t, warnings)
				data, err := os.ReadFile(opts.OutputPath)
				require.NoError(t, err)
				parsed, results, err := VerifyBinary(bytes.NewReader(data), int64(len(data)))
				require.NoError(t, err)
				require.Len(t, results, len(tc.algorithms))
				if tc.wantDict {
					require.Positive(t, idx.DictionarySize)
					assert.Equal(t, idx.DictionarySize, parsed.DictionarySize)
					assert.Equal(t, idx.DictionarySHA256, parsed.DictionarySHA256)
				} else {
					assert.Zero(t, idx.DictionarySize)
					assert.Zero(t, idx.DictionaryOffset)
					assert.Empty(t, idx.DictionarySHA256)
					assert.Zero(t, idx.DictionaryID)
					assert.Zero(t, parsed.DictionarySize)
					assert.Equal(t, int64(len(payload)), idx.Variants[0].Offset, "no dictionary bytes precede the first payload")
				}
				dict := data[idx.DictionaryOffset : idx.DictionaryOffset+idx.DictionarySize]
				for i, result := range results {
					assert.True(t, result.Valid)
					assert.NoError(t, result.Error)
					assert.Equal(t, tc.algorithms[i], parsed.Variants[i].Compression)
					c, err := codec.Get(tc.algorithms[i])
					require.NoError(t, err)
					var control bytes.Buffer
					if dc, ok := c.(codec.DictCodec); ok && len(dict) > 0 {
						require.NoError(t, dc.CompressWithDict(&control, payload, tc.levels[i], dict))
					} else {
						require.NoError(t, c.Compress(&control, payload, tc.levels[i]))
					}
					entry := idx.Variants[i]
					assert.Equal(t, control.Bytes(), data[entry.Offset:entry.Offset+entry.CompressedSize])
				}
			})
		}
	}
}

func TestAutomaticDictionarySkipsTrainingWithoutConsumers(t *testing.T) {
	t.Parallel()
	const sampleSize = 3
	tempDir := t.TempDir()
	input := filepath.Join(tempDir, "input")
	require.NoError(t, os.WriteFile(input, make([]byte, sampleSize), 0o755))
	for _, algorithm := range []string{codec.AlgorithmNone, codec.AlgorithmLZ4} {
		t.Run(algorithm, func(t *testing.T) {
			t.Parallel()
			var warnings []string
			opts := DefaultOptions()
			opts.StubPath, opts.OutputPath, opts.SkipELFValidation = input, filepath.Join(t.TempDir(), "output"), true
			opts.Profile = codec.ProfileSize
			opts.Variants = map[string]string{"v1": input, "v3": input}
			opts.VariantCompression = map[string]VariantCompressionOptions{
				"v1": {Compression: algorithm}, "v3": {Compression: algorithm},
			}
			opts.WarnFunc = func(message string, args ...any) { warnings = append(warnings, fmt.Sprintf(message, args...)) }
			idx, err := Pack(opts)
			require.NoError(t, err)
			assert.Zero(t, idx.DictionarySize)
			assert.Empty(t, warnings, "the too-small sample would emit a warning if dictionary training were attempted")
		})
	}
}

func TestDictionarySelectionErrors(t *testing.T) {
	t.Parallel()
	input, _ := dictionaryContractInput(t)
	cases := []struct {
		name, path, compression string
		wantErr                 error
	}{
		{"missing variant", filepath.Join(t.TempDir(), "missing"), codec.AlgorithmZstd, os.ErrNotExist},
		{"invalid codec", input, "invalid", codec.ErrUnsupportedCodec},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := DefaultOptions()
			opts.Profile, opts.Compression = codec.ProfileSize, tc.compression
			opts.Variants = map[string]string{"v1": tc.path, "v3": tc.path}
			dict, checksum, err := prepareSharedDictionary(&opts, []string{"v1", "v3"})
			require.ErrorIs(t, err, tc.wantErr)
			assert.Empty(t, dict)
			assert.Empty(t, checksum)
		})
	}
}
