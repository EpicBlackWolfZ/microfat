// Package e2e contains end-to-end and integration tests for microfat binaries and runtime dispatch.
package e2e

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// SeccompAction indicates how to handle the result of SYS_SECCOMP.
type SeccompAction int

const (
	// SeccompActionSuccess indicates the filter was installed and synchronized across all threads.
	SeccompActionSuccess SeccompAction = iota
	// SeccompActionThreadError indicates synchronization failed on a specific thread ID.
	SeccompActionThreadError
	// SeccompActionFallbackPermitted indicates SYS_SECCOMP is unsupported and prctl fallback is permitted.
	SeccompActionFallbackPermitted
	// SeccompActionHardError indicates an unrecoverable permission or parameter error.
	SeccompActionHardError
)

// ErrSeccompTSYNCFailed indicates synchronization failed on a specific thread.
var ErrSeccompTSYNCFailed = errors.New("seccomp TSYNC failed")

// ClassifySeccompResult evaluates the return value and errno of SYS_SECCOMP with TSYNC.
func ClassifySeccompResult(r1 uintptr, errno syscall.Errno) (SeccompAction, error) {
	if errno == 0 {
		if r1 == 0 {
			return SeccompActionSuccess, nil
		}
		return SeccompActionThreadError, fmt.Errorf("%w on thread %d", ErrSeccompTSYNCFailed, r1)
	}
	if errno == unix.ENOSYS || errno == unix.EINVAL {
		return SeccompActionFallbackPermitted, fmt.Errorf("seccomp TSYNC unsupported (%w), fallback permitted", errno)
	}
	return SeccompActionHardError, fmt.Errorf("seccomp filter installation failed: %w", errno)
}
