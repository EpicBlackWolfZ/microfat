//go:build linux

package e2e_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	cacheExecutablePrewarm = "prewarm"
	cacheExecutableJSON    = "--json"
)

func TestCacheMissingExecutePermissionRecovery(t *testing.T) {
	t.Parallel()
	for _, profile := range cacheFirstProfiles(t) {
		t.Run(profile.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			entry := prepareCacheFirstEntry(t, profile.path, dir)
			require.NoError(t, os.Chmod(entry, 0o600))
			before, err := os.Stat(entry)
			require.NoError(t, err)
			args := []string{cacheExecutablePrewarm, profile.path, "--all", "--verify", cacheExecutableJSON, "--cache-dir", dir}
			stdout, stderr, err := runCacheFirstProcess(t, cliPath, nil, args...)
			require.Error(t, err, stderr)
			require.Contains(t, stdout, "cache file lacks owner execute permission")
			after, err := os.Stat(entry)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after))
			require.Equal(t, before.Mode(), after.Mode())
			env := cacheFirstEnv(dir)
			requireCacheFirstMode(t, profile.path, env, execModeMemfd)
			after, err = os.Stat(entry)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after), "auto must leave the unusable safe entry intact")
			require.Equal(t, before.Mode(), after.Mode())
			args = []string{cacheExecutablePrewarm, profile.path, "--all", cacheExecutableJSON, "--cache-dir", dir}
			stdout, stderr, err = runCacheFirstProcess(t, cliPath, nil, args...)
			require.NoError(t, err, stderr)
			require.NotContains(t, strings.ReplaceAll(stdout, " ", ""), `"already_cached":true`)
			after, err = os.Stat(entry)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o700), after.Mode().Perm())
			env = append(cacheFirstEnv(dir), envExecCache)
			requireCacheFirstMode(t, profile.path, env, execModeCache)
			require.NoError(t, os.Chmod(entry, 0o600))
			requireCacheFirstMode(t, profile.path, env, execModeCache)
			after, err = os.Stat(entry)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o700), after.Mode().Perm(), "explicit cache mode rematerializes an executable entry")
		})
	}
}
