package pack

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reproducibleEpoch int64 = 1724540000

type reproducibleCompression struct {
	name, algorithm, level string
	overrides              map[string]VariantCompressionOptions
}

func reproduciblePackInputs(t *testing.T) (string, map[string]string) {
	t.Helper()
	stub, data := dictionaryContractInput(t)
	variants := make(map[string]string)
	dir := t.TempDir()
	for _, level := range []string{"v1", "v2", "v3"} {
		path := filepath.Join(dir, level)
		payload := append(bytes.Clone(data), []byte("variant-specific-symbol-"+level)...)
		require.NoError(t, os.WriteFile(path, payload, 0o755))
		variants[level] = path
	}
	return stub, variants
}

func reproducibleCompressionCases() []reproducibleCompression {
	cases := []reproducibleCompression{{name: codec.AlgorithmNone, algorithm: codec.AlgorithmNone}}
	const lz4LevelCount = 10
	for level := range lz4LevelCount {
		cases = append(cases, reproducibleCompression{
			name: codec.AlgorithmLZ4 + "/" + strconv.Itoa(level), algorithm: codec.AlgorithmLZ4, level: strconv.Itoa(level),
		})
	}
	for _, level := range []string{testCompressionFastest, "default", "better", compressionBest} {
		cases = append(cases, reproducibleCompression{
			name: codec.AlgorithmZstd + "/" + level, algorithm: codec.AlgorithmZstd, level: level,
		})
	}
	return append(cases, reproducibleCompression{
		name: "mixed", algorithm: codec.AlgorithmZstd,
		overrides: map[string]VariantCompressionOptions{
			"v1": {Compression: codec.AlgorithmNone},
			"v2": {Compression: codec.AlgorithmLZ4, Level: "better"},
			"v3": {Compression: codec.AlgorithmZstd, Level: compressionBest},
		},
	})
}

func packReproducibleArtifact(t *testing.T, opts Options, epoch int64, dictionary bool) []byte {
	t.Helper()
	idx, err := Pack(opts)
	require.NoError(t, err)
	assert.Equal(t, epoch, idx.CreatedUnix)
	data, err := os.ReadFile(opts.OutputPath)
	require.NoError(t, err)
	parsed, results, err := VerifyBinary(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	assert.Equal(t, epoch, parsed.CreatedUnix)
	require.Len(t, results, len(opts.Variants))
	for _, result := range results {
		assert.True(t, result.Valid)
		assert.NoError(t, result.Error)
	}
	if dictionary {
		assert.Positive(t, parsed.DictionarySize)
	} else {
		assert.Zero(t, parsed.DictionarySize)
	}
	return data
}

func TestPackCreatedUnixReproducibleArtifacts(t *testing.T) {
	t.Parallel()
	stub, inputs := reproduciblePackInputs(t)
	levels := []string{"v1", "v2", "v3"}
	const dictSize = 8 * 1024
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, compression := range reproducibleCompressionCases() {
			for _, dictionary := range []bool{false, true} {
				t.Run(fmt.Sprintf("format%d/%s/dictionary=%t", version, compression.name, dictionary), func(t *testing.T) {
					epoch := reproducibleEpoch
					opts := DefaultOptions()
					opts.StubPath, opts.AppName, opts.CreatedUnix = stub, "reproducible-app", &epoch
					opts.FormatVersion, opts.EnableDict, opts.DictSize = version, dictionary, dictSize
					opts.Compression, opts.CompressionLevel = compression.algorithm, compression.level
					var artifacts [2][]byte
					for attempt := range artifacts {
						opts.OutputPath = filepath.Join(t.TempDir(), "output")
						opts.Variants = make(map[string]string)
						opts.VariantCompression = make(map[string]VariantCompressionOptions)
						for position := range levels {
							if attempt > 0 {
								position = len(levels) - position - 1
							}
							level := levels[position]
							opts.Variants[level] = inputs[level]
							if override, ok := compression.overrides[level]; ok {
								opts.VariantCompression[level] = override
							}
						}
						artifacts[attempt] = packReproducibleArtifact(t, opts, epoch, dictionary)
					}
					require.Equal(t, sha256.Sum256(artifacts[0]), sha256.Sum256(artifacts[1]), "complete artifact SHA-256")
					assert.True(t, bytes.Equal(artifacts[0], artifacts[1]), "complete artifact bytes differ")
				})
			}
		}
	}
}

func TestPackCreatedUnixExactEpoch(t *testing.T) {
	t.Parallel()
	input, _ := compressionContractInput(t)
	epochs := []struct {
		name  string
		epoch int64
	}{
		{"negative", -123456789}, {"zero", 0}, {"positive", reproducibleEpoch},
		{"minimum", math.MinInt64}, {"maximum", math.MaxInt64},
	}
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, tc := range epochs {
			t.Run(fmt.Sprintf("format%d/%s", version, tc.name), func(t *testing.T) {
				t.Parallel()
				opts := DefaultOptions()
				opts.StubPath, opts.OutputPath = input, filepath.Join(t.TempDir(), "output")
				opts.FormatVersion, opts.Compression, opts.CreatedUnix = version, codec.AlgorithmNone, &tc.epoch
				opts.Variants = map[string]string{"v1": input}
				data := packReproducibleArtifact(t, opts, tc.epoch, false)
				idx, err := format.ReadTrailerAndIndex(bytes.NewReader(data), int64(len(data)))
				require.NoError(t, err)
				jsonData, err := json.Marshal(idx)
				require.NoError(t, err)
				var decoded format.Index
				require.NoError(t, json.Unmarshal(jsonData, &decoded))
				assert.Equal(t, tc.epoch, decoded.CreatedUnix, "JSON retains exact signed seconds")
			})
		}
	}
}

func TestPackCreatedUnixChangedEpochChangesArtifact(t *testing.T) {
	t.Parallel()
	input, _ := compressionContractInput(t)
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		t.Run(fmt.Sprintf("format%d", version), func(t *testing.T) {
			t.Parallel()
			epoch := reproducibleEpoch
			opts := DefaultOptions()
			opts.StubPath, opts.OutputPath = input, filepath.Join(t.TempDir(), "output")
			opts.FormatVersion, opts.CreatedUnix = version, &epoch
			opts.Variants = map[string]string{"v1": input}
			first := packReproducibleArtifact(t, opts, epoch, false)
			epoch++
			second := packReproducibleArtifact(t, opts, epoch, false)
			assert.NotEqual(t, sha256.Sum256(first), sha256.Sum256(second))
			assert.False(t, bytes.Equal(first, second))
		})
	}
}

func TestPackCreatedUnixResolvedBeforeInputCallbacks(t *testing.T) {
	t.Parallel()
	input, _ := compressionContractInput(t)
	epoch := reproducibleEpoch
	opts := DefaultOptions()
	opts.StubPath, opts.OutputPath, opts.CreatedUnix = input, filepath.Join(t.TempDir(), "output"), &epoch
	opts.Variants = map[string]string{"v1": input, "v3": input}
	opts.VariantValidator = func(string, string) error {
		epoch++
		return nil
	}
	packReproducibleArtifact(t, opts, reproducibleEpoch, false)
	assert.Greater(t, epoch, reproducibleEpoch, "the callback changed the caller-owned pointer after resolution")
}

func TestPackCreatedUnixIgnoresEnvironment(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "not-an-epoch")
	input, _ := compressionContractInput(t)
	opts := DefaultOptions()
	opts.StubPath, opts.OutputPath = input, filepath.Join(t.TempDir(), "output")
	opts.Compression = codec.AlgorithmNone
	opts.Variants = map[string]string{"v1": input}
	start := time.Now().Unix()
	idx, err := Pack(opts)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, idx.CreatedUnix, start)
	assert.LessOrEqual(t, idx.CreatedUnix, time.Now().Unix())
	epoch := reproducibleEpoch
	opts.CreatedUnix = &epoch
	packReproducibleArtifact(t, opts, epoch, false)
}
