//go:build linux

package cgroup

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestRetainedExecutableMemorySeals(t *testing.T) {
	t.Parallel()
	fd, err := unix.MemfdCreate("memory-budget-test", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	require.NoError(t, err)
	f := os.NewFile(uintptr(fd), "memory-budget-test")
	defer f.Close()
	const size = 100
	require.NoError(t, f.Truncate(size))
	got, err := retainedMemoryForFile(f)
	require.NoError(t, err)
	require.Zero(t, got)
	const seals = unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	_, err = unix.FcntlInt(f.Fd(), unix.F_ADD_SEALS, seals)
	require.NoError(t, err)
	got, err = retainedMemoryForFile(f)
	require.NoError(t, err)
	require.EqualValues(t, os.Getpagesize(), got)
	require.NoError(t, f.Close())
	_, err = retainedMemoryForFile(f)
	require.Error(t, err)
	got, err = RetainedExecutableMemory()
	require.NoError(t, err)
	require.Zero(t, got, "unit test executable is a regular disk file")
}
