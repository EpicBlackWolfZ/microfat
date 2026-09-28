//go:build linux

package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	var descriptors []int
	for _, name := range files {
		descriptors = append(descriptors, observeMountProcess(t, filepath.Join(req.Root, "cache", name)))
	}
	require.ErrorIs(t, <-result, context.DeadlineExceeded)
	for _, fd := range descriptors {
		require.ErrorIs(t, unix.PidfdSendSignal(fd, 0, nil, 0), unix.ESRCH,
			"timed-out fixture process must be terminated and reaped, including detached descendants")
	}
}

func observeMountProcess(t *testing.T, path string) int {
	t.Helper()
	fd := -1
	deadline := time.Now().Add(mountObserveTimeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		var process mountfixture.Process
		if err == nil && json.Unmarshal(data, &process) == nil && process.PID > 1 && process.Namespace != "" {
			fd = mountProcessFD(process)
			if fd >= 0 {
				break
			}
		}
		time.Sleep(mountObserveInterval)
	}
	require.GreaterOrEqual(t, fd, 0, "fixture process must be observable before cancellation")
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
	if err != nil || namespace != process.Namespace {
		return false
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
