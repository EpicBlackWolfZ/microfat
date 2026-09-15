//go:build linux

// Package seccomp provides test utilities for Linux BPF seccomp filter installation
// and TSYNC classification.
package seccomp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Exit codes for seccomp runner orchestration.
const (
	ExitCodeUsageError      = 64
	ExitCodeSelfTestFailure = 99
	ExitCodeExecFailure     = 127
)

// BPF instruction and opcode constants.
const (
	BPFInstructionCount = 4
	BPFOpLoadSyscall    = 0x20 // BPF_LD | BPF_W | BPF_ABS
	BPFOpJumpEqual      = 0x15 // BPF_JMP | BPF_JEQ | BPF_K
	BPFOpReturn         = 0x06 // BPF_RET | BPF_K
	ErrnoMask           = 0xFFFF
)

// Test simulation environment variables and CLI flag prefixes.
const (
	EnvSimulateTsyncTID      = "MICROFAT_TEST_SIMULATE_TSYNC_TID"
	EnvSimulateSeccompErrno  = "MICROFAT_TEST_SIMULATE_SECCOMP_ERRNO"
	EnvSimulateFallbackErrno = "MICROFAT_TEST_SIMULATE_FALLBACK_ERRNO"
	EnvSimulateFallbackTID   = "MICROFAT_TEST_SIMULATE_FALLBACK_TID"
	EnvSimulateNoNewPrivsErr = "MICROFAT_TEST_SIMULATE_NO_NEW_PRIVS_ERR"

	FlagSimulateTsyncTID      = "--simulate-tsync-tid="
	FlagSimulateSeccompErrno  = "--simulate-seccomp-errno="
	FlagSimulateFallbackErrno = "--simulate-fallback-errno="
	FlagSimulateFallbackTID   = "--simulate-fallback-tid="
	FlagSimulateNoNewPrivsErr = "--simulate-no-new-privs-err"
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

// SyscallAdapter allows injecting raw syscall operations for testing.
type SyscallAdapter struct {
	RawSyscall func(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err syscall.Errno)
	Prctl      func(option int, arg2, arg3, arg4, arg5 uintptr) error
}

// DefaultSyscallAdapter returns the standard production syscall adapter backed by real kernel calls.
func DefaultSyscallAdapter() SyscallAdapter {
	return SyscallAdapter{
		RawSyscall: syscall.RawSyscall,
		Prctl:      unix.Prctl,
	}
}

// ExecFn replaces the current process image.
type ExecFn func(argv0 string, argv []string, envv []string) error

// DefaultExec is the standard syscall.Exec implementation.
var DefaultExec ExecFn = syscall.Exec

var (
	memfdCreateFn           = unix.MemfdCreate
	openDevNullFn           = func() (int, error) { return unix.Open("/dev/null", unix.O_RDONLY|unix.O_CLOEXEC, 0) }
	defaultSyscallAdapterFn = DefaultSyscallAdapter
)

// RunStandardSyscallCheck verifies standard syscalls execute unhindered.
func RunStandardSyscallCheck() error {
	pid := unix.Getpid()
	if pid <= 0 {
		return fmt.Errorf("unexpected pid %d", pid)
	}
	fd, err := openDevNullFn()
	if err != nil {
		return fmt.Errorf("open /dev/null: %w", err)
	}
	return unix.Close(fd)
}

// VerifySingleInjectedFailureInvariant verifies strictly SYS_memfd_create fails with ENOSYS
// while standard syscalls continue to execute unhindered.
func VerifySingleInjectedFailureInvariant() error {
	if err := RunStandardSyscallCheck(); err != nil {
		return fmt.Errorf("standard syscall failed under filter: %w", err)
	}

	fd, err := memfdCreateFn("runner_invariant_probe", unix.MFD_CLOEXEC)
	if err == nil {
		_ = unix.Close(fd)
		return errors.New("memfd_create unexpectedly succeeded when filter is installed")
	}
	if !errors.Is(err, unix.ENOSYS) {
		return fmt.Errorf("expected ENOSYS error on memfd_create, got: %w", err)
	}

	return nil
}

// InstallStrictMemfdDenialFilterWithAdapter installs a strict seccomp BPF filter denying only SYS_memfd_create.
func InstallStrictMemfdDenialFilterWithAdapter(adapter SyscallAdapter) error {
	prctlFn := adapter.Prctl
	if prctlFn == nil {
		prctlFn = unix.Prctl
	}
	rawSyscallFn := adapter.RawSyscall
	if rawSyscallFn == nil {
		rawSyscallFn = syscall.RawSyscall
	}

	if err := prctlFn(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("prctl PR_SET_NO_NEW_PRIVS: %w", err)
	}

	filter := [BPFInstructionCount]unix.SockFilter{
		{Code: BPFOpLoadSyscall, Jt: 0, Jf: 0, K: 0},
		{Code: BPFOpJumpEqual, Jt: 0, Jf: 1, K: uint32(unix.SYS_MEMFD_CREATE)},
		{Code: BPFOpReturn, Jt: 0, Jf: 0, K: uint32(unix.SECCOMP_RET_ERRNO | (unix.ENOSYS & ErrnoMask))},
		{Code: BPFOpReturn, Jt: 0, Jf: 0, K: uint32(unix.SECCOMP_RET_ALLOW)},
	}

	prog := unix.SockFprog{
		Len:    BPFInstructionCount,
		Filter: &filter[0],
	}

	// #nosec G103 -- SYS_SECCOMP requires pointer to sock_fprog structure
	bpfProgPtr := uintptr(unsafe.Pointer(&prog))

	r1, _, errno := rawSyscallFn(
		unix.SYS_SECCOMP,
		uintptr(unix.SECCOMP_SET_MODE_FILTER),
		uintptr(unix.SECCOMP_FILTER_FLAG_TSYNC),
		bpfProgPtr,
	)
	action, err := ClassifySeccompResult(r1, errno)
	switch action {
	case SeccompActionSuccess:
		return nil
	case SeccompActionThreadError:
		return err
	case SeccompActionFallbackPermitted:
		// #nosec G103 -- SYS_PRCTL fallback requires pointer to sock_fprog structure
		fallbackProgPtr := uintptr(unsafe.Pointer(&prog))
		r1Fallback, _, errnoFallback := rawSyscallFn(
			syscall.SYS_PRCTL,
			uintptr(unix.PR_SET_SECCOMP),
			uintptr(unix.SECCOMP_MODE_FILTER),
			fallbackProgPtr,
		)
		if errnoFallback != 0 || r1Fallback != 0 {
			if errnoFallback != 0 {
				return fmt.Errorf("prctl seccomp fallback failed: %w", errnoFallback)
			}
			return fmt.Errorf("prctl seccomp fallback failed on thread %d", r1Fallback)
		}
		return nil
	default:
		return err
	}
}

// InstallStrictMemfdDenialFilter installs the filter using default production syscalls.
func InstallStrictMemfdDenialFilter() error {
	return InstallStrictMemfdDenialFilterWithAdapter(defaultSyscallAdapterFn())
}

// SimulationConfig holds parameters to simulate kernel behavior in tests.
type SimulationConfig struct {
	SimulateTsyncTID      uintptr
	SimulateSeccompErrno  syscall.Errno
	SimulateFallbackErrno syscall.Errno
	SimulateFallbackTID   uintptr
	SimulateNoNewPrivsErr bool
}

func parseErrnoName(s string) syscall.Errno {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ENOSYS":
		return unix.ENOSYS
	case "EINVAL":
		return unix.EINVAL
	case "EPERM":
		return unix.EPERM
	case "EACCES":
		return unix.EACCES
	case "ENOENT":
		return unix.ENOENT
	default:
		val, err := strconv.ParseUint(s, 10, 32)
		if err == nil {
			return syscall.Errno(val)
		}
		return 0
	}
}

// BuildSimulatedSyscallAdapter creates a SyscallAdapter configured according to SimulationConfig.
func BuildSimulatedSyscallAdapter(cfg SimulationConfig) SyscallAdapter {
	adapter := DefaultSyscallAdapter()

	if cfg.SimulateNoNewPrivsErr {
		adapter.Prctl = func(option int, arg2, arg3, arg4, arg5 uintptr) error {
			if option == unix.PR_SET_NO_NEW_PRIVS {
				return unix.EPERM
			}
			return unix.Prctl(option, arg2, arg3, arg4, arg5)
		}
	}

	adapter.RawSyscall = func(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err syscall.Errno) {
		if trap == unix.SYS_SECCOMP {
			if cfg.SimulateTsyncTID != 0 {
				return cfg.SimulateTsyncTID, 0, 0
			}
			if cfg.SimulateSeccompErrno != 0 {
				return 0, 0, cfg.SimulateSeccompErrno
			}
			if cfg.SimulateFallbackErrno != 0 || cfg.SimulateFallbackTID != 0 {
				return 0, 0, unix.ENOSYS
			}
		}
		if trap == syscall.SYS_PRCTL && a1 == uintptr(unix.PR_SET_SECCOMP) {
			if cfg.SimulateFallbackTID != 0 {
				return cfg.SimulateFallbackTID, 0, 0
			}
			if cfg.SimulateFallbackErrno != 0 {
				return 0, 0, cfg.SimulateFallbackErrno
			}
		}
		return syscall.RawSyscall(trap, a1, a2, a3)
	}

	return adapter
}

// ResolveRunnerArgsAndAdapter extracts test simulation flags/env and returns clean args and adapter.
func ResolveRunnerArgsAndAdapter(args []string) ([]string, SyscallAdapter) {
	var cfg SimulationConfig
	hasSimulation := false

	if envVal := os.Getenv(EnvSimulateTsyncTID); envVal != "" {
		if tid, err := strconv.ParseUint(envVal, 10, 64); err == nil {
			cfg.SimulateTsyncTID = uintptr(tid)
			hasSimulation = true
		}
	}
	if envVal := os.Getenv(EnvSimulateSeccompErrno); envVal != "" {
		cfg.SimulateSeccompErrno = parseErrnoName(envVal)
		hasSimulation = true
	}
	if envVal := os.Getenv(EnvSimulateFallbackErrno); envVal != "" {
		cfg.SimulateFallbackErrno = parseErrnoName(envVal)
		hasSimulation = true
	}
	if envVal := os.Getenv(EnvSimulateFallbackTID); envVal != "" {
		if tid, err := strconv.ParseUint(envVal, 10, 64); err == nil {
			cfg.SimulateFallbackTID = uintptr(tid)
			hasSimulation = true
		}
	}
	if os.Getenv(EnvSimulateNoNewPrivsErr) != "" {
		cfg.SimulateNoNewPrivsErr = true
		hasSimulation = true
	}

	var cleanArgs []string
	cleanArgs = append(cleanArgs, args[0])

	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case strings.HasPrefix(arg, FlagSimulateTsyncTID):
			valStr := strings.TrimPrefix(arg, FlagSimulateTsyncTID)
			if tid, err := strconv.ParseUint(valStr, 10, 64); err == nil {
				cfg.SimulateTsyncTID = uintptr(tid)
				hasSimulation = true
			}
		case strings.HasPrefix(arg, FlagSimulateSeccompErrno):
			valStr := strings.TrimPrefix(arg, FlagSimulateSeccompErrno)
			cfg.SimulateSeccompErrno = parseErrnoName(valStr)
			hasSimulation = true
		case strings.HasPrefix(arg, FlagSimulateFallbackErrno):
			valStr := strings.TrimPrefix(arg, FlagSimulateFallbackErrno)
			cfg.SimulateFallbackErrno = parseErrnoName(valStr)
			hasSimulation = true
		case strings.HasPrefix(arg, FlagSimulateFallbackTID):
			valStr := strings.TrimPrefix(arg, FlagSimulateFallbackTID)
			if tid, err := strconv.ParseUint(valStr, 10, 64); err == nil {
				cfg.SimulateFallbackTID = uintptr(tid)
				hasSimulation = true
			}
		case arg == FlagSimulateNoNewPrivsErr:
			cfg.SimulateNoNewPrivsErr = true
			hasSimulation = true
		default:
			cleanArgs = append(cleanArgs, arg)
		}
	}

	if hasSimulation {
		return cleanArgs, BuildSimulatedSyscallAdapter(cfg)
	}
	return cleanArgs, DefaultSyscallAdapter()
}

// RunRunner runs the full runner orchestration workflow.
// If any check or installation step fails, it prints to stderr and returns the failure exit code
// without invoking execFn.
func RunRunner(args []string, adapter SyscallAdapter, execFn ExecFn, stderr io.Writer) int {
	const minRunnerArgs = 2
	if len(args) < minRunnerArgs {
		_, _ = fmt.Fprintf(stderr, "usage: %s <executable> [args...]\n", args[0])
		return ExitCodeUsageError
	}

	targetExe := args[1]
	targetArgs := args[1:]

	// Pre-filter self-test: verify standard syscalls execute normally
	if err := RunStandardSyscallCheck(); err != nil {
		_, _ = fmt.Fprintf(stderr, "seccomp-runner: pre-filter self-test failed: %v\n", err)
		return ExitCodeSelfTestFailure
	}

	// Install strict seccomp BPF filter
	if err := InstallStrictMemfdDenialFilterWithAdapter(adapter); err != nil {
		_, _ = fmt.Fprintf(stderr, "seccomp-runner: filter installation failed: %v\n", err)
		return ExitCodeSelfTestFailure
	}

	// Post-filter self-test: verify strictly SYS_memfd_create fails with ENOSYS
	// while standard syscalls continue to execute unhindered
	if err := VerifySingleInjectedFailureInvariant(); err != nil {
		_, _ = fmt.Fprintf(stderr, "seccomp-runner: single injected failure invariant failed: %v\n", err)
		return ExitCodeSelfTestFailure
	}

	// Replace process image with target executable
	if execFn == nil {
		execFn = DefaultExec
	}
	execErr := execFn(targetExe, targetArgs, os.Environ())
	if execErr != nil {
		_, _ = fmt.Fprintf(stderr, "seccomp-runner: execve %s failed: %v\n", targetExe, execErr)
		return ExitCodeExecFailure
	}
	return 0
}
