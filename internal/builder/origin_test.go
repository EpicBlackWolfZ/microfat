package builder

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
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
