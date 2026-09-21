//go:build linux

package e2e_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// Pause after exec has loaded the ELF image but before launcher Go code runs.
func runPausedImage(t *testing.T, path string, args, env []string, change func()) (string, error) {
	t.Helper()
	const timeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Ptrace: true}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		if errors.Is(err, unix.EPERM) {
			t.Skip("ptrace is unavailable on this host")
		}
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	var status unix.WaitStatus
	_, err := unix.Wait4(cmd.Process.Pid, &status, 0, nil)
	require.NoError(t, err)
	require.True(t, status.Stopped())
	require.Equal(t, unix.SIGTRAP, status.StopSignal())
	change()
	require.NoError(t, unix.PtraceDetach(cmd.Process.Pid))
	err = cmd.Wait()
	waited = true
	require.NoError(t, ctx.Err(), "launcher did not finish: %s", output.String())
	return output.String(), err
}

func TestRunningImageSurvivesDeploymentChange(t *testing.T) {
	baseLevel := "v1"
	if currentHostArch == archARM64 {
		baseLevel = manifestARM64Base
	}
	for _, profile := range []string{launcherFullProfile, launcherMinimalProfile} {
		t.Run(profile, func(t *testing.T) {
			dir := t.TempDir()
			stub := stubPath
			if profile == launcherMinimalProfile {
				stub = filepath.Join(dir, launcherMinimalProfile)
				require.NoError(t, compileBinaryWithFlags(stubPackagePath, stub, []string{"GOAMD64=v1"}, "-tags=minimal"))
			}
			old := filepath.Join(dir, "old-fat")
			newer := filepath.Join(dir, "new-fat")
			require.NoError(t, packBinary(cliPath, stub, "old-deployment", old, map[string]string{baseLevel: goldenVariantBins[baseLevel]}))
			// A replacement CLI payload is intentionally distinguishable from the golden app.
			require.NoError(t, packBinary(cliPath, stub, "new-deployment", newer, map[string]string{baseLevel: cliPath}))
			original, err := os.ReadFile(old)
			require.NoError(t, err)
			replacement, err := os.ReadFile(newer)
			require.NoError(t, err)
			for _, mode := range []string{"memfd", "cache"} {
				for _, action := range []string{"unchanged", "unlink", "replace"} {
					t.Run(mode+"/"+action, func(t *testing.T) {
						root := t.TempDir()
						path := filepath.Join(root, "deployment")
						require.NoError(t, os.WriteFile(path, original, defaultFilePerm))
						output, err := runPausedImage(t, path, nil, []string{"MICROFAT_EXEC_MODE=" + mode}, func() {
							changeDeployment(t, action, path, replacement)
						})
						require.NoError(t, err, output)
						require.Contains(t, output, "golden:variant="+baseLevel)
						assertDeployment(t, action, path, original, replacement)
					})
				}
			}
			if profile == launcherFullProfile {
				testStaleImageMetaCommands(t, original, replacement)
			}
		})
	}
}

const (
	manifestARM64Base      = "v8.0"
	launcherFullProfile    = "full"
	launcherMinimalProfile = "minimal"
)

func changeDeployment(t *testing.T, action, path string, replacement []byte) {
	t.Helper()
	switch action {
	case "unlink":
		require.NoError(t, os.Remove(path))
	case "replace":
		require.NoError(t, os.WriteFile(path+".next", replacement, defaultFilePerm))
		require.NoError(t, os.Rename(path+".next", path))
	}
}

func assertDeployment(t *testing.T, action, path string, original, replacement []byte) {
	t.Helper()
	if action == "unlink" {
		require.NoFileExists(t, path)
		return
	}
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	if action == "replace" {
		require.Equal(t, replacement, got)
	} else {
		require.Equal(t, original, got)
	}
}

func testStaleImageMetaCommands(t *testing.T, original, replacement []byte) {
	t.Helper()
	for _, action := range []string{"unlink", "replace"} {
		for _, command := range []string{"trim", "specialize", "optimize", "trim-to", "specialize-to", "optimize-to", "info"} {
			t.Run(action+"/"+command, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "deployment")
				require.NoError(t, os.WriteFile(path, original, defaultFilePerm))
				arg := "--microfat:" + command
				if command == "trim-to" || command == "specialize-to" || command == "optimize-to" {
					arg += "=" + path
				}
				output, err := runPausedImage(t, path, []string{arg}, nil, func() {
					changeDeployment(t, action, path, replacement)
				})
				if command == "info" {
					require.NoError(t, err, output)
					require.Contains(t, output, "old-deployment")
				} else {
					require.Error(t, err, output)
					require.Contains(t, output, "deployment pathname no longer identifies the running image")
				}
				assertDeployment(t, action, path, original, replacement)
			})
		}
	}
}
