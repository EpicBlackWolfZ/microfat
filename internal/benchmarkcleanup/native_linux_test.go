//go:build linux

package benchmarkcleanup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	nativeProofEnvironment = "MICROFAT_BENCH_CLEANUP_PROOF"
	nativeHelperMode       = "MICROFAT_NATIVE_CLEANUP_HELPER"
	nativeHelperGroup      = "MICROFAT_NATIVE_CLEANUP_GROUP"
	nativeHelperMarker     = "MICROFAT_NATIVE_CLEANUP_MARKER"
	nativeGraceful         = "graceful"
	nativeForced           = "forced"
	nativeController       = "controller"
	nativeOutside          = "outside"
	nativeProofTimeout     = 15 * time.Second
	nativeHelperLifetime   = time.Minute
	nativePollTimeout      = 1000
	nativeDirectoryMode    = 0o755
	nativeMarkerMode       = 0o600
)

type nativeChild struct {
	command *exec.Cmd
	pidfd   int
	waited  bool
}

type nativeOrphan struct {
	pid    int
	pidfd  int
	reaped bool
}

// TestNativeCleanupHelper is a subprocess entry point, never a privileged test fixture itself.
func TestNativeCleanupHelper(t *testing.T) {
	mode := os.Getenv(nativeHelperMode)
	if mode == "" {
		return
	}
	group := os.Getenv(nativeHelperGroup)
	if group != "" {
		procs := filepath.Join(group, "cgroup.procs")
		require.NoError(t, os.WriteFile(procs, []byte(strconv.Itoa(os.Getpid())), nativeMarkerMode))
	}
	if mode == nativeController {
		nativeRunController(t, group)
		return
	}
	if mode == nativeForced {
		signal.Ignore(syscall.SIGTERM)
		require.NoError(t, nativeWriteReady(os.Getpid()))
		time.Sleep(nativeHelperLifetime)
		os.Exit(0)
	}
	require.Contains(t, []string{nativeGraceful, nativeOutside}, mode)
	terminated := make(chan os.Signal, 1)
	signal.Notify(terminated, syscall.SIGTERM)
	require.NoError(t, nativeWriteReady(os.Getpid()))
	select {
	case <-terminated:
		if marker := os.Getenv(nativeHelperMarker); marker != "" {
			require.NoError(t, os.WriteFile(marker, []byte("SIGTERM\n"), nativeMarkerMode))
		}
	case <-time.After(nativeHelperLifetime):
		t.Fatal("fixture worker exceeded its lifetime")
	}
	os.Exit(0)
}

func nativeWriteReady(pids ...int) error {
	values := make([]string, len(pids))
	for i, pid := range pids {
		values[i] = strconv.Itoa(pid)
	}
	_, err := fmt.Fprintln(os.Stdout, "ready", strings.Join(values, " "))
	return err
}

func nativeRunController(t *testing.T, manager string) {
	t.Helper()
	root := filepath.Dir(manager)
	mode := os.Getenv("MICROFAT_NATIVE_CLEANUP_WORKER")
	markers := os.Getenv(nativeHelperMarker)
	managerChild, _ := nativeStartChild(t, mode, manager, filepath.Join(markers, "manager"))
	workloadChild, _ := nativeStartChild(t, mode, filepath.Join(root, "microfat-bench-1"), filepath.Join(markers, "workload"))
	require.NoError(t, nativeWriteReady(managerChild.command.Process.Pid, workloadChild.command.Process.Pid))
	time.Sleep(nativeHelperLifetime)
	t.Fatal("fixture controller was not terminated")
}

func nativeStartChild(t *testing.T, mode, group, marker string) (*nativeChild, []int) {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestNativeCleanupHelper$")
	command.Env = append(os.Environ(), nativeHelperMode+"="+mode,
		nativeHelperGroup+"="+group, nativeHelperMarker+"="+marker)
	command.Stderr = os.Stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := command.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, command.Start())
	pidfd, err := unix.PidfdOpen(command.Process.Pid, 0)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
	}
	require.NoError(t, err)
	child := &nativeChild{command: command, pidfd: pidfd}
	t.Cleanup(func() {
		if !child.waited {
			_ = unix.PidfdSendSignal(child.pidfd, unix.SIGKILL, nil, 0)
			_ = child.command.Wait()
		}
		require.NoError(t, unix.Close(child.pidfd))
	})
	type response struct {
		line string
		err  error
	}
	ready := make(chan response, 1)
	go func() {
		line, readErr := bufio.NewReader(stdout).ReadString('\n')
		ready <- response{line, readErr}
	}()
	var line string
	select {
	case result := <-ready:
		require.NoError(t, result.err)
		line = result.line
	case <-time.After(nativeProofTimeout):
		t.Fatal("fixture helper did not become ready")
	}
	fields := strings.Fields(line)
	require.GreaterOrEqual(t, len(fields), 2)
	require.Equal(t, "ready", fields[0])
	pids := make([]int, len(fields)-1)
	for i, field := range fields[1:] {
		pids[i], err = strconv.Atoi(field)
		require.NoError(t, err)
		require.Positive(t, pids[i])
	}
	return child, pids
}

func nativeReapOrphan(t *testing.T, child *nativeOrphan) unix.WaitStatus {
	t.Helper()
	var status unix.WaitStatus
	deadline := time.Now().Add(nativeProofTimeout)
	for {
		pid, err := unix.Wait4(child.pid, &status, unix.WNOHANG, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		require.NoError(t, err)
		if pid == child.pid {
			child.reaped = true
			return status
		}
		require.True(t, time.Now().Before(deadline), "orphan %d was not reaped", child.pid)
		time.Sleep(pollPeriod)
	}
}

func nativeOpenOrphan(t *testing.T, pid int) *nativeOrphan {
	t.Helper()
	pidfd, err := unix.PidfdOpen(pid, 0)
	require.NoError(t, err)
	child := &nativeOrphan{pid: pid, pidfd: pidfd}
	t.Cleanup(func() {
		if !child.reaped {
			_ = unix.PidfdSendSignal(child.pidfd, unix.SIGKILL, nil, 0)
			nativeReapOrphan(t, child)
		}
		require.NoError(t, unix.Close(child.pidfd))
	})
	return child
}

func nativeAssertAlive(t *testing.T, fd int) {
	t.Helper()
	ready, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 0)
	require.NoError(t, err)
	require.Zero(t, ready, "outside process was terminated by delegation cleanup")
}

func nativeAssertExited(t *testing.T, fd int) {
	t.Helper()
	ready, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, nativePollTimeout)
	require.NoError(t, err)
	require.Equal(t, 1, ready, "owned process survived delegation cleanup")
}

func nativeAssertMembership(t *testing.T, pid int, group string) {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	require.NoError(t, err)
	require.Contains(t, strings.Split(string(data), "\n"), "0::"+strings.TrimPrefix(group, cgroupDirectory))
}

func nativeCreateGroup(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.Mkdir(path, nativeDirectoryMode))
	t.Cleanup(func() {
		err := os.Remove(path)
		if !errors.Is(err, os.ErrNotExist) {
			assert.NoError(t, err)
		}
	})
}

// TestNativeCleanup runs only as a separate root test process outside benchmark-enter.
// Successful kernel rmdir proves the subtree was unpopulated; every owned pidfd is also checked explicitly.
func TestNativeCleanup(t *testing.T) {
	if os.Getenv(nativeProofEnvironment) == "" {
		t.Skip("native cleanup proof requires its separate privileged workflow step")
	}
	require.Equal(t, "required", os.Getenv(nativeProofEnvironment))
	require.Zero(t, os.Geteuid(), "native cleanup proof must run as root outside the delegation")
	runID, attempt := os.Getenv("GITHUB_RUN_ID"), os.Getenv("GITHUB_RUN_ATTEMPT")
	require.NotEmpty(t, runID)
	require.NotEmpty(t, attempt)
	var filesystem unix.Statfs_t
	require.NoError(t, unix.Statfs(cgroupDirectory, &filesystem))
	require.Equal(t, int64(unix.CGROUP2_SUPER_MAGIC), filesystem.Type)
	membership, err := os.ReadFile("/proc/self/cgroup")
	require.NoError(t, err)
	require.NotContains(t, string(membership), "/microfat-ci-", "native cleanup proof must run outside benchmark-enter")
	var original int32
	require.NoError(t, unix.Prctl(unix.PR_GET_CHILD_SUBREAPER, uintptr(unsafe.Pointer(&original)), 0, 0, 0))
	require.NoError(t, unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0))
	t.Cleanup(func() { require.NoError(t, unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, uintptr(original), 0, 0, 0)) })

	outside, _ := nativeStartChild(t, nativeOutside, "", "")
	sibling, _ := nativeStartChild(t, nativeOutside, "", "")
	siblingRoot := filepath.Join(cgroupDirectory, fmt.Sprintf("microfat-ci-%s-%s-%d", runID, attempt, sibling.command.Process.Pid))
	nativeCreateGroup(t, siblingRoot)
	siblingManager := filepath.Join(siblingRoot, "manager")
	nativeCreateGroup(t, siblingManager)
	require.NoError(t, os.WriteFile(filepath.Join(siblingManager, "cgroup.procs"),
		[]byte(strconv.Itoa(sibling.command.Process.Pid)), nativeMarkerMode))
	nativeAssertMembership(t, sibling.command.Process.Pid, siblingManager)
	// Register child cleanup after cgroup cleanup registration, so the sibling exits before its groups are removed.
	t.Cleanup(func() {
		_ = unix.PidfdSendSignal(sibling.pidfd, unix.SIGKILL, nil, 0)
		_ = sibling.command.Wait()
		sibling.waited = true
	})

	for _, mode := range []string{"empty", nativeGraceful, nativeForced} {
		t.Run(mode, func(t *testing.T) {
			root := filepath.Join(cgroupDirectory, fmt.Sprintf("microfat-ci-%s-%s-%d", runID, attempt, os.Getpid()))
			nativeCreateGroup(t, root)
			manager := filepath.Join(root, "manager")
			nativeCreateGroup(t, manager)
			workload := filepath.Join(root, "microfat-bench-1")
			nativeCreateGroup(t, workload)
			require.NoError(t, Register(root, runID, attempt, 0))
			t.Cleanup(func() {
				if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
					ctx, cancel := context.WithTimeout(context.Background(), nativeProofTimeout)
					defer cancel()
					assert.NoError(t, Cleanup(ctx, root, runID, attempt))
				}
			})
			var orphans []*nativeOrphan
			markers := t.TempDir()
			if mode != "empty" {
				t.Setenv("MICROFAT_NATIVE_CLEANUP_WORKER", mode)
				controller, pids := nativeStartChild(t, nativeController, manager, markers)
				require.Len(t, pids, 2)
				for _, pid := range pids {
					orphans = append(orphans, nativeOpenOrphan(t, pid))
				}
				// On an assertion failure, reap the controller before the adopted workers' cleanups run.
				t.Cleanup(func() {
					if !controller.waited {
						_ = unix.PidfdSendSignal(controller.pidfd, unix.SIGKILL, nil, 0)
						_ = controller.command.Wait()
						controller.waited = true
					}
				})
				nativeAssertMembership(t, pids[0], manager)
				nativeAssertMembership(t, pids[1], workload)
				require.NoError(t, unix.PidfdSendSignal(controller.pidfd, unix.SIGKILL, nil, 0))
				require.Error(t, controller.command.Wait())
				controller.waited = true
				for _, child := range orphans {
					nativeAssertAlive(t, child.pidfd)
				}
			}
			data, err := os.ReadFile(filepath.Join(root, "cgroup.events"))
			require.NoError(t, err)
			populated := "populated 1"
			if mode == "empty" {
				populated = "populated 0"
			}
			require.Contains(t, strings.Split(string(data), "\n"), populated)
			ctx, cancel := context.WithTimeout(t.Context(), nativeProofTimeout)
			defer cancel()
			require.NoError(t, Cleanup(ctx, root, runID, attempt))
			for _, child := range orphans {
				nativeAssertExited(t, child.pidfd)
				status := nativeReapOrphan(t, child)
				if mode == nativeForced {
					require.True(t, status.Signaled())
					require.Equal(t, syscall.SIGKILL, status.Signal())
				} else {
					require.True(t, status.Exited())
					require.Zero(t, status.ExitStatus())
				}
			}
			for _, marker := range []string{"manager", "workload"} {
				data, err := os.ReadFile(filepath.Join(markers, marker))
				if mode == nativeGraceful {
					require.NoError(t, err)
					require.Equal(t, "SIGTERM\n", string(data))
				} else {
					require.ErrorIs(t, err, os.ErrNotExist)
				}
			}
			for _, path := range []string{manager, workload, root, filepath.Join(receiptDirectory, filepath.Base(root)+".json")} {
				_, err := os.Stat(path)
				require.ErrorIs(t, err, os.ErrNotExist)
			}
			require.Error(t, Cleanup(ctx, root, runID, attempt), "removed delegations require a fresh ownership receipt")
			nativeAssertAlive(t, outside.pidfd)
			nativeAssertAlive(t, sibling.pidfd)
			nativeAssertMembership(t, sibling.command.Process.Pid, siblingManager)
			_, err = os.Stat(siblingManager)
			require.NoError(t, err, "sibling sentinel group was removed")
			t.Logf("native cleanup mode=%s: owned subtree removed, outside pid=%d and sibling pid=%d survived",
				mode, outside.command.Process.Pid, sibling.command.Process.Pid)
		})
	}
}
