package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mixedCodecEnvironment() []string {
	env := make([]string, 0, len(os.Environ()))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "MICROFAT_") {
			continue
		}
		switch key {
		case "GOOS", "GOARCH", "GOAMD64", "GOARM64", "GOFLAGS", "GOTOOLCHAIN", "CGO_ENABLED", sourceDateEpochEnv:
			continue
		}
		env = append(env, item)
	}
	return append(env, "GOOS=linux", "GOARCH="+runtime.GOARCH,
		"GOAMD64=v1", "GOARM64=v8.0", "CGO_ENABLED=0", "GOTOOLCHAIN=local")
}

func mixedCodecBuild(t *testing.T, path, pkg, tags string) {
	t.Helper()
	args := []string{"build", "-buildvcs=false", "-trimpath", "-ldflags=-s -w"}
	if tags != "" {
		args = append(args, "-tags="+tags)
	}
	cmd := exec.Command("go", append(args, "-o", path, pkg)...)
	cmd.Env = mixedCodecEnvironment()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "building %s: %s", path, out)
}

func TestVariantCompressionMixedCodecExecutionMatrix(t *testing.T) {
	if runtime.GOOS != testOSLinux || (runtime.GOARCH != testArchAMD64 && runtime.GOARCH != testArchARM64) {
		t.Skip("mixed-codec launcher execution requires Linux amd64 or arm64")
	}
	dir := t.TempDir()
	cli := filepath.Join(dir, testBinaryMicrofat)
	mixedCodecBuild(t, cli, ".", "")
	mixedCodecBuild(t, filepath.Join(dir, releaseFullStub), "../microfat-stub", "")
	mixedCodecBuild(t, filepath.Join(dir, releaseMinStub), "../microfat-stub", "minimal")

	source := filepath.Join(dir, "payload.go")
	require.NoError(t, os.WriteFile(source, []byte(`package main
import (
	"fmt"
	"os"
)
func main() {
	fmt.Printf("mixed codec payload|%s|%s|%s\n", os.Getenv("MICROFAT_SELECTED_VARIANT"),
		os.Getenv("MICROFAT_EXEC_MODE"), os.Getenv("MICROFAT_SELECTED_SHA256"))
}
`), 0o600))
	payloadPath := filepath.Join(dir, "payload")
	mixedCodecBuild(t, payloadPath, source, "")
	payload, err := os.ReadFile(payloadPath)
	require.NoError(t, err)

	// Direct packaging can reuse a baseline ELF at higher declared tiers. The
	// launcher still enforces hardware compatibility, so only supported tiers run.
	levels := []string{microarch.AMD64v1, microarch.AMD64v2, microarch.AMD64v3}
	if runtime.GOARCH == testArchARM64 {
		levels = []string{microarch.ARM64v8_0, microarch.ARM64v8_1, microarch.ARM64v8_2}
	}
	algorithms := []string{codec.AlgorithmNone, codec.AlgorithmLZ4, codec.AlgorithmZstd}
	for _, version := range []int{format.FormatVersion1, format.FormatVersion2} {
		for _, profile := range []string{"full", "minimal"} {
			for _, dictionary := range []bool{false, true} {
				t.Run(fmt.Sprintf("format%d/%s/dictionary=%t", version, profile, dictionary), func(t *testing.T) {
					output := filepath.Join(t.TempDir(), "mixed-fat")
					args := []string{"pack", flagArch, runtime.GOARCH, flagStubProfile, profile,
						flagFormatVersion, strconv.Itoa(version), flagOutput, output, flagProfile, codec.ProfileBalanced,
						"--compression", "zstd:best"}
					for i, level := range levels {
						spec := algorithms[i]
						if spec != codec.AlgorithmNone {
							spec += ":fastest"
						}
						args = append(args, "-v", level+"="+payloadPath,
							"--variant-compression", runtime.GOARCH+"_"+level+"="+spec)
					}
					if dictionary {
						args = append(args, "--dict", "--dict-size", "4096")
					}
					cmd := exec.Command(cli, args...)
					cmd.Env = mixedCodecEnvironment()
					out, packErr := cmd.CombinedOutput()
					require.NoError(t, packErr, "packing mixed codecs: %s", out)
					idx := assertMixedCodecFrames(t, output, payload, version, dictionary, levels, algorithms)
					for _, level := range levels {
						for _, mode := range []string{format.ExecModeMemfd, format.ExecModeCache} {
							t.Run(level+"/"+mode, func(t *testing.T) {
								if !microarch.IsSupported(level) {
									t.Skipf("host does not support %s; embedded frame was decoded and checked", level)
								}
								entry, ok := idx.FindVariant(level)
								require.True(t, ok)
								assertMixedCodecExecution(t, output, mode, *entry)
							})
						}
					}
				})
			}
		}
	}
}

func assertMixedCodecFrames(
	t *testing.T, path string, payload []byte, version int, dictionary bool, levels, algorithms []string,
) *format.Index {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	idx, err := format.ReadTrailerAndIndex(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	assert.Equal(t, version, idx.Version)
	require.Len(t, idx.Variants, len(levels))
	dict := data[idx.DictionaryOffset : idx.DictionaryOffset+idx.DictionarySize]
	if dictionary {
		require.NotEmpty(t, dict)
		digest := sha256.Sum256(dict)
		assert.Equal(t, hex.EncodeToString(digest[:]), idx.DictionarySHA256)
		require.NotZero(t, idx.DictionaryID)
	} else {
		require.Empty(t, dict)
		assert.Zero(t, idx.DictionaryOffset)
		assert.Empty(t, idx.DictionarySHA256)
		assert.Zero(t, idx.DictionaryID)
	}
	digest := sha256.Sum256(payload)
	for i, level := range levels {
		entry, ok := idx.FindVariant(level)
		require.True(t, ok)
		require.Equal(t, algorithms[i], entry.Compression)
		assert.Equal(t, int64(len(payload)), entry.UncompressedSize)
		assert.Equal(t, hex.EncodeToString(digest[:]), entry.SHA256)
		frame := data[entry.Offset : entry.Offset+entry.CompressedSize]
		decoded := decodeMixedCodecFrame(t, entry.Compression, frame, dict, idx.DictionaryID)
		assert.Equal(t, payload, decoded, "decoded %s bytes must match the original ELF", level)
		decodedDigest := sha256.Sum256(decoded)
		assert.Equal(t, entry.SHA256, hex.EncodeToString(decodedDigest[:]))
	}
	return idx
}

// Decode with the underlying wire-format libraries rather than microfat's codec
// registry or VerifyBinary, so incorrect metadata or a mislabeled frame fails.
func decodeMixedCodecFrame(t *testing.T, algorithm string, frame, dict []byte, dictionaryID uint32) []byte {
	t.Helper()
	switch algorithm {
	case codec.AlgorithmNone:
		return frame
	case codec.AlgorithmLZ4:
		data, err := io.ReadAll(lz4.NewReader(bytes.NewReader(frame)))
		require.NoError(t, err)
		return data
	case codec.AlgorithmZstd:
		var header zstd.Header
		require.NoError(t, header.Decode(frame))
		assert.Equal(t, dictionaryID, header.DictionaryID)
		opts := []zstd.DOption{zstd.WithDecoderConcurrency(1)}
		if len(dict) > 0 {
			opts = append(opts, zstd.WithDecoderDicts(dict))
		}
		decoder, err := zstd.NewReader(nil, opts...)
		require.NoError(t, err)
		defer decoder.Close()
		data, err := decoder.DecodeAll(frame, nil)
		require.NoError(t, err)
		return data
	default:
		t.Fatalf("unexpected codec %q", algorithm)
		return nil
	}
}

func assertMixedCodecExecution(t *testing.T, path, mode string, entry format.VariantEntry) {
	t.Helper()
	cmd := exec.Command(path)
	cmd.Env = append(mixedCodecEnvironment(), format.EnvForceLevel+"="+entry.Level,
		format.EnvExecMode+"="+mode, format.EnvCacheDir+"="+filepath.Join(t.TempDir(), "cache"), format.EnvLog+"=json")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	require.NoError(t, err, "executing %s via %s: %s", entry.Level, mode, stderr.String())
	assert.Equal(t, fmt.Sprintf("mixed codec payload|%s|%s|%s\n", entry.Level, mode, entry.SHA256), string(stdout))
	var telemetry format.DispatchTelemetry
	log, ok := strings.CutPrefix(strings.TrimSpace(stderr.String()), "[microfat] ")
	require.True(t, ok, stderr.String())
	require.NoError(t, json.Unmarshal([]byte(log), &telemetry), stderr.String())
	assert.Equal(t, "dispatch", telemetry.Event)
	assert.Equal(t, entry.Level, telemetry.SelectedVariant)
	assert.Equal(t, entry.SHA256, telemetry.SelectedSHA256)
	assert.Equal(t, entry.UncompressedSize, telemetry.SelectedSizeBytes)
	assert.Equal(t, mode, telemetry.ExecMode)
}
