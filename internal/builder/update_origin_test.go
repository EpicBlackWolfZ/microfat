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
