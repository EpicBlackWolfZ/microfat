//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestAutoMemoryBudgetRejectionDoesNotMutateCache(t *testing.T) {
	for _, mode := range []struct {
		name     string
		exec     string
		dispatch string
	}{
		{name: "unset"},
		{name: "explicit auto", exec: format.ExecModeAuto},
		{name: "legacy auto", dispatch: format.ExecModeAuto},
		{name: "unrecognized mode", exec: "unrecognized"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			for _, state := range []string{cacheFirstAbsentDirectory, "insecure directory", "wrong size", "wrong digest"} {
				t.Run(state, func(t *testing.T) {
					f, entry, root, payload := cacheFirstFixture(t)
					t.Setenv(format.EnvExecMode, mode.exec)
					t.Setenv(format.EnvDispatchMode, mode.dispatch)
					t.Setenv("TMPDIR", t.TempDir())
					dir := filepath.Join(root, "microfat")
					t.Setenv(format.EnvCacheDir, dir)
					const insecureMode = os.FileMode(0o777)
					var corrupt []byte
					switch state {
					case "insecure directory":
						t.Setenv(format.EnvCacheDir, "")
						t.Setenv("XDG_CACHE_HOME", root)
						require.NoError(t, os.Mkdir(dir, insecureMode))
						require.NoError(t, os.Chmod(dir, insecureMode))
					case "wrong size":
						corrupt = []byte("short")
					case "wrong digest":
						corrupt = []byte(strings.Repeat("x", len(payload)))
					}
					path := filepath.Join(dir, entry.SHA256)
					var before unix.Stat_t
					if corrupt != nil {
						require.NoError(t, os.Mkdir(dir, 0o700))
						require.NoError(t, os.WriteFile(path, corrupt, 0o700))
						require.NoError(t, unix.Stat(path, &before))
					}
					resolveExistingCacheDirFunc = format.ResolveExistingCacheDirFD
					materializeCalls := 0
					resolveCacheDirFunc = func(custom string) (int, string, error) {
						materializeCalls++
						return format.ResolveCacheDirFD(custom)
					}
					readCgroupLimitsFunc = func() (cgroup.Limits, error) {
						return cgroup.Limits{CgroupVersion: cgroup.VersionV2, EffectiveMemoryLimitBytes: 1}, nil
					}
					memfdCalls, execCalls := 0, 0
					memfdCreateFunc = func(string, int) (int, error) {
						memfdCalls++
						return -1, unix.EPERM
					}
					execveFunc = func(string, []string, []string) error {
						execCalls++
						return unix.EACCES
					}

					require.ErrorIs(t, dispatchCacheFirst(f, entry), cgroup.ErrMemoryBudget)
					assert.Zero(t, memfdCalls, "budget rejection must precede memfd creation")
					assert.Zero(t, execCalls, "budget rejection must prevent execution")
					assert.Zero(t, materializeCalls, "budget rejection must prevent materializing cache resolution")
					switch state {
					case cacheFirstAbsentDirectory:
						_, err := os.Stat(dir)
						require.ErrorIs(t, err, os.ErrNotExist)
					case "insecure directory":
						info, err := os.Stat(dir)
						require.NoError(t, err)
						require.Equal(t, insecureMode, info.Mode().Perm(), "budget rejection must not repair permissions")
					default:
						data, err := os.ReadFile(path)
						require.NoError(t, err, "budget rejection must not delete corrupt regular cache entries")
						require.Equal(t, corrupt, data)
						var after unix.Stat_t
						require.NoError(t, unix.Stat(path, &after))
						require.Equal(t, before.Dev, after.Dev)
						require.Equal(t, before.Ino, after.Ino, "budget rejection must preserve the corrupt entry inode")
						require.Equal(t, before.Mode, after.Mode, "budget rejection must preserve corrupt entry permissions")
					}
				})
			}
		})
	}
}

func TestAutoMemoryBudgetAllowsVerifiedWarmCache(t *testing.T) {
	f, entry, dir, payload := cacheFirstFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, entry.SHA256), payload, 0o700))
	readCgroupLimitsFunc = func() (cgroup.Limits, error) {
		return cgroup.Limits{CgroupVersion: cgroup.VersionV2, EffectiveMemoryLimitBytes: 1}, nil
	}
	memfdCreateFunc = func(string, int) (int, error) {
		t.Fatal("verified warm cache must bypass extraction")
		return -1, nil
	}
	resolveCacheDirFunc = func(string) (int, string, error) {
		t.Fatal("verified warm cache must bypass materializing cache resolution")
		return -1, "", nil
	}
	execCalls := 0
	execveFunc = func(path string, _, env []string) error {
		execCalls++
		require.Contains(t, env, format.EnvExecMode+"=cache")
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, payload, data)
		return nil
	}
	require.NoError(t, dispatchCacheFirst(f, entry))
	require.Equal(t, 1, execCalls)
}

func TestAutoMemoryBudgetPreservesWarmExecFailure(t *testing.T) {
	f, entry, dir, payload := cacheFirstFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, entry.SHA256), payload, 0o700))
	readCgroupLimitsFunc = func() (cgroup.Limits, error) {
		return cgroup.Limits{CgroupVersion: cgroup.VersionV2, EffectiveMemoryLimitBytes: 1}, nil
	}
	memfdCreateFunc = func(string, int) (int, error) {
		t.Fatal("budget rejection must precede memfd creation after denied warm execution")
		return -1, nil
	}
	resolveCacheDirFunc = func(string) (int, string, error) {
		t.Fatal("denied warm execution must not retry materializing cache resolution")
		return -1, "", nil
	}
	execCalls := 0
	execveFunc = func(_ string, _, env []string) error {
		execCalls++
		require.Contains(t, env, format.EnvExecMode+"=cache")
		return unix.EACCES
	}
	err := dispatchCacheFirst(f, entry)
	require.ErrorIs(t, err, cgroup.ErrMemoryBudget)
	require.ErrorIs(t, err, unix.EACCES)
	var dispatchErr *format.DispatchError
	require.ErrorAs(t, err, &dispatchErr)
	require.Len(t, dispatchErr.Attempts, 2)
	require.Equal(t, format.ExecModeCache, dispatchErr.Attempts[0].AttemptedMode)
	require.Equal(t, format.ExecModeMemfd, dispatchErr.Attempts[1].AttemptedMode)
	require.Equal(t, 1, execCalls)
}
