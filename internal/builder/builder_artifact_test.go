package builder_test

import (
	"context"
	"debug/buildinfo"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/builder"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAndPack_RealCompilation_InspectBuildSettings(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stubFile := createDummyELF(t, tmpDir, "microfat-stub", testArchAMD64)

	pkgDir := filepath.Join(tmpDir, "pkg")
	require.NoError(t, os.MkdirAll(pkgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module testpkg\ngo 1.27.1\n"), 0o644))

	outFile := filepath.Join(tmpDir, "bin", "real_amd64_fat")

	m := &builder.Manifest{
		AppName:    "real-amd64-test",
		Package:    pkgDir,
		Output:     outFile,
		Stub:       stubFile,
		TargetOS:   testOSLinux,
		TargetArch: testArchAMD64,
		Variants: []builder.VariantConfig{
			{Level: "v1", PGO: pgoOff},
			{Level: "v3", PGO: pgoOff},
		},
		Dir: tmpDir,
	}

	res, err := builder.BuildAndPack(context.Background(), m, builder.BuildOptions{
		Concurrency:       2,
		KeepIntermediates: true,
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	// Inspect intermediate artifacts
	for _, level := range []string{"v1", "v3"} {
		intermediatePath := res.CompiledVariants[level]
		require.FileExists(t, intermediatePath)

		bi, err := buildinfo.ReadFile(intermediatePath)
		require.NoError(t, err)

		settings := make(map[string]string)
		for _, s := range bi.Settings {
			settings[s.Key] = s.Value
		}

		assert.Equal(t, "linux", settings["GOOS"])
		assert.Equal(t, "amd64", settings["GOARCH"])
		assert.Equal(t, level, settings["GOAMD64"])
		assert.NotContains(t, settings, "GOARM64")
	}

	// Verify the packaged fat binary
	f, err := os.Open(outFile)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	require.NoError(t, err)

	idx, results, err := pack.VerifyBinary(f, stat.Size())
	require.NoError(t, err)
	assert.Equal(t, "real-amd64-test", idx.AppName)
	require.Len(t, results, 2)
	for _, r := range results {
		assert.True(t, r.Valid)
	}
}

func TestBuildAndPack_RealCompilation_ARM64_InspectBuildSettings(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stubFile := createDummyELF(t, tmpDir, "microfat-stub", testArchARM64)

	pkgDir := filepath.Join(tmpDir, "pkg")
	require.NoError(t, os.MkdirAll(pkgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module arm64pkg\ngo 1.27.1\n"), 0o644))

	outFile := filepath.Join(tmpDir, "bin", "real_arm64_fat")

	m := &builder.Manifest{
		AppName:    "real-arm64-test",
		Package:    pkgDir,
		Output:     outFile,
		Stub:       stubFile,
		TargetOS:   testOSLinux,
		TargetArch: testArchARM64,
		Variants: []builder.VariantConfig{
			{Level: testLevelV8_0, PGO: pgoOff},
			{Level: "v8.1", PGO: pgoOff},
		},
		Dir: tmpDir,
	}

	res, err := builder.BuildAndPack(context.Background(), m, builder.BuildOptions{
		Concurrency:       2,
		KeepIntermediates: true,
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	// Inspect intermediate ARM64 artifacts
	for _, level := range []string{testLevelV8_0, "v8.1"} {
		intermediatePath := res.CompiledVariants[level]
		require.FileExists(t, intermediatePath)

		bi, err := buildinfo.ReadFile(intermediatePath)
		require.NoError(t, err)

		settings := make(map[string]string)
		for _, s := range bi.Settings {
			settings[s.Key] = s.Value
		}

		assert.Equal(t, "linux", settings["GOOS"])
		assert.Equal(t, "arm64", settings["GOARCH"])
		assert.NotContains(t, settings, "GOAMD64")

		arm64Setting := settings["GOARM64"]
		require.NotEmpty(t, arm64Setting)
		if level == "v8.0" {
			assert.Equal(t, "v8.0", arm64Setting)
		} else {
			// v8.1 or v8.1,lse are canonical equivalents
			assert.Contains(t, []string{"v8.1", "v8.1,lse"}, arm64Setting)
		}
	}
}

// TestBuildAndPack_WrongArtifactMetadata_Rejection verifies that when a compiler emits
// a successful zero exit code but writes an artifact with wrong target metadata or missing buildinfo,
// the builder detects it immediately before packaging, cancels workers, cleans intermediates,
// and preserves any pre-existing output file.
func TestBuildAndPack_WrongArtifactMetadata_Rejection(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	stubFile := createDummyELF(t, tmpDir, "microfat-stub", testArchAMD64)

	pkgDir := filepath.Join(tmpDir, "pkg")
	require.NoError(t, os.MkdirAll(pkgDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "go.mod"), []byte("module fake\ngo 1.27.1\n"), 0o644))

	// Pre-existing output file
	outFile := filepath.Join(tmpDir, "bin", "preserved_output.bin")
	require.NoError(t, os.MkdirAll(filepath.Dir(outFile), 0o755))
	preexistingBytes := []byte("do not overwrite me!")
	require.NoError(t, os.WriteFile(outFile, preexistingBytes, 0o755))

	realGo, err := exec.LookPath("go")
	require.NoError(t, err)

	t.Run("compiler_outputs_wrong_cpu_tier", func(t *testing.T) {
		// Create a fake compiler wrapper that forces GOAMD64=v4 regardless of what was requested
		wrapperScript := filepath.Join(tmpDir, "fake_go_wrong_tier.sh")
		scriptContent := fmt.Sprintf("#!/bin/sh\nexport GOAMD64=v4\nexec %s \"$@\"\n", realGo)
		require.NoError(t, os.WriteFile(wrapperScript, []byte(scriptContent), 0o755))

		m := &builder.Manifest{
			AppName:    "wrong-tier-test",
			Package:    pkgDir,
			Output:     outFile,
			Stub:       stubFile,
			TargetOS:   testOSLinux,
			TargetArch: microarch.ArchAMD64,
			Variants: []builder.VariantConfig{
				{Level: "v1", PGO: pgoOff}, // Expects v1, but wrapper forces v4
			},
			Dir: tmpDir,
		}

		res, err := builder.BuildAndPack(context.Background(), m, builder.BuildOptions{
			GoBinary:          wrapperScript,
			SkipELFValidation: false,
		})
		require.Error(t, err)
		require.ErrorIs(t, err, builder.ErrInvalidArtifactBuildInfo)
		assert.Contains(t, err.Error(), "GOAMD64 mismatch")
		assert.Nil(t, res)

		// Preexisting output untouched
		data, err := os.ReadFile(outFile)
		require.NoError(t, err)
		assert.Equal(t, preexistingBytes, data)
	})

	t.Run("compiler_outputs_missing_buildinfo_even_with_SkipELFValidation", func(t *testing.T) {
		// Create a fake compiler wrapper that writes a dummy non-Go ELF binary
		wrapperScript := filepath.Join(tmpDir, "fake_go_no_buildinfo.sh")
		// The script extracts output path from '-o <path>' and writes a dummy ELF
		dummyELF := createDummyELF(t, tmpDir, "dummy_elf_src", testArchAMD64)
		scriptContent := fmt.Sprintf(`#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    out="$2"
    shift 2
  else
    shift
  fi
done
cp "%s" "$out"
exit 0
`, dummyELF)
		require.NoError(t, os.WriteFile(wrapperScript, []byte(scriptContent), 0o755))

		m := &builder.Manifest{
			AppName:    "no-buildinfo-test",
			Package:    pkgDir,
			Output:     outFile,
			Stub:       stubFile,
			TargetOS:   testOSLinux,
			TargetArch: microarch.ArchAMD64,
			Variants: []builder.VariantConfig{
				{Level: "v1", PGO: pgoOff},
			},
			Dir: tmpDir,
		}

		// SkipELFValidation=true MUST NOT bypass buildinfo validation!
		res, err := builder.BuildAndPack(context.Background(), m, builder.BuildOptions{
			GoBinary:          wrapperScript,
			SkipELFValidation: true,
		})
		require.Error(t, err)
		require.ErrorIs(t, err, builder.ErrInvalidArtifactBuildInfo)
		assert.Nil(t, res)

		// Preexisting output untouched
		data, err := os.ReadFile(outFile)
		require.NoError(t, err)
		assert.Equal(t, preexistingBytes, data)
	})
}
