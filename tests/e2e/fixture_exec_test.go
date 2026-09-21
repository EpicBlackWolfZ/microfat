//go:build linux

package e2e_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFixtureExecWaitsOnlyForPreStartBusy(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "copied fixture")
	copyFile(t, goldenFatBin, path)
	writer, err := os.OpenFile(path, os.O_WRONLY, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Close() })
	var output bytes.Buffer
	attempts := 0
	err = runFixtureCommand(func() *exec.Cmd {
		attempts++
		const releaseAttempt = 3
		if attempts == releaseAttempt {
			require.NoError(t, writer.Close())
		}
		cmd := exec.Command(path, "--echo-args", "one execution")
		cmd.Stdout, cmd.Stderr = &output, &output
		return cmd
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, attempts, 3)
	assert.Equal(t, "one execution", output.String(), "only the successful process may execute")
}

func TestFixtureExecBusyExhaustion(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "held fixture")
	copyFile(t, goldenFatBin, path)
	writer, err := os.OpenFile(path, os.O_WRONLY, 0)
	require.NoError(t, err)
	defer func() { require.NoError(t, writer.Close()) }()
	attempts := 0
	err = runFixtureCommand(func() *exec.Cmd {
		attempts++
		return exec.Command(path)
	})
	require.ErrorIs(t, err, syscall.ETXTBSY)
	assert.Equal(t, fixtureBusyAttempts, attempts, "busy fixture must eventually fail rather than loop indefinitely")
}

func TestFixtureExecPreservesFailures(t *testing.T) {
	t.Parallel()
	t.Run("missing_executable", func(t *testing.T) {
		t.Parallel()
		attempts := 0
		err := runFixtureCommand(func() *exec.Cmd {
			attempts++
			return exec.Command(filepath.Join(t.TempDir(), "missing"))
		})
		require.ErrorIs(t, err, os.ErrNotExist)
		assert.Equal(t, 1, attempts)
	})
	t.Run("started_process_exit_status", func(t *testing.T) {
		t.Parallel()
		attempts := 0
		err := runFixtureCommand(func() *exec.Cmd {
			attempts++
			return exec.Command(goldenFatBin, "--exit-code", "42")
		})
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit)
		assert.Equal(t, expectedExitCode42, exit.ExitCode())
		assert.Equal(t, 1, attempts, "a started process must never be retried")
	})
}
