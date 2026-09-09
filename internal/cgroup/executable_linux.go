//go:build linux

package cgroup

import (
	"errors"
	"math"
	"os"

	"golang.org/x/sys/unix"
)

// RetainedExecutableMemory reads kernel state, never inherited MICROFAT metadata.
// Only sealed memory-backed executable files require an unreclaimable storage deduction.
func RetainedExecutableMemory() (int64, error) {
	f, err := os.Open("/proc/self/exe")
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	return retainedMemoryForFile(f)
}

func retainedMemoryForFile(f *os.File) (int64, error) {
	seals, err := unix.FcntlInt(f.Fd(), unix.F_GET_SEALS, 0)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTTY) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	const required = unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	if seals&required != required {
		return 0, nil
	}
	stat, err := f.Stat()
	if err != nil {
		return 0, err
	}
	page := int64(os.Getpagesize())
	if stat.Size() < 0 || stat.Size() > math.MaxInt64-page+1 {
		return 0, ErrMemoryBudget
	}
	return (stat.Size() + page - 1) / page * page, nil
}
