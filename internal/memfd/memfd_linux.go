//go:build linux

package memfd

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const (
	// LegacyFlags defines the fallback flag combination supported on kernels prior to MFD_EXEC (Linux < 6.3).
	LegacyFlags = unix.MFD_CLOEXEC | unix.MFD_ALLOW_SEALING

	// PreferredFlags defines the primary flag combination requesting explicit execution permission.
	PreferredFlags = LegacyFlags | unix.MFD_EXEC

	// TargetSeals defines mandatory Linux kernel memory file descriptor seals applied to
	// anonymous RAM payloads prior to execution via /proc/self/fd/<fd>:
	// - F_SEAL_WRITE: prevents modification of decompressed binary code in memory.
	// - F_SEAL_SHRINK & F_SEAL_GROW: prevents truncation or expansion of the memory region.
	// - F_SEAL_SEAL: permanently locks the seal set.
	TargetSeals = unix.F_SEAL_WRITE | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_SEAL

	// executePermissionMask is the bitmask for any execute permission (owner, group, or other).
	executePermissionMask = 0o111
)

// SyscallAdapter provides injectable syscall functions for testing and isolation.
// If any function field is nil, the default system syscall is invoked.
type SyscallAdapter struct {
	MemfdCreate func(name string, flags int) (int, error)
	FcntlInt    func(fd uintptr, cmd int, arg int) (int, error)
	Fstat       func(fd int, stat *unix.Stat_t) error
	Close       func(fd int) error
}

func (a *SyscallAdapter) memfdCreate(name string, flags int) (int, error) {
	if a != nil && a.MemfdCreate != nil {
		return a.MemfdCreate(name, flags)
	}
	return unix.MemfdCreate(name, flags)
}

func (a *SyscallAdapter) fcntlInt(fd uintptr, cmd int, arg int) (int, error) {
	if a != nil && a.FcntlInt != nil {
		return a.FcntlInt(fd, cmd, arg)
	}
	return unix.FcntlInt(fd, cmd, arg)
}

func (a *SyscallAdapter) fstat(fd int, stat *unix.Stat_t) error {
	if a != nil && a.Fstat != nil {
		return a.Fstat(fd, stat)
	}
	return unix.Fstat(fd, stat)
}

func (a *SyscallAdapter) closeFD(fd int) error {
	if a != nil && a.Close != nil {
		return a.Close(fd)
	}
	return unix.Close(fd)
}

// CreateExecutable attempts to create an anonymous executable memory file descriptor using
// MFD_CLOEXEC | MFD_ALLOW_SEALING | MFD_EXEC. If the kernel returns EINVAL (indicating an
// unrecognized MFD_EXEC flag on older kernels or configurations), it retries at most once
// with LegacyFlags (MFD_CLOEXEC | MFD_ALLOW_SEALING).
//
// Policy denials (EPERM, EACCES), lack of syscall support (ENOSYS), resource exhaustion (EMFILE),
// or any other non-EINVAL errors are never retried.
//
// On success, the caller owns exactly one file descriptor in CreateResult.FD.
// On any error, any created descriptor is closed before returning.
func CreateExecutable(name string, adapter *SyscallAdapter) (CreateResult, error) {
	res := CreateResult{FD: -1}

	fd, err := adapter.memfdCreate(name, PreferredFlags)
	if err == nil {
		res.FD = fd
		res.Flags = PreferredFlags
		res.ExplicitExecSucceeded = true
		return res, nil
	}

	// Clean up if a descriptor was unexpectedly returned alongside an error
	if fd >= 0 {
		_ = adapter.closeFD(fd)
	}

	// Only retry on EINVAL. Permission, policy, resource, or missing syscall errors are never retried.
	if errors.Is(err, unix.EINVAL) {
		res.LegacyRetryUsed = true
		res.FirstErr = err

		retryFD, retryErr := adapter.memfdCreate(name, LegacyFlags)
		if retryErr != nil {
			if retryFD >= 0 {
				_ = adapter.closeFD(retryFD)
			}
			return res, retryErr
		}
		res.FD = retryFD
		res.Flags = LegacyFlags
		res.ExplicitExecSucceeded = false
		return res, nil
	}

	res.FirstErr = err
	return res, err
}

// ApplyTargetSeals applies TargetSeals to the open descriptor fd.
func ApplyTargetSeals(fd int, adapter *SyscallAdapter) error {
	_, err := adapter.fcntlInt(uintptr(fd), unix.F_ADD_SEALS, TargetSeals)
	return err
}

// GetSeals returns the current fcntl seal mask on fd.
func GetSeals(fd int, adapter *SyscallAdapter) (int, error) {
	return adapter.fcntlInt(uintptr(fd), unix.F_GET_SEALS, 0)
}

// CheckExecutableMode inspects the file mode on fd using Fstat and reports if
// executable permission bits are present.
func CheckExecutableMode(fd int, adapter *SyscallAdapter) (ModeObservation, error) {
	var stat unix.Stat_t
	if err := adapter.fstat(fd, &stat); err != nil {
		return ModeObservation{}, err
	}
	mode := os.FileMode(stat.Mode)
	isExec := (stat.Mode & executePermissionMask) != 0
	return ModeObservation{
		Mode:         mode,
		ModeOctal:    fmt.Sprintf("%04o", mode.Perm()),
		IsExecutable: isExec,
	}, nil
}

// VerifySeals applies the mandatory TargetSeals to fd and verifies via F_GET_SEALS
// that the resulting mask contains all required seal bits.
func VerifySeals(fd int, adapter *SyscallAdapter) (SealsObservation, error) {
	if err := ApplyTargetSeals(fd, adapter); err != nil {
		return SealsObservation{
			TargetMask: TargetSeals,
			Supported:  false,
			Error:      err.Error(),
		}, fmt.Errorf("applying target seals: %w", err)
	}
	seals, err := GetSeals(fd, adapter)
	if err != nil {
		return SealsObservation{
			TargetMask: TargetSeals,
			Supported:  false,
			Error:      err.Error(),
		}, fmt.Errorf("getting seals: %w", err)
	}
	matches := (seals & TargetSeals) == TargetSeals
	return SealsObservation{
		TargetMask:   TargetSeals,
		ObservedMask: seals,
		Matches:      matches,
		Supported:    true,
	}, nil
}
