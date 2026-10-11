package builder_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/builder"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const inlineCompressionFastest = "fastest"

func TestManifestInlineCompressionLevelPrecedence(t *testing.T) {
	t.Parallel()
	input := createDummyELF(t, t.TempDir(), "payload", testArchAMD64)
	payload, err := os.ReadFile(input)
	require.NoError(t, err)
	cases := []struct {
		name, cliAlgorithm, cliLevel, variantAlgorithm, variantLevel, wantAlgorithm, wantLevel string
	}{
		{"variant inline beats root", "", "", "zstd:fastest", "", codec.AlgorithmZstd, inlineCompressionFastest},
		{"variant inline beats CLI level", codec.AlgorithmZstd, "best", "zstd:fastest", "", codec.AlgorithmZstd, inlineCompressionFastest},
		{"CLI inline beats root", "zstd:fastest", "", "", "", codec.AlgorithmZstd, inlineCompressionFastest},
		{"CLI separate beats own inline", "zstd:best", inlineCompressionFastest, "", "", codec.AlgorithmZstd, inlineCompressionFastest},
		{"variant separate beats inline", codec.AlgorithmZstd, "best", "zstd:best", inlineCompressionFastest,
			codec.AlgorithmZstd, inlineCompressionFastest},
		{"variant transition beats CLI", "zstd:best", "", "lz4:fastest", "", codec.AlgorithmLZ4, inlineCompressionFastest},
	}
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("format%d/%s", version, tc.name), func(t *testing.T) {
				t.Parallel()
				yaml := fmt.Sprintf(`name: level-precedence
package: example
target_arch: amd64
compression:
  algorithm: zstd
  level: best
variants:
  - level: v1
    compression:
      algorithm: %q
      level: %q
`, tc.variantAlgorithm, tc.variantLevel)
				m, parseErr := builder.ParseManifest([]byte(yaml), ".yaml")
				require.NoError(t, parseErr)
				opts := builder.AssemblePackOptionsForTest(m, input, filepath.Join(t.TempDir(), "fat"),
					m.AppName, map[string]string{"v1": input}, builder.BuildOptions{
						Compression: tc.cliAlgorithm, CompressionLevel: tc.cliLevel,
						FormatVersion: version, SkipELFValidation: true,
					})
				opts.VariantValidator = nil
				idx, packErr := pack.Pack(opts)
				require.NoError(t, packErr)
				require.Len(t, idx.Variants, 1)
				assert.Equal(t, tc.wantAlgorithm, idx.Variants[0].Compression)
				data, readErr := os.ReadFile(opts.OutputPath)
				require.NoError(t, readErr)
				_, results, verifyErr := pack.VerifyBinary(bytes.NewReader(data), int64(len(data)))
				require.NoError(t, verifyErr)
				require.Len(t, results, 1)
				assert.True(t, results[0].Valid)
				c, getErr := codec.Get(tc.wantAlgorithm)
				require.NoError(t, getErr)
				var control bytes.Buffer
				require.NoError(t, c.Compress(&control, payload, tc.wantLevel))
				entry := idx.Variants[0]
				assert.Equal(t, control.Bytes(), data[entry.Offset:entry.Offset+entry.CompressedSize])
			})
		}
	}
}
