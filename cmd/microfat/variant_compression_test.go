package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const flagVariantCompression = "--variant-compression"

func TestParseVariantCompression(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, arch string
		levels     []string
		raw        []string
		want       map[string]pack.VariantCompressionOptions
	}{
		{"omitted", testArchAMD64, nil, nil, map[string]pack.VariantCompressionOptions{}},
		{"mixed and aliases", testArchAMD64, []string{"amd64_v1", "v2", "v3"},
			[]string{" V1 = NONE ", "amd64-v2=LZ4:fastest", "AMD64_V3=zstd:best"},
			map[string]pack.VariantCompressionOptions{
				"v1": {Compression: codec.AlgorithmNone}, "v2": {Compression: codec.AlgorithmLZ4, Level: "fastest"},
				"v3": {Compression: codec.AlgorithmZstd, Level: "best"},
			}},
		{"arm aliases", testArchARM64, []string{"v8.0", microarch.ARM64v8_2},
			[]string{"arm64-v8.0=lz4:9", "ARM64_V8.2=zstd:standard"},
			map[string]pack.VariantCompressionOptions{
				"v8.0":              {Compression: codec.AlgorithmLZ4, Level: "9"},
				microarch.ARM64v8_2: {Compression: codec.AlgorithmZstd, Level: "standard"},
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseVariantCompression(tc.raw, tc.arch, tc.levels)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestVariantCompressionLevelSpellings(t *testing.T) {
	t.Parallel()
	for algorithm, levels := range map[string][]string{
		codec.AlgorithmZstd: {"fastest", "fast", "speed", "default", "standard", "better", "best", "max", "-1", "0", "2", "11"},
		codec.AlgorithmLZ4:  {"fastest", "fast", "default", "better", "best", "max", "-1", "0", "9", "99"},
	} {
		for _, level := range levels {
			t.Run(algorithm+"/"+level, func(t *testing.T) {
				t.Parallel()
				got, err := parseVariantCompressionSpec(algorithm + ":" + level)
				require.NoError(t, err)
				assert.Equal(t, algorithm, got.Compression)
				assert.Equal(t, level, got.Level)
			})
		}
	}
}

func TestVariantCompressionRejectsBeforeBuildOrSnapshot(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, want string
		raw        []string
	}{
		{"empty", "expected level=codec", []string{""}},
		{"separator missing", "expected level=codec", []string{"v1"}},
		{"empty key", "expected level=codec", []string{" =none"}},
		{"empty spec", "expected level=codec", []string{"v1= "}},
		{"invalid tier", "architecture amd64", []string{"v99=none"}},
		{"wrong architecture", "architecture amd64", []string{"v8.0=none"}},
		{"unknown tier", "not a supplied variant", []string{"v4=none"}},
		{"duplicate", "duplicate --variant-compression", []string{"v1=none", "v1=lz4"}},
		{"alias collision", "normalizes to", []string{"amd64_v1=none", "V1=lz4"}},
		{"unknown codec", "unsupported compression", []string{"v1=brotli"}},
		{"empty codec", "expected codec", []string{"v1=:best"}},
		{"empty level", "expected codec", []string{"v1=zstd:"}},
		{"multiple colons", "expected codec", []string{"v1=zstd:best:1"}},
		{"multiple equals", "unsupported compression", []string{"v1=none=other"}},
		{"none level", "none does not accept", []string{"v1=none:best"}},
		{"zstd bad level", "invalid compression level", []string{"v1=zstd:unknown"}},
		{"lz4 bad level", "invalid compression level", []string{"v1=lz4:standard"}},
		{"overflow", "invalid compression level", []string{"v1=zstd:999999999999999999999999999999"}},
		{"comma list", "unsupported compression", []string{"v1=none,v2=lz4"}},
	}
	routes := []struct {
		name string
		new  func() *cobra.Command
	}{
		{"direct", newPackCmd}, {"manifest", newPackCmd}, {"pgo", newPgoPackCmd},
	}
	for _, route := range routes {
		for _, tc := range cases {
			t.Run(route.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				output := filepath.Join(dir, "missing", "output")
				manifest := filepath.Join(dir, "manifest.yaml")
				require.NoError(t, os.WriteFile(manifest, []byte("package: .\nvariants:\n  - level: v1\n"), 0o644))
				args := []string{flagOutput, output}
				if route.name == "direct" {
					args = append(args, flagStub, "missing-stub", "-v", "v1=missing-payload")
				} else {
					args = append(args, flagManifest, manifest, "--go-binary", "missing-compiler")
				}
				for _, raw := range tc.raw {
					args = append(args, flagVariantCompression, raw)
				}
				cmd := route.new()
				cmd.SetOut(&bytes.Buffer{})
				cmd.SetErr(&bytes.Buffer{})
				cmd.SetArgs(args)
				require.ErrorContains(t, cmd.Execute(), tc.want)
				assert.NoDirExists(t, filepath.Dir(output))
			})
		}
	}
}

func TestManifestVariantCompressionOutputs(t *testing.T) {
	t.Parallel()
	for _, route := range []string{"manifest", "pgo"} {
		t.Run(route, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			stub := filepath.Join(dir, "stub")
			require.NoError(t, os.WriteFile(stub, []byte("stub"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
				[]byte("module example.com/overrides\ngo "+microfat.GoVersion()+"\n"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))
			manifest := filepath.Join(dir, "manifest.yaml")
			content := fmt.Sprintf(`package: .
stub: %q
target_arch: amd64
compression:
  algorithm: zstd
  level: default
variants:
  - level: v1
    pgo: "off"
    compression:
      algorithm: zstd
      level: fastest
  - level: v2
    pgo: "off"
    compression:
      algorithm: lz4
      level: fastest
  - level: v3
    pgo: "off"
`, stub)
			require.NoError(t, os.WriteFile(manifest, []byte(content), 0o644))
			output := filepath.Join(dir, "app.fat")
			cmd := newPackCmd()
			if route == "pgo" {
				cmd = newPgoPackCmd()
			}
			cmd.SetArgs([]string{flagManifest, manifest, flagOutput, output, flagSkipELF,
				"--compression", codec.AlgorithmNone, flagVariantCompression, "AMD64_V1=lz4",
				flagVariantCompression, "v2=zstd:best"})
			require.NoError(t, cmd.Execute())
			f, err := os.Open(output)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, f.Close()) })
			stat, err := f.Stat()
			require.NoError(t, err)
			idx, results, err := pack.VerifyBinary(f, stat.Size())
			require.NoError(t, err)
			require.Len(t, results, 3)
			for _, result := range results {
				assert.True(t, result.Valid)
			}
			for level, algorithm := range map[string]string{
				"v1": codec.AlgorithmLZ4, "v2": codec.AlgorithmZstd, "v3": codec.AlgorithmNone,
			} {
				entry, found := idx.FindVariant(level)
				require.True(t, found)
				assert.Equal(t, algorithm, entry.Compression)
			}
		})
	}
}
