package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	precedenceBest        = "best"
	precedenceFastest     = "fastest"
	precedenceCompression = "--compression"
	precedenceZstdBest    = "zstd:best"
	precedenceLZ4         = "v1=lz4"
	precedenceDirect      = "direct"
	precedenceManifest    = "manifest"
	precedencePGO         = "pgo"
)

func TestVariantCompressionCLILevelPrecedence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	stub := filepath.Join(dir, "stub")
	require.NoError(t, os.WriteFile(stub, []byte("stub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/precedence\ngo "+microfat.GoVersion()+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\nfunc main() { println(\"compression precedence\") }\n"), 0o644))
	payload := filepath.Join(dir, "payload")
	build := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-o", payload, ".")
	build.Dir = dir
	build.Env = buildChildEnv(map[string]string{
		"GOOS": "linux", "GOARCH": "amd64", "GOAMD64": "v1", "CGO_ENABLED": "0",
		"GOTOOLCHAIN": "local", "GOFLAGS": "", "GOWORK": "off",
	})
	buildOutput, err := build.CombinedOutput()
	require.NoError(t, err, "building baseline payload: %s", buildOutput)

	cases := []struct {
		name, rootAlgorithm, rootLevel, wantLevel string
		flags                                     []string
		manifestOnly                              bool
	}{
		{
			name: "CLI inline global level survives codec-only override", wantLevel: precedenceBest,
			flags: []string{precedenceCompression, precedenceZstdBest, flagVariantCompression, precedenceLZ4},
		},
		{
			name: "CLI separate global best matches inline best", wantLevel: precedenceBest,
			flags: []string{precedenceCompression, "zstd", "--compression-level", precedenceBest, flagVariantCompression, precedenceLZ4},
		},
		{
			name: "CLI separate global level outranks inline global level", wantLevel: precedenceFastest,
			flags: []string{precedenceCompression, precedenceZstdBest, "--compression-level", precedenceFastest,
				flagVariantCompression, precedenceLZ4},
		},
		{
			name: "variant inline level outranks both global levels", wantLevel: precedenceFastest,
			flags: []string{precedenceCompression, precedenceZstdBest, "--compression-level", precedenceBest,
				flagVariantCompression, "v1=lz4:fastest"},
		},
		{
			name: "root inline level remains attached to replaced algorithm", rootAlgorithm: precedenceZstdBest,
			wantLevel: precedenceFastest, manifestOnly: true,
			flags: []string{flagVariantCompression, precedenceLZ4},
		},
		{
			name: "root separate level survives codec-only override", rootAlgorithm: "zstd", rootLevel: precedenceBest,
			wantLevel: precedenceBest, manifestOnly: true,
			flags: []string{flagVariantCompression, precedenceLZ4},
		},
	}
	for _, route := range []string{precedenceDirect, precedenceManifest, precedencePGO} {
		t.Run(route, func(t *testing.T) {
			for _, tc := range cases {
				if route == precedenceDirect && tc.manifestOnly {
					continue
				}
				t.Run(tc.name, func(t *testing.T) {
					output := filepath.Join(t.TempDir(), "app.fat")
					args := []string{flagOutput, output, flagSkipELF}
					if route == precedenceDirect {
						args = append(args, flagStub, stub, "-v", "v1="+payload)
					} else {
						manifest := filepath.Join(dir, "manifest.yaml")
						content := fmt.Sprintf(`package: .
stub: %q
target_arch: amd64
compression:
  algorithm: %q
  level: %q
variants:
  - level: v1
    pgo: "off"
`, stub, tc.rootAlgorithm, tc.rootLevel)
						require.NoError(t, os.WriteFile(manifest, []byte(content), 0o644))
						args = append(args, flagManifest, manifest)
					}
					cmd := newPackCmd()
					if route == precedencePGO {
						cmd = newPgoPackCmd()
					}
					cmd.SetOut(&bytes.Buffer{})
					cmd.SetErr(&bytes.Buffer{})
					cmd.SetArgs(append(args, tc.flags...))
					require.NoError(t, cmd.Execute())
					assertVariantCompressionLZ4Level(t, output, tc.wantLevel)
				})
			}
		})
	}
}

func assertVariantCompressionLZ4Level(t *testing.T, path, wantLevel string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	idx, results, err := pack.VerifyBinary(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.True(t, results[0].Valid)
	require.NoError(t, results[0].Error)
	require.Len(t, idx.Variants, 1)
	entry := idx.Variants[0]
	require.Equal(t, codec.AlgorithmLZ4, entry.Compression)
	frame := data[entry.Offset : entry.Offset+entry.CompressedSize]
	payload := decodeMixedCodecFrame(t, entry.Compression, frame, nil, 0)
	frames := make(map[string][]byte)
	for _, level := range []string{precedenceBest, precedenceFastest} {
		var control bytes.Buffer
		require.NoError(t, codec.NewLZ4Codec().Compress(&control, payload, level))
		frames[level] = control.Bytes()
	}
	require.False(t, bytes.Equal(frames[precedenceBest], frames[precedenceFastest]),
		"fixture must distinguish LZ4 best and fastest compression")
	assert.True(t, bytes.Equal(frames[wantLevel], frame), "packed LZ4 frame must use %s compression", wantLevel)
}
