//go:build linux

package seccomp

import "syscall"

// SetMemfdCreateProbe overrides memfdCreateFn for testing and returns a restore function.
func SetMemfdCreateProbe(fn func(name string, flags int) (int, error)) func() {
	orig := memfdCreateFn
	memfdCreateFn = fn
	return func() { memfdCreateFn = orig }
}

// SetOpenDevNullProbe overrides openDevNullFn for testing and returns a restore function.
func SetOpenDevNullProbe(fn func() (int, error)) func() {
	orig := openDevNullFn
	openDevNullFn = fn
	return func() { openDevNullFn = orig }
}

// SetDefaultSyscallAdapter overrides defaultSyscallAdapterFn for testing and returns a restore function.
func SetDefaultSyscallAdapter(fn func() SyscallAdapter) func() {
	orig := defaultSyscallAdapterFn
	defaultSyscallAdapterFn = fn
	return func() { defaultSyscallAdapterFn = orig }
}

// ParseErrnoNameForTest exposes parseErrnoName for unit tests.
func ParseErrnoNameForTest(s string) syscall.Errno {
	return parseErrnoName(s)
}
