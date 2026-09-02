// Package main implements a test runner that executes child processes under a strict
// Linux BPF seccomp filter denying strictly and only SYS_memfd_create with ENOSYS.
package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	bpfInstructionCount     = 4
	bpfOpLoadSyscall        = 0x20 // BPF_LD | BPF_W | BPF_ABS
	bpfOpJumpEqual          = 0x15 // BPF_JMP | BPF_JEQ | BPF_K
	bpfOpReturn             = 0x06 // BPF_RET | BPF_K
	errnoMask               = 0xFFFF
	exitCodeUsageError      = 64
	exitCodeSelfTestFailure = 99
	exitCodeExecFailure     = 127
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <executable> [args...]\n", os.Args[0])
		os.Exit(exitCodeUsageError)
	}

	targetExe := os.Args[1]
	targetArgs := os.Args[1:]

	// Pre-filter self-test: verify standard syscalls execute normally
	if err := runStandardSyscallCheck(); err != nil {
		fmt.Fprintf(os.Stderr, "seccomp-runner: pre-filter self-test failed: %v\n", err)
		os.Exit(exitCodeSelfTestFailure)
	}

	// Install strict seccomp BPF filter
	if err := installStrictMemfdDenialFilter(); err != nil {
		fmt.Fprintf(os.Stderr, "seccomp-runner: filter installation failed: %v\n", err)
		os.Exit(exitCodeSelfTestFailure)
	}

	// Post-filter self-test: verify strictly SYS_memfd_create fails with ENOSYS
	// while standard syscalls continue to execute unhindered
	if err := verifySingleInjectedFailureInvariant(); err != nil {
		fmt.Fprintf(os.Stderr, "seccomp-runner: single injected failure invariant failed: %v\n", err)
		os.Exit(exitCodeSelfTestFailure)
	}

	// Replace process image with target executable
	execErr := syscall.Exec(targetExe, targetArgs, os.Environ())
	if execErr != nil {
		fmt.Fprintf(os.Stderr, "seccomp-runner: execve %s failed: %v\n", targetExe, execErr)
		os.Exit(exitCodeExecFailure)
	}
}

func runStandardSyscallCheck() error {
	pid := unix.Getpid()
	if pid <= 0 {
		return fmt.Errorf("unexpected pid %d", pid)
	}
	fd, err := unix.Open("/dev/null", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open /dev/null: %w", err)
	}
	return unix.Close(fd)
}

func installStrictMemfdDenialFilter() error {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("prctl PR_SET_NO_NEW_PRIVS: %w", err)
	}

	// Strict BPF filter:
	// [0] Load syscall number from offset 0 of seccomp_data
	// [1] If syscall == SYS_memfd_create, jump to [2], else jump to [3]
	// [2] Return SECCOMP_RET_ERRNO | ENOSYS
	// [3] Return SECCOMP_RET_ALLOW (unconditionally allows all other syscalls)
	filter := [bpfInstructionCount]unix.SockFilter{
		{Code: bpfOpLoadSyscall, Jt: 0, Jf: 0, K: 0},
		{Code: bpfOpJumpEqual, Jt: 0, Jf: 1, K: uint32(unix.SYS_MEMFD_CREATE)},
		{Code: bpfOpReturn, Jt: 0, Jf: 0, K: uint32(unix.SECCOMP_RET_ERRNO | (uint32(unix.ENOSYS) & errnoMask))},
		{Code: bpfOpReturn, Jt: 0, Jf: 0, K: uint32(unix.SECCOMP_RET_ALLOW)},
	}

	prog := unix.SockFprog{
		Len:    bpfInstructionCount,
		Filter: &filter[0],
	}

	_, _, errno := syscall.RawSyscall(
		syscall.SYS_PRCTL,
		uintptr(unix.PR_SET_SECCOMP),
		uintptr(unix.SECCOMP_MODE_FILTER),
		uintptr(unsafe.Pointer(&prog)),
	)
	if errno != 0 {
		return fmt.Errorf("prctl PR_SET_SECCOMP: %w", errno)
	}

	return nil
}

func verifySingleInjectedFailureInvariant() error {
	// Standard syscalls MUST continue to succeed unhindered
	if err := runStandardSyscallCheck(); err != nil {
		return fmt.Errorf("standard syscall failed under filter: %w", err)
	}

	// Strictly SYS_memfd_create MUST fail with ENOSYS
	fd, err := unix.MemfdCreate("runner_invariant_probe", unix.MFD_CLOEXEC)
	if err == nil {
		_ = unix.Close(fd)
		return errors.New("memfd_create unexpectedly succeeded when filter is installed")
	}
	if !errors.Is(err, unix.ENOSYS) {
		return fmt.Errorf("expected ENOSYS error on memfd_create, got: %w", err)
	}

	return nil
}
