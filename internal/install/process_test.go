//go:build linux

package install

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const childEnvironment = "MICROFAT_INSTALL_TEST_CHILD"

// This helper is entered only by a test subprocess with explicit private fixtures.
func TestInstallationChild(t *testing.T) {
	mode := os.Getenv(childEnvironment)
	if mode == "" {
		t.Skip("subprocess entrypoint")
	}
	paths := Paths{Bin: os.Getenv("TEST_INSTALL_BIN"), Store: os.Getenv("TEST_INSTALL_STORE")}
	if mode == "lock" {
		root, err := os.OpenRoot(paths.Store)
		require.NoError(t, err)
		defer root.Close()
		file, err := lock(t.Context(), root)
		require.NoError(t, err)
		defer file.Close()
		_, err = fmt.Fprintln(os.Stdout, "ready")
		require.NoError(t, err)
		_, err = io.ReadFull(os.Stdin, make([]byte, 1))
		require.NoError(t, err)
		return
	}
	snapshot, err := Inspect(paths)
	require.NoError(t, err)
	data, err := os.ReadFile(os.Getenv("TEST_INSTALL_GENERATION"))
	require.NoError(t, err)
	var generation Generation
	require.NoError(t, json.Unmarshal(data, &generation))
	_, err = fmt.Fprintln(os.Stdout, "ready")
	require.NoError(t, err)
	_, err = io.ReadFull(os.Stdin, make([]byte, 1))
	require.NoError(t, err)
	_, err = apply(t.Context(), snapshot, generation, os.Getenv("TEST_INSTALL_SOURCE"), ApplyOptions{}, func(point string) error {
		if point == os.Getenv("TEST_INSTALL_STOP") {
			_, err := fmt.Fprintln(os.Stdout, "stopped")
			require.NoError(t, err)
			_, err = io.ReadFull(os.Stdin, make([]byte, 1))
			require.NoError(t, err)
		}
		return nil
	})
	if errors.Is(err, ErrChanged) {
		return
	}
	require.NoError(t, err)
}

type child struct {
	command *exec.Cmd
	input   io.WriteCloser
	output  *bufio.Reader
}

func startChild(t *testing.T, paths Paths, mode, version, stop string) child {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestInstallationChild$")
	command.Env = append(os.Environ(), childEnvironment+"="+mode, "TEST_INSTALL_BIN="+paths.Bin, "TEST_INSTALL_STORE="+paths.Store)
	if mode != "lock" {
		generation, source := fixture(t, version)
		metadata := filepath.Join(t.TempDir(), generationFile)
		rewriteJSON(t, metadata, generation)
		command.Env = append(command.Env, "TEST_INSTALL_GENERATION="+metadata, "TEST_INSTALL_SOURCE="+source, "TEST_INSTALL_STOP="+stop)
	}
	output, err := command.StdoutPipe()
	require.NoError(t, err)
	input, err := command.StdinPipe()
	require.NoError(t, err)
	command.Stderr = os.Stderr
	require.NoError(t, command.Start())
	t.Cleanup(func() { _ = command.Process.Kill(); _ = input.Close() })
	result := child{command: command, input: input, output: bufio.NewReader(output)}
	line, err := result.output.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "ready\n", line)
	return result
}

func (c child) proceed(t *testing.T) {
	t.Helper()
	_, err := c.input.Write([]byte("x"))
	require.NoError(t, err)
}

func TestRealProcessLockAndCrashRelease(t *testing.T) {
	t.Parallel()
	paths := pathsFor(t)
	applyFixture(t, paths, "0.3.0")
	process := startChild(t, paths, "lock", "", "")
	root, err := os.OpenRoot(paths.Store)
	require.NoError(t, err)
	defer root.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err = lock(ctx, root)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, process.command.Process.Kill())
	require.Error(t, process.command.Wait())
	file, err := lock(t.Context(), root)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	info, err := root.Lstat(lockFile)
	require.NoError(t, err)
	applyFixture(t, paths, "0.3.1")
	after, err := root.Lstat(lockFile)
	require.NoError(t, err)
	assert.True(t, os.SameFile(info, after), "locking must keep the same inode across operations")
}

func TestIndependentInstallersSerialize(t *testing.T) {
	t.Parallel()
	paths := pathsFor(t)
	applyFixture(t, paths, "0.3.0")
	first := startChild(t, paths, "apply", "0.3.1", "")
	second := startChild(t, paths, "apply", "0.3.2", "")
	first.proceed(t)
	second.proceed(t)
	require.NoError(t, first.command.Wait())
	require.NoError(t, second.command.Wait())
	active := readActive(t, paths)
	entries, err := os.ReadDir(filepath.Join(paths.Store, generationDir))
	require.NoError(t, err)
	assert.Len(t, entries, 2, "a stale concurrent installer must not overwrite a newly selected release")
	assert.Contains(t, []string{"0.3.1", "0.3.2"}, active.Version)
	for _, name := range Products() {
		out, err := exec.CommandContext(t.Context(), filepath.Join(paths.Bin, name)).Output()
		require.NoError(t, err)
		assert.Equal(t, active.Version+":"+name+"\n", string(out))
	}
}

func TestActualProcessInterruption(t *testing.T) {
	t.Parallel()
	for _, point := range []string{"staged-microfat", "generation-published", "activated", "linked-microfat"} {
		t.Run(point, func(t *testing.T) {
			t.Parallel()
			paths := pathsFor(t)
			applyFixture(t, paths, "0.3.0")
			process := startChild(t, paths, "apply", "0.3.1", point)
			process.proceed(t)
			line, err := process.output.ReadString('\n')
			require.NoError(t, err)
			require.Equal(t, "stopped\n", line)
			require.NoError(t, process.command.Process.Kill())
			require.Error(t, process.command.Wait())
			want := "0.3.0"
			if point == "activated" || point == "linked-microfat" {
				want = "0.3.1"
			}
			assert.Equal(t, want, readActive(t, paths).Version)
			applyFixture(t, paths, "0.3.1")
			assert.Equal(t, "0.3.1", readActive(t, paths).Version)
		})
	}
}
