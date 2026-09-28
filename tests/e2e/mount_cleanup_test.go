//go:build linux

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	mountCleanupTimeout  = 5 * time.Second
	mountObserveTimeout  = 4 * time.Second
	mountObserveInterval = 20 * time.Millisecond
)

func assertMountTimeoutCleanup(t *testing.T, backend string, p mountProducts, mode string) {
	t.Helper()
	req := prepareMountRequest(t, p, p.reporter, "directory", execModeNative)
	req.Args = []string{mode}
	assertFixtureTimeoutCleanup(t, req, mountNamespaceCommand(backend, p.controller, filepath.Join(req.Root, "request.json")))
}

func assertFixtureTimeoutCleanup(t *testing.T, req mountfixture.Request, args []string) {
	t.Helper()
	request := filepath.Join(req.Root, "request.json")
	writeMountJSON(t, request, req)
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _, err := invokeMountArgs(args, mountCleanupTimeout)
		result <- err
	}()
	t.Cleanup(func() { <-done })
	files := []string{"pid.json"}
	if req.Args[0] == "--spawn-detached" {
		files = append(files, "descendant.json")
	}
	var token string
	if req.Binfmt != nil && len(req.Args) > 1 {
		token = req.Args[1]
	}
	var descriptors []int
	for _, name := range files {
		descriptors = append(descriptors, observeMountProcess(t, filepath.Join(req.Root, "cache", name), token))
	}
	require.ErrorIs(t, <-result, context.DeadlineExceeded)
	for _, fd := range descriptors {
		require.ErrorIs(t, unix.PidfdSendSignal(fd, 0, nil, 0), unix.ESRCH,
			"timed-out fixture process must be terminated and reaped, including detached descendants")
	}
}

func observeMountProcess(t *testing.T, path, token string) int {
	t.Helper()
	fd := -1
	var observation string
	deadline := time.Now().Add(mountObserveTimeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		observation = string(data)
		var process mountfixture.Process
		if err == nil && json.Unmarshal(data, &process) == nil && process.PID > 1 && process.Namespace != "" && process.Token == token {
			fd = mountProcessFD(process)
			if fd >= 0 {
				break
			}
		}
		time.Sleep(mountObserveInterval)
	}
	require.GreaterOrEqual(t, fd, 0, "fixture process must be observable before cancellation at %s: %s", path, observation)
	t.Logf("cleanup process identity %s: %s", filepath.Base(path), observation)
	// A pidfd cannot accidentally signal a reused host PID. Retain this fallback
	// even when namespace teardown regresses, so the regression itself stays safe.
	t.Cleanup(func() {
		_ = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
		require.NoError(t, unix.Close(fd))
	})
	return fd
}

func mountProcessFD(process mountfixture.Process) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return -1
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		path := filepath.Join("/proc", entry.Name())
		if !mountProcessMatches(path, process) {
			continue
		}
		fd, err := unix.PidfdOpen(pid, 0)
		if err != nil {
			continue
		}
		if mountProcessMatches(path, process) {
			return fd
		}
		_ = unix.Close(fd)
	}
	return -1
}

func mountProcessMatches(path string, process mountfixture.Process) bool {
	namespace, err := os.Readlink(filepath.Join(path, "ns/pid"))
	if err != nil {
		// An ordinary controller cannot inspect ns links in a root-owned userns.
		// Only QEMU cleanup fixtures supply a random argv token as an independent
		// identity, paired with NSpid and checked again after opening the pidfd.
		if !errors.Is(err, os.ErrPermission) || process.Token == "" {
			return false
		}
	} else if namespace != process.Namespace {
		return false
	}
	if process.Token != "" {
		data, err := os.ReadFile(filepath.Join(path, "cmdline"))
		if err != nil || !slices.Contains(strings.Split(string(data), "\x00"), process.Token) {
			return false
		}
	}
	data, err := os.ReadFile(filepath.Join(path, "status"))
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "NSpid:") {
			fields := strings.Fields(line)
			return fields[len(fields)-1] == strconv.Itoa(process.PID)
		}
	}
	return false
}

func TestMountProcessIdentity(t *testing.T) {
	const processNamespace = "pid:[fixture]"
	t.Parallel()
	for _, tc := range []struct {
		name, namespace, token, pid string
		hidden, match               bool
	}{
		{"native", processNamespace, "", "2", false, true},
		{"wrong-namespace", "pid:[other]", "", "2", false, false},
		{"wrong-pid", processNamespace, "", "3", false, false},
		{"token", processNamespace, "unique-fixture", "2", false, true},
		{"wrong-token", processNamespace, "other-token", "2", false, false},
		{"hidden-native", processNamespace, "", "2", true, false},
		{"hidden-token", processNamespace, "unique-fixture", "2", true, true},
		{"hidden-wrong-token", processNamespace, "other-token", "2", true, false},
		{"hidden-wrong-pid", processNamespace, "unique-fixture", "3", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.hidden && os.Geteuid() == 0 {
				t.Skip("permission refusal requires ordinary credentials")
			}
			root := t.TempDir()
			ns := filepath.Join(root, "ns")
			require.NoError(t, os.Mkdir(ns, privateDirPerm))
			require.NoError(t, os.Symlink(tc.namespace, filepath.Join(ns, "pid")))
			require.NoError(t, os.WriteFile(filepath.Join(root, "cmdline"), []byte("reporter\x00unique-fixture\x00"), privateFilePerm))
			require.NoError(t, os.WriteFile(filepath.Join(root, "status"), []byte("NSpid:\t100\t"+tc.pid+"\n"), privateFilePerm))
			if tc.hidden {
				require.NoError(t, os.Chmod(ns, 0))
				t.Cleanup(func() { require.NoError(t, os.Chmod(ns, privateDirPerm)) })
			}
			process := mountfixture.Process{PID: 2, Namespace: processNamespace, Token: tc.token}
			require.Equal(t, tc.match, mountProcessMatches(root, process))
		})
	}
}
