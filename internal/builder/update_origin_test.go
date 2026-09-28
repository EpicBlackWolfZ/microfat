//go:build linux

package builder

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/install"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveUpdateNativeIdentity(t *testing.T) {
	// These seams belong to existing origin tests and must not be used concurrently.
	oldReadlink, oldExecutable, oldEval := readlinkProcSelfExe, osExecutableFunc, evalSymlinksFunc
	t.Cleanup(func() {
		readlinkProcSelfExe = oldReadlink
		osExecutableFunc = oldExecutable
		evalSymlinksFunc = oldEval
	})
	physical, err := os.Executable()
	require.NoError(t, err)
	physical, err = filepath.EvalSymlinks(physical)
	require.NoError(t, err)
	readlinkProcSelfExe = func() (string, error) { return physical, nil }
	osExecutableFunc = func() (string, error) { return physical, nil }
	evalSymlinksFunc = filepath.EvalSymlinks
	actual, err := ResolveInstallationExecutable()
	require.NoError(t, err)
	assert.Equal(t, physical, actual)
	t.Setenv("MICROFAT_ORIGINAL_EXE", "/unrelated/target")
	actual, err = ResolveInstallationExecutable()
	require.NoError(t, err)
	assert.Equal(t, physical, actual)
	for _, scenario := range []string{"disappeared", "redirected"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			readlinkProcSelfExe = func() (string, error) {
				calls++
				return physical, nil
			}
			evalSymlinksFunc = func(path string) (string, error) {
				if path == physical && calls == 2 {
					if scenario == "disappeared" {
						return "", os.ErrNotExist
					}
					return filepath.Join(t.TempDir(), "microfat"), nil
				}
				return filepath.EvalSymlinks(path)
			}
			resolved, err := ResolveInstallationExecutable()
			require.ErrorIs(t, err, install.ErrChanged)
			assert.Empty(t, resolved, "a changed native source must not become an update destination")
			if scenario == "disappeared" {
				assert.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
	evalSymlinksFunc = filepath.EvalSymlinks
	substitute := filepath.Join(t.TempDir(), "microfat")
	require.NoError(t, os.WriteFile(substitute, []byte("different inode"), 0o755))
	readlinkProcSelfExe = func() (string, error) { return substitute, nil }
	osExecutableFunc = func() (string, error) { return substitute, nil }
	_, err = ResolveInstallationExecutable()
	require.ErrorIs(t, err, install.ErrChanged)
	readlinkProcSelfExe = func() (string, error) { return "", errors.New("procfs unavailable") }
	_, err = ResolveInstallationExecutable()
	require.ErrorContains(t, err, "binding running image")
	osExecutableFunc = func() (string, error) { return "", errors.New("no executable") }
	_, err = ResolveInstallationExecutable()
	require.ErrorContains(t, err, "no executable")
}
