// Package main implements a test runner that executes child processes under a strict
// Linux BPF seccomp filter denying strictly and only SYS_memfd_create with ENOSYS.
package main

import (
	"os"
	"runtime"

	"github.com/EpicBlackWolfZ/microfat/internal/testutil/seccomp"
)

func init() {
	runtime.LockOSThread()
}

func main() {
	cleanArgs, adapter := seccomp.ResolveRunnerArgsAndAdapter(os.Args)
	exitCode := seccomp.RunRunner(cleanArgs, adapter, seccomp.DefaultExec, os.Stderr)
	os.Exit(exitCode)
}
