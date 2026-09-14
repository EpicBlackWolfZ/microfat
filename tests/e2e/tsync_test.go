package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestClassifySeccompResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		r1         uintptr
		errno      syscall.Errno
		wantAction SeccompAction
		wantErrStr string
	}{
		{
			name:       "Success_ZeroReturnZeroErrno",
			r1:         0,
			errno:      0,
			wantAction: SeccompActionSuccess,
		},
		{
			name:       "ThreadError_NonZeroTIDZeroErrno",
			r1:         1337,
			errno:      0,
			wantAction: SeccompActionThreadError,
			wantErrStr: "seccomp TSYNC failed on thread 1337",
		},
		{
			name:       "FallbackPermitted_ENOSYS",
			r1:         0,
			errno:      unix.ENOSYS,
			wantAction: SeccompActionFallbackPermitted,
			wantErrStr: "fallback permitted",
		},
		{
			name:       "FallbackPermitted_EINVAL",
			r1:         0,
			errno:      unix.EINVAL,
			wantAction: SeccompActionFallbackPermitted,
			wantErrStr: "fallback permitted",
		},
		{
			name:       "HardError_EPERM",
			r1:         0,
			errno:      unix.EPERM,
			wantAction: SeccompActionHardError,
			wantErrStr: "operation not permitted",
		},
		{
			name:       "HardError_EACCES",
			r1:         0,
			errno:      unix.EACCES,
			wantAction: SeccompActionHardError,
			wantErrStr: "permission denied",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			action, err := ClassifySeccompResult(tc.r1, tc.errno)
			assert.Equal(t, tc.wantAction, action)
			if tc.wantErrStr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrStr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSeccompRunner_SubprocessExecution(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	runnerBin := filepath.Join(tempDir, "seccomp-runner")

	// Compile seccomp_runner
	buildCmd := exec.Command("go", "build", "-o", runnerBin, "./testdata/seccomp_runner/main.go")
	buildCmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := buildCmd.CombinedOutput()
	require.NoError(t, err, "compiling seccomp_runner: %s", string(out))

	t.Run("ExecutesStandardCommand", func(t *testing.T) {
		cmd := exec.Command(runnerBin, "/bin/echo", "hello-seccomp-tsync")
		runOut, err := cmd.CombinedOutput()
		require.NoError(t, err, "running standard command through seccomp runner: %s", string(runOut))
		assert.Contains(t, string(runOut), "hello-seccomp-tsync")
	})

	t.Run("DeniesMemfdCreateWithENOSYS", func(t *testing.T) {
		// Compile a probe binary that attempts memfd_create
		probeSrc := filepath.Join(tempDir, "probe.go")
		probeCode := `package main
import (
	"fmt"
	"os"
	"golang.org/x/sys/unix"
)
func main() {
	fd, err := unix.MemfdCreate("test_probe", unix.MFD_CLOEXEC)
	if err == nil {
		_ = unix.Close(fd)
		fmt.Println("MEMFD_SUCCEEDED")
		os.Exit(0)
	}
	if err == unix.ENOSYS {
		fmt.Println("MEMFD_DENIED_ENOSYS")
		os.Exit(42)
	}
	fmt.Printf("MEMFD_OTHER_ERROR: %v\n", err)
	os.Exit(1)
}
`
		require.NoError(t, os.WriteFile(probeSrc, []byte(probeCode), 0o644))
		probeBin := filepath.Join(tempDir, "probe-bin")
		pBuild := exec.Command("go", "build", "-o", probeBin, probeSrc)
		pBuild.Env = append(os.Environ(), "GOTOOLCHAIN=local")
		pOut, err := pBuild.CombinedOutput()
		require.NoError(t, err, "compiling probe: %s", string(pOut))

		// Run probe through seccomp-runner
		cmd := exec.Command(runnerBin, probeBin)
		probeOut, err := cmd.CombinedOutput()
		require.Error(t, err)

		// Exit code 42 indicates MEMFD_DENIED_ENOSYS
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr)
		assert.Equal(t, 42, exitErr.ExitCode(), "probe should exit with 42 (ENOSYS)")
		assert.True(t, strings.Contains(string(probeOut), "MEMFD_DENIED_ENOSYS"))
	})
}
