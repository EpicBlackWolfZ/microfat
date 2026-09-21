package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommand(t *testing.T) {
	t.Parallel()
	require.ErrorContains(t, run(nil), "usage:")
	require.Error(t, run([]string{t.TempDir()}), "lock is absent from the package test directory")
	require.Error(t, verifySignature(filepath.Join(t.TempDir(), "missing")), "missing or unauthenticated metadata must fail")
}

func TestMain(t *testing.T) {
	if os.Getenv("MICROFAT_KERNEL_LOCK_HELPER") == "1" {
		os.Args = []string{"benchmark-kernel-lock"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMain$")
	cmd.Env = append(os.Environ(), "MICROFAT_KERNEL_LOCK_HELPER=1")
	output, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "usage: benchmark-kernel-lock")
}
