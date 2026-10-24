package pack

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func codecPolicyInput(t *testing.T, size int) (string, []byte) {
	t.Helper()
	path, header := compressionContractInput(t)
	require.GreaterOrEqual(t, size, len(header))
	payload := make([]byte, size)
	copy(payload, bytes.Repeat([]byte("codec-policy-payload\n"), size/len("codec-policy-payload\n")+1))
	copy(payload, header)
	require.NoError(t, os.WriteFile(path, payload, 0o755))
	return path, payload
}

func TestPackLatencyPolicyBoundary(t *testing.T) {
	t.Parallel()
	const threshold = codec.DefaultLatencyUncompressedThreshold
	cases := []struct {
		name, global, variant, want string
		size                        int
	}{
		{name: "below", size: threshold - 1, want: codec.AlgorithmNone},
		{name: "at", size: threshold, want: codec.AlgorithmLZ4},
		{name: "above", size: threshold + 1, want: codec.AlgorithmLZ4},
		{name: "global lz4 below", size: threshold - 1, global: codec.AlgorithmLZ4, want: codec.AlgorithmLZ4},
		{name: "global none above", size: threshold + 1, global: codec.AlgorithmNone, want: codec.AlgorithmNone},
		{name: "variant lz4 below", size: threshold - 1, global: codec.AlgorithmNone,
			variant: codec.AlgorithmLZ4, want: codec.AlgorithmLZ4},
		{name: "variant none above", size: threshold + 1, global: codec.AlgorithmLZ4,
			variant: codec.AlgorithmNone, want: codec.AlgorithmNone},
	}
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("format%d/%s", version, tc.name), func(t *testing.T) {
				t.Parallel()
				input, payload := codecPolicyInput(t, tc.size)
				opts := DefaultOptions()
				opts.StubPath, opts.OutputPath = input, filepath.Join(t.TempDir(), "output")
				opts.FormatVersion, opts.Profile, opts.Compression = version, codec.ProfileLatency, tc.global
				opts.Variants = map[string]string{"v1": input}
				if tc.variant != "" {
					opts.VariantCompression = map[string]VariantCompressionOptions{"v1": {Compression: tc.variant}}
				}
				assertPackedCompression(t, opts, payload, tc.want, "")
			})
		}
	}
}

func TestPackSizePolicyMatchedDictionaryControls(t *testing.T) {
	t.Parallel()
	const (
		dictSize = 8 * 1024
		bestSpec = codec.AlgorithmZstd + ":" + compressionBest
	)
	input, payload := dictionaryContractInput(t)
	variants := map[string]string{"v1": input, "v3": input}
	samples, err := sampleVariantPayloads(variants, []string{"v1", "v3"})
	require.NoError(t, err)
	// The trainer receives the raw setting, even though size resolves payload encoding to best.
	expectedDictionary, err := codec.TrainDictionary(samples, dictSize, "")
	require.NoError(t, err)
	effectiveLevelDictionary, err := codec.TrainDictionary(samples, dictSize, compressionBest)
	require.NoError(t, err)
	require.False(t, bytes.Equal(expectedDictionary, effectiveLevelDictionary), "fixture must distinguish raw and resolved training levels")
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		t.Run(fmt.Sprintf("format%d", version), func(t *testing.T) {
			t.Parallel()
			epoch := reproducibleEpoch
			opts := DefaultOptions()
			opts.StubPath, opts.Variants, opts.CreatedUnix = input, variants, &epoch
			opts.FormatVersion, opts.DictSize = version, dictSize
			opts.Profile, opts.OutputPath = codec.ProfileSize, filepath.Join(t.TempDir(), "automatic")
			automatic := packReproducibleArtifact(t, opts, epoch, true)
			autoIndex, err := format.ReadTrailerAndIndex(bytes.NewReader(automatic), int64(len(automatic)))
			require.NoError(t, err)
			dictionary := automatic[autoIndex.DictionaryOffset : autoIndex.DictionaryOffset+autoIndex.DictionarySize]
			assert.Equal(t, expectedDictionary, dictionary)
			assert.Empty(t, opts.CompressionLevel, "effective payload level must not overwrite the raw trainer setting")

			opts.Profile, opts.Compression, opts.EnableDict = codec.ProfileBalanced, bestSpec, true
			opts.OutputPath = filepath.Join(t.TempDir(), "forced")
			forced := packReproducibleArtifact(t, opts, epoch, true)
			assert.True(t, bytes.Equal(automatic, forced), "size auto and matched forced-dictionary complete artifacts differ")

			opts.EnableDict, opts.OutputPath = false, filepath.Join(t.TempDir(), "no-dictionary")
			withoutDictionary := packReproducibleArtifact(t, opts, epoch, false)
			idx, err := format.ReadTrailerAndIndex(bytes.NewReader(withoutDictionary), int64(len(withoutDictionary)))
			require.NoError(t, err)
			assert.Equal(t, int64(len(payload)), idx.Variants[0].Offset, "no hidden dictionary bytes precede frames")
			c, err := codec.Get(codec.AlgorithmZstd)
			require.NoError(t, err)
			var expectedFrame bytes.Buffer
			require.NoError(t, c.Compress(&expectedFrame, payload, compressionBest))
			for _, entry := range idx.Variants {
				assert.Equal(t, codec.AlgorithmZstd, entry.Compression)
				assert.Equal(t, expectedFrame.Bytes(), withoutDictionary[entry.Offset:entry.Offset+entry.CompressedSize])
			}
		})
	}
}

func TestPackUnavailableCodecPreservesOutput(t *testing.T) {
	t.Parallel()
	const (
		unavailable = "unavailable-policy-codec"
		outputMode  = 0o600
	)
	input, _ := compressionContractInput(t)
	cases := []struct {
		name, profile, global string
		overrides             map[string]VariantCompressionOptions
	}{
		{name: "global", profile: codec.ProfileBalanced, global: unavailable},
		{name: "later variant", profile: codec.ProfileBalanced, global: codec.AlgorithmNone,
			overrides: map[string]VariantCompressionOptions{"v3": {Compression: unavailable}}},
		{name: "automatic dictionary selection", profile: codec.ProfileSize,
			overrides: map[string]VariantCompressionOptions{"v1": {Compression: unavailable}}},
	}
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("format%d/%s", version, tc.name), func(t *testing.T) {
				t.Parallel()
				opts := DefaultOptions()
				opts.StubPath, opts.OutputPath = input, filepath.Join(t.TempDir(), "output")
				opts.FormatVersion, opts.Profile, opts.Compression = version, tc.profile, tc.global
				opts.Variants = map[string]string{"v1": input, "v3": input}
				opts.VariantCompression = tc.overrides
				original := []byte("preserve previously published complete artifact")
				require.NoError(t, os.WriteFile(opts.OutputPath, original, outputMode))
				before, err := os.Stat(opts.OutputPath)
				require.NoError(t, err)
				_, err = Pack(opts)
				require.ErrorIs(t, err, codec.ErrUnsupportedCodec)
				after, err := os.Stat(opts.OutputPath)
				require.NoError(t, err)
				assert.True(t, os.SameFile(before, after), "failure replaced the published inode")
				assert.Equal(t, before.Mode(), after.Mode())
				assert.Equal(t, before.ModTime(), after.ModTime())
				data, err := os.ReadFile(opts.OutputPath)
				require.NoError(t, err)
				assert.Equal(t, original, data)
				entries, err := os.ReadDir(filepath.Dir(opts.OutputPath))
				require.NoError(t, err)
				require.Len(t, entries, 1, "failed packing left temporary output behind")
				assert.Equal(t, filepath.Base(opts.OutputPath), entries[0].Name())
			})
		}
	}
}
