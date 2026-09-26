//go:build !linux

package memfd

const (
	// LegacyFlags is 0 on non-Linux platforms.
	LegacyFlags = 0

	// PreferredFlags is 0 on non-Linux platforms.
	PreferredFlags = 0

	// TargetSeals is 0 on non-Linux platforms.
	TargetSeals = 0
)

// SyscallAdapter provides stubbed syscall functions on non-Linux platforms.
type SyscallAdapter struct {
	MemfdCreate func(name string, flags int) (int, error)
	FcntlInt    func(fd uintptr, cmd int, arg int) (int, error)
	Fstat       func(fd int, stat any) error
	Close       func(fd int) error
}

// CreateExecutable returns ErrUnsupported on non-Linux platforms.
func CreateExecutable(name string, adapter *SyscallAdapter) (CreateResult, error) {
	return CreateResult{FD: -1}, ErrUnsupported
}

// ApplyTargetSeals returns ErrUnsupported on non-Linux platforms.
func ApplyTargetSeals(fd int, adapter *SyscallAdapter) error {
	return ErrUnsupported
}

// GetSeals returns ErrUnsupported on non-Linux platforms.
func GetSeals(fd int, adapter *SyscallAdapter) (int, error) {
	return 0, ErrUnsupported
}

// CheckExecutableMode returns ErrUnsupported on non-Linux platforms.
func CheckExecutableMode(fd int, adapter *SyscallAdapter) (ModeObservation, error) {
	return ModeObservation{}, ErrUnsupported
}

// VerifySeals returns ErrUnsupported on non-Linux platforms.
func VerifySeals(fd int, adapter *SyscallAdapter) (SealsObservation, error) {
	return SealsObservation{}, ErrUnsupported
}
