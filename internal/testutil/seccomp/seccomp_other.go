//go:build !linux

package seccomp

import (
	"errors"
	"io"
	"syscall"
)

// Exit codes for seccomp runner orchestration.
const (
	ExitCodeUsageError      = 64
	ExitCodeSelfTestFailure = 99
	ExitCodeExecFailure     = 127
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

// ErrNotSupportedOnOS indicates seccomp is not supported on non-linux OS.
var ErrNotSupportedOnOS = errors.New("seccomp is only supported on linux")

// SyscallAdapter allows injecting raw syscall operations for testing.
type SyscallAdapter struct {
	RawSyscall func(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err syscall.Errno)
	Prctl      func(option int, arg2, arg3, arg4, arg5 uintptr) error
}

// DefaultSyscallAdapter returns an empty adapter on non-linux systems.
func DefaultSyscallAdapter() SyscallAdapter {
	return SyscallAdapter{}
}

// ExecFn replaces the current process image.
type ExecFn func(argv0 string, argv []string, envv []string) error

// DefaultExec is nil on non-linux systems.
var DefaultExec ExecFn

// ClassifySeccompResult evaluates the return value and errno on non-linux systems.
func ClassifySeccompResult(r1 uintptr, errno syscall.Errno) (SeccompAction, error) {
	if errno == 0 {
		if r1 == 0 {
			return SeccompActionSuccess, nil
		}
		return SeccompActionThreadError, ErrSeccompTSYNCFailed
	}
	return SeccompActionHardError, ErrNotSupportedOnOS
}

// InstallStrictMemfdDenialFilter is not supported on non-linux OS.
func InstallStrictMemfdDenialFilter() error {
	return ErrNotSupportedOnOS
}

// InstallStrictMemfdDenialFilterWithAdapter is not supported on non-linux OS.
func InstallStrictMemfdDenialFilterWithAdapter(_ SyscallAdapter) error {
	return ErrNotSupportedOnOS
}

// RunStandardSyscallCheck is not supported on non-linux OS.
func RunStandardSyscallCheck() error {
	return ErrNotSupportedOnOS
}

// VerifySingleInjectedFailureInvariant is not supported on non-linux OS.
func VerifySingleInjectedFailureInvariant() error {
	return ErrNotSupportedOnOS
}

// ResolveRunnerArgsAndAdapter returns unmodified args and empty adapter on non-linux systems.
func ResolveRunnerArgsAndAdapter(args []string) ([]string, SyscallAdapter) {
	return args, DefaultSyscallAdapter()
}

// RunRunner returns ExitCodeSelfTestFailure on non-linux OS.
func RunRunner(_ []string, _ SyscallAdapter, _ ExecFn, _ io.Writer) int {
	return ExitCodeSelfTestFailure
}
