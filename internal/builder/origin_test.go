package builder

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsMemfdTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		target   string
		expected bool
	}{
		{
			name:     "ValidKernelMemfdWithSlash",
			target:   "/memfd:microfat_payload (deleted)",
			expected: true,
		},
		{
			name:     "ValidKernelMemfdWithoutSlash",
			target:   "memfd:microfat (deleted)",
			expected: true,
		},
		{
			name:     "Negative_OptMemfdBuild",
			target:   "/opt/memfd:build/microfat",
			expected: false,
		},
		{
			name:     "Negative_OptReleaseDeleted",
			target:   "/opt/release (deleted)/microfat",
			expected: false,
		},
		{
			name:     "Negative_EmptyName",
			target:   "/memfd: (deleted)",
			expected: false,
		},
		{
			name:     "Negative_ContainsSlashInName",
			target:   "/memfd:nested/name (deleted)",
			expected: false,
		},
		{
			name:     "Negative_NoDeletedSuffix",
			target:   "/memfd:microfat",
			expected: false,
		},
		{
			name:     "Negative_ArbitraryPath",
			target:   "/usr/local/bin/microfat",
			expected: false,
		},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, isMemfdTarget(tc.target))
		})
	}
}

func TestIsInsideCache_ComponentComparison(t *testing.T) {
	tempDir := t.TempDir()
	cacheDir := filepath.Join(tempDir, "cache", "microfat")
	require.NoError(t, os.MkdirAll(cacheDir, 0o755))

	origResolveCache := resolveCacheDirFDFunc
	t.Cleanup(func() {
		resolveCacheDirFDFunc = origResolveCache
	})
	resolveCacheDirFDFunc = func(string) (int, string, error) {
		return -1, cacheDir, nil
	}

	tests := []struct {
		name     string
		target   string
		expected bool
	}{
		{
			name:     "ValidDirectChild",
			target:   filepath.Join(cacheDir, "microfat"),
			expected: true,
		},
		{
			name:     "ValidNestedChild",
			target:   filepath.Join(cacheDir, "sub", "payload"),
			expected: true,
		},
		{
			name:     "ValidChildStartingWithTwoDots",
			target:   filepath.Join(cacheDir, "..foo"),
			expected: true,
		},
		{
			name:     "Negative_CacheRootItself",
			target:   cacheDir,
			expected: false,
		},
		{
			name:     "Negative_SiblingPrefixDir",
			target:   filepath.Join(tempDir, "cache", "microfat-tools", "microfat"),
			expected: false,
		},
		{
			name:     "Negative_ParentDir",
			target:   filepath.Join(tempDir, "cache"),
			expected: false,
		},
		{
			name:     "Negative_UnrelatedPath",
			target:   "/usr/local/bin/microfat",
			expected: false,
		},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, isInsideCache(tc.target))
		})
	}
}

func TestIsInsideCache_SymlinkedAndRelativeRoots(t *testing.T) {
	tempDir := t.TempDir()
	realCache := filepath.Join(tempDir, "real_cache")
	symlinkCache := filepath.Join(tempDir, "symlink_cache")
	require.NoError(t, os.MkdirAll(realCache, 0o755))
	require.NoError(t, os.Symlink(realCache, symlinkCache))

	origResolveCache := resolveCacheDirFDFunc
	t.Cleanup(func() {
		resolveCacheDirFDFunc = origResolveCache
	})
	resolveCacheDirFDFunc = func(string) (int, string, error) {
		return -1, symlinkCache, nil
	}

	// Target inside realCache should be recognized because symlinks are evaluated
	targetInReal := filepath.Join(realCache, "payload")
	assert.True(t, isInsideCache(targetInReal))

	// Target inside symlinkCache should also be recognized
	targetInSym := filepath.Join(symlinkCache, "payload")
	assert.True(t, isInsideCache(targetInSym))
}

func TestResolveInstallationDirectory_FalsePositivePathsPreservedAsNative(t *testing.T) {
	tmpDir := setupOriginMocks(t)

	// Create three false-positive native paths
	falsePositives := []string{
		filepath.Join(tmpDir, "opt", "memfd:build", "microfat"),
		filepath.Join(tmpDir, "opt", "release (deleted)", "microfat"),
		filepath.Join(tmpDir, "user", ".cache", "microfat-tools", "microfat"),
	}

	for _, fp := range falsePositives {
		path := fp
		t.Run(path, func(t *testing.T) {
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte("native-exe"), 0o755))

			osExecutableFunc = func() (string, error) { return path, nil }
			readlinkProcSelfExe = func() (string, error) { return path, nil }

			// Set hostile inherited dispatch metadata
			_ = os.Setenv(format.EnvOriginalExe, "/hostile/original/exe")
			_ = os.Setenv(format.EnvExecMode, format.ExecModeMemfd)
			defer func() {
				_ = os.Unsetenv(format.EnvOriginalExe)
				_ = os.Unsetenv(format.EnvExecMode)
			}()

			// Must resolve as native directory, ignoring the hostile metadata
			resolvedDir, err := ResolveInstallationDirectory()
			require.NoError(t, err)
			assert.Equal(t, filepath.Dir(path), resolvedDir)
		})
	}
}

func TestResolveInstallationDirectory_DecoyWhitespacePath(t *testing.T) {
	tmpDir := setupOriginMocks(t)

	installDir := filepath.Join(tmpDir, "install_dir")
	require.NoError(t, os.MkdirAll(installDir, 0o755))

	// Create two distinguishable variant payloads:
	// Target payload (for "app ")
	targetV1Path := filepath.Join(tmpDir, "target_v1.bin")
	targetV1Bytes := append([]byte("TARGET_DISTINGUISHABLE_PAYLOAD_V1_"), make([]byte, 200)...)
	require.NoError(t, os.WriteFile(targetV1Path, targetV1Bytes, 0o755))
	targetHash := sha256.Sum256(targetV1Bytes)
	targetDigest := hex.EncodeToString(targetHash[:])

	// Decoy payload (for "app")
	decoyV1Path := filepath.Join(tmpDir, "decoy_v1.bin")
	decoyV1Bytes := append([]byte("DECOY_DISTINGUISHABLE_PAYLOAD_V1_"), make([]byte, 300)...)
	require.NoError(t, os.WriteFile(decoyV1Path, decoyV1Bytes, 0o755))

	stubPath := createDummyELF(t, tmpDir, "stub.elf", runtime.GOARCH)

	baseLevel := "v1"
	if runtime.GOARCH == "arm64" {
		baseLevel = "v8.0"
	}

	// Pack target fat binary: "app "
	targetFatPath := filepath.Join(installDir, "app ")
	targetPackOpts := pack.DefaultOptions()
	targetPackOpts.StubPath = stubPath
	targetPackOpts.OutputPath = targetFatPath
	targetPackOpts.AppName = "app-target"
	targetPackOpts.TargetArch = runtime.GOARCH
	targetPackOpts.Variants = map[string]string{baseLevel: targetV1Path}
	targetPackOpts.SkipELFValidation = true
	_, err := pack.Pack(targetPackOpts)
	require.NoError(t, err)

	// Pack decoy fat binary: "app"
	decoyFatPath := filepath.Join(installDir, "app")
	decoyPackOpts := pack.DefaultOptions()
	decoyPackOpts.StubPath = stubPath
	decoyPackOpts.OutputPath = decoyFatPath
	decoyPackOpts.AppName = "app-decoy"
	decoyPackOpts.TargetArch = runtime.GOARCH
	decoyPackOpts.Variants = map[string]string{baseLevel: decoyV1Path}
	decoyPackOpts.SkipELFValidation = true
	_, err = pack.Pack(decoyPackOpts)
	require.NoError(t, err)

	// Configure mocked dispatched environment to match the TARGET running payload
	readlinkProcSelfExe = func() (string, error) {
		return mockMemfdTarget, nil
	}
	readAndHashSelfExe = func() (int64, string, error) {
		return int64(len(targetV1Bytes)), targetDigest, nil
	}

	t.Setenv(format.EnvExecMode, format.ExecModeMemfd)
	t.Setenv(format.EnvSelectedVariant, baseLevel)
	t.Setenv(format.EnvSelectedSize, strconv.FormatInt(int64(len(targetV1Bytes)), 10))
	t.Setenv(format.EnvSelectedSHA256, targetDigest)

	// Case 1: Hint correctly specifies target path with trailing space ("app ")
	t.Run("ValidTargetWithTrailingSpace", func(t *testing.T) {
		t.Setenv(format.EnvOriginalExe, targetFatPath)
		resolvedDir, err := ResolveInstallationDirectory()
		require.NoError(t, err)
		assert.Equal(t, installDir, resolvedDir)
	})

	// Case 2: Hint mistakenly trimmed to decoy path ("app")
	t.Run("TrimmedDecoyTarget_RejectedByOriginConsistency", func(t *testing.T) {
		t.Setenv(format.EnvOriginalExe, decoyFatPath)
		_, err := ResolveInstallationDirectory()
		require.Error(t, err, "origin consistency must reject decoy path even if it exists in the same directory")
	})

	// Case 3: Parent directory contains literal whitespace
	t.Run("ParentDirectoryContainsWhitespace", func(t *testing.T) {
		spacesDir := filepath.Join(tmpDir, "path with spaces in dir")
		require.NoError(t, os.MkdirAll(spacesDir, 0o755))
		fatPathInSpacesDir := filepath.Join(spacesDir, "myfat")

		pOpts := pack.DefaultOptions()
		pOpts.StubPath = stubPath
		pOpts.OutputPath = fatPathInSpacesDir
		pOpts.AppName = "app-spaces-dir"
		pOpts.TargetArch = runtime.GOARCH
		pOpts.Variants = map[string]string{baseLevel: targetV1Path}
		pOpts.SkipELFValidation = true
		_, err := pack.Pack(pOpts)
		require.NoError(t, err)

		t.Setenv(format.EnvOriginalExe, fatPathInSpacesDir)
		resolvedDir, err := ResolveInstallationDirectory()
		require.NoError(t, err)
		assert.Equal(t, spacesDir, resolvedDir)
	})
}
