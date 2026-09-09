//go:build unix

package report

import (
	"golang.org/x/sys/unix"
	"os"
)

func openEvidence(path string) (*os.File, error) {
	// Nonblocking open allows descriptor validation to reject substituted FIFOs promptly.
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
