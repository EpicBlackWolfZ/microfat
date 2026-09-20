//go:build linux

package main

import (
	"errors"

	"golang.org/x/sys/unix"
)

func createExecutableMemfd() (int, error) {
	const legacyFlags = unix.MFD_CLOEXEC | unix.MFD_ALLOW_SEALING
	fd, err := memfdCreateFunc("microfat_payload", legacyFlags|unix.MFD_EXEC)
	// The constant name is short and the legacy flags are valid on older kernels,
	// so EINVAL indicates an unrecognized MFD_EXEC flag. Permission/policy errors
	// are never retried. Mandatory sealing and exec checks still apply after retry.
	if errors.Is(err, unix.EINVAL) {
		return memfdCreateFunc("microfat_payload", legacyFlags)
	}
	return fd, err
}
