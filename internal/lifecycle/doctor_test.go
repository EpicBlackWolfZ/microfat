package lifecycle

import (
	"errors"
	"strings"
	"syscall"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/memfd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const summarySubNotReady = "NOT ready"

func TestEvaluatePrerequisites(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		cpuLevel     string
		memfdPassed  bool
		cachePassed  bool
		strict       bool
		warnings     []string
		expectReady  bool
		expectErrors int
		summarySub   string
	}{
		{
			name:         "Normal_AllPassed",
			cpuLevel:     "v3",
			memfdPassed:  true,
			cachePassed:  true,
			strict:       false,
			expectReady:  true,
			expectErrors: 0,
			summarySub:   "prerequisite checks passed, payload execution was not tested",
		},
		{
			name:         "Normal_AllPassed_WithWarnings",
			cpuLevel:     "v3",
			memfdPassed:  true,
			cachePassed:  true,
			strict:       false,
			warnings:     []string{"some warning"},
			expectReady:  true,
			expectErrors: 0,
			summarySub:   "passed with warnings, payload execution was not tested",
		},
		{
			name:         "Normal_MemfdOnly",
			cpuLevel:     "v3",
			memfdPassed:  true,
			cachePassed:  false,
			strict:       false,
			expectReady:  true,
			expectErrors: 0,
			summarySub:   "passed with warnings, payload execution was not tested",
		},
		{
			name:         "Normal_CacheOnly",
			cpuLevel:     "v3",
			memfdPassed:  false,
			cachePassed:  true,
			strict:       false,
			expectReady:  true,
			expectErrors: 0,
			summarySub:   "passed with warnings, payload execution was not tested",
		},
		{
			name:         "Normal_NeitherExecutionPath",
			cpuLevel:     "v3",
			memfdPassed:  false,
			cachePassed:  false,
			strict:       false,
			expectReady:  false,
			expectErrors: 1,
			summarySub:   summarySubNotReady,
		},
		{
			name:         "Normal_CPUUnsupported",
			cpuLevel:     "",
			memfdPassed:  true,
			cachePassed:  true,
			strict:       false,
			expectReady:  false,
			expectErrors: 1,
			summarySub:   summarySubNotReady,
		},
		{
			name:         "Strict_AllPassed",
			cpuLevel:     "v3",
			memfdPassed:  true,
			cachePassed:  true,
			strict:       true,
			expectReady:  true,
			expectErrors: 0,
			summarySub:   "prerequisite checks passed, payload execution was not tested",
		},
		{
			name:         "Strict_MemfdFailed",
			cpuLevel:     "v3",
			memfdPassed:  false,
			cachePassed:  true,
			strict:       true,
			expectReady:  false,
			expectErrors: 1,
			summarySub:   summarySubNotReady,
		},
		{
			name:         "Strict_CacheFailed",
			cpuLevel:     "v3",
			memfdPassed:  true,
			cachePassed:  false,
			strict:       true,
			expectReady:  false,
			expectErrors: 1,
			summarySub:   summarySubNotReady,
		},
		{
			name:         "Strict_BothFailed",
			cpuLevel:     "v3",
			memfdPassed:  false,
			cachePassed:  false,
			strict:       true,
			expectReady:  false,
			expectErrors: 2,
			summarySub:   summarySubNotReady,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			verdict := EvaluatePrerequisites(tt.cpuLevel, tt.memfdPassed, tt.cachePassed, tt.strict, tt.warnings)
			assert.Equal(t, tt.expectReady, verdict.Ready)
			assert.Len(t, verdict.Errors, tt.expectErrors)
			assert.True(t, strings.Contains(verdict.Summary, tt.summarySub), "summary %q should contain %q", verdict.Summary, tt.summarySub)
		})
	}
}

func TestResolveCandidateExplanations(t *testing.T) {
	t.Parallel()

	t.Run("nil error", func(t *testing.T) {
		t.Parallel()
		name, val, expls := ResolveCandidateExplanations(nil)
		assert.Empty(t, name)
		assert.Zero(t, val)
		assert.Nil(t, expls)
	})

	t.Run("EPERM", func(t *testing.T) {
		t.Parallel()
		name, val, expls := ResolveCandidateExplanations(unix.EPERM)
		assert.Equal(t, "EPERM", name)
		assert.Equal(t, int(unix.EPERM), val)
		require.Len(t, expls, 1)
		assert.Contains(t, expls[0], "Permission denied (possible cause: restricted security policy")
	})

	t.Run("ENOSYS", func(t *testing.T) {
		t.Parallel()
		name, val, expls := ResolveCandidateExplanations(unix.ENOSYS)
		assert.Equal(t, "ENOSYS", name)
		assert.Equal(t, int(unix.ENOSYS), val)
		require.Len(t, expls, 1)
		assert.Contains(t, expls[0], "Function not implemented (memfd_create system call not supported by this kernel)")
	})

	t.Run("EINVAL", func(t *testing.T) {
		t.Parallel()
		name, val, expls := ResolveCandidateExplanations(unix.EINVAL)
		assert.Equal(t, "EINVAL", name)
		assert.Equal(t, int(unix.EINVAL), val)
		require.Len(t, expls, 1)
		assert.Contains(t, expls[0], "Invalid argument (possible cause: unsupported flags for this kernel)")
	})

	t.Run("OtherErrno_EACCES", func(t *testing.T) {
		t.Parallel()
		name, val, expls := ResolveCandidateExplanations(unix.EACCES)
		assert.Equal(t, "EACCES", name)
		assert.Equal(t, int(unix.EACCES), val)
		require.Len(t, expls, 1)
		assert.Contains(t, expls[0], "EACCES")
	})

	t.Run("GenericError", func(t *testing.T) {
		t.Parallel()
		name, val, expls := ResolveCandidateExplanations(errors.New("custom failure"))
		assert.Empty(t, name)
		assert.Zero(t, val)
		require.Len(t, expls, 1)
		assert.Equal(t, "custom failure", expls[0])
	})
}

func TestCheckMemfdSupport_Mocked(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		closed := false
		adapter := &memfd.SyscallAdapter{
			MemfdCreate: func(name string, flags int) (int, error) { return 100, nil },
			Fstat: func(fd int, stat *unix.Stat_t) error {
				stat.Mode = unix.S_IFREG | 0o700
				return nil
			},
			FcntlInt: func(fd uintptr, cmd int, arg int) (int, error) {
				if cmd == unix.F_GET_SEALS {
					return memfd.TargetSeals, nil
				}
				return 0, nil
			},
			Close: func(fd int) error {
				closed = true
				return nil
			},
		}

		obs := CheckMemfdSupport(adapter)
		assert.True(t, obs.Available)
		assert.True(t, obs.Passed)
		assert.Empty(t, obs.Phase)
		assert.Empty(t, obs.Operation)
		assert.Equal(t, "Available", obs.Status)
		assert.Equal(t, "not_tested", obs.Execution)
		assert.NoError(t, obs.Cause)
		assert.True(t, closed, "descriptor must be closed cleanly")
	})

	t.Run("CreationFailure", func(t *testing.T) {
		adapter := &memfd.SyscallAdapter{
			MemfdCreate: func(name string, flags int) (int, error) { return -1, unix.EPERM },
		}

		obs := CheckMemfdSupport(adapter)
		assert.False(t, obs.Available)
		assert.False(t, obs.Passed)
		assert.Equal(t, "creation", obs.Phase)
		assert.Equal(t, "create", obs.Operation)
		assert.Equal(t, "not_tested", obs.Execution)
		assert.Equal(t, "EPERM", obs.ErrnoName)
		assert.Equal(t, int(unix.EPERM), obs.ErrnoValue)
		assert.ErrorIs(t, obs.Cause, unix.EPERM)
		assert.NotEmpty(t, obs.CandidateExplanations)
	})

	t.Run("FstatFailure", func(t *testing.T) {
		closed := false
		adapter := &memfd.SyscallAdapter{
			MemfdCreate: func(name string, flags int) (int, error) { return 101, nil },
			Fstat: func(fd int, stat *unix.Stat_t) error {
				return syscall.EBADF
			},
			Close: func(fd int) error {
				closed = true
				return nil
			},
		}

		obs := CheckMemfdSupport(adapter)
		assert.False(t, obs.Available)
		assert.False(t, obs.Passed)
		assert.Equal(t, "fstat", obs.Phase)
		assert.Equal(t, "fstat", obs.Operation)
		assert.Equal(t, "not_tested", obs.Execution)
		assert.Equal(t, "EBADF", obs.ErrnoName)
		assert.ErrorIs(t, obs.Cause, syscall.EBADF)
		assert.True(t, closed, "descriptor must be closed even when fstat fails")
	})

	t.Run("NonExecutableMode", func(t *testing.T) {
		closed := false
		adapter := &memfd.SyscallAdapter{
			MemfdCreate: func(name string, flags int) (int, error) { return 102, nil },
			Fstat: func(fd int, stat *unix.Stat_t) error {
				stat.Mode = unix.S_IFREG | 0o600 // non-executable
				return nil
			},
			Close: func(fd int) error {
				closed = true
				return nil
			},
		}

		obs := CheckMemfdSupport(adapter)
		assert.False(t, obs.Available)
		assert.False(t, obs.Passed)
		assert.Equal(t, "mode", obs.Phase)
		assert.Equal(t, "mode", obs.Operation)
		assert.Equal(t, "not_tested", obs.Execution)
		assert.Contains(t, obs.Status, "non-executable")
		assert.True(t, closed, "descriptor must be closed on non-executable mode")
	})

	t.Run("SealingFailure_F_ADD_SEALS", func(t *testing.T) {
		closed := false
		adapter := &memfd.SyscallAdapter{
			MemfdCreate: func(name string, flags int) (int, error) { return 103, nil },
			Fstat: func(fd int, stat *unix.Stat_t) error {
				stat.Mode = unix.S_IFREG | 0o700
				return nil
			},
			FcntlInt: func(fd uintptr, cmd int, arg int) (int, error) {
				if cmd == unix.F_ADD_SEALS {
					return -1, unix.EPERM
				}
				return 0, nil
			},
			Close: func(fd int) error {
				closed = true
				return nil
			},
		}

		obs := CheckMemfdSupport(adapter)
		assert.False(t, obs.Available)
		assert.False(t, obs.Passed)
		assert.Equal(t, "seals", obs.Phase)
		assert.Equal(t, "add_seals", obs.Operation)
		assert.Equal(t, "not_tested", obs.Execution)
		assert.Equal(t, "EPERM", obs.ErrnoName)
		assert.ErrorIs(t, obs.Cause, unix.EPERM)
		assert.Contains(t, obs.Status, "F_ADD_SEALS failed")
		assert.True(t, closed, "descriptor must be closed on sealing failure")
	})

	t.Run("SealingFailure_F_GET_SEALS", func(t *testing.T) {
		closed := false
		adapter := &memfd.SyscallAdapter{
			MemfdCreate: func(name string, flags int) (int, error) { return 104, nil },
			Fstat: func(fd int, stat *unix.Stat_t) error {
				stat.Mode = unix.S_IFREG | 0o700
				return nil
			},
			FcntlInt: func(fd uintptr, cmd int, arg int) (int, error) {
				if cmd == unix.F_GET_SEALS {
					return -1, syscall.EIO
				}
				return 0, nil
			},
			Close: func(fd int) error {
				closed = true
				return nil
			},
		}

		obs := CheckMemfdSupport(adapter)
		assert.False(t, obs.Available)
		assert.False(t, obs.Passed)
		assert.Equal(t, "seals", obs.Phase)
		assert.Equal(t, "get_seals", obs.Operation)
		assert.Equal(t, "not_tested", obs.Execution)
		assert.Equal(t, "EIO", obs.ErrnoName)
		assert.ErrorIs(t, obs.Cause, syscall.EIO)
		assert.Contains(t, obs.Status, "F_GET_SEALS failed")
		assert.True(t, closed, "descriptor must be closed on get seals failure")
	})

	t.Run("SealingFailure_MissingBitsInReadback", func(t *testing.T) {
		closed := false
		adapter := &memfd.SyscallAdapter{
			MemfdCreate: func(name string, flags int) (int, error) { return 105, nil },
			Fstat: func(fd int, stat *unix.Stat_t) error {
				stat.Mode = unix.S_IFREG | 0o700
				return nil
			},
			FcntlInt: func(fd uintptr, cmd int, arg int) (int, error) {
				if cmd == unix.F_GET_SEALS {
					return unix.F_SEAL_WRITE, nil // missing SHRINK, GROW, SEAL
				}
				return 0, nil
			},
			Close: func(fd int) error {
				closed = true
				return nil
			},
		}

		obs := CheckMemfdSupport(adapter)
		assert.False(t, obs.Available)
		assert.False(t, obs.Passed)
		assert.Equal(t, "seals", obs.Phase)
		assert.Equal(t, "get_seals", obs.Operation)
		assert.Equal(t, "not_tested", obs.Execution)
		assert.Contains(t, obs.Status, "mandatory seals missing in readback")
		assert.True(t, closed)
	})

	t.Run("AdapterWithoutClose", func(t *testing.T) {
		adapter := &memfd.SyscallAdapter{
			MemfdCreate: func(name string, flags int) (int, error) { return 106, nil },
			Fstat: func(fd int, stat *unix.Stat_t) error {
				stat.Mode = unix.S_IFREG | 0o700
				return nil
			},
			FcntlInt: func(fd uintptr, cmd int, arg int) (int, error) {
				if cmd == unix.F_GET_SEALS {
					return memfd.TargetSeals, nil
				}
				return 0, nil
			},
			Close: nil, // Must not close arbitrary host FD
		}

		obs := CheckMemfdSupport(adapter)
		assert.True(t, obs.Available)
		assert.True(t, obs.Passed)
	})

	t.Run("CriticalRegression_ModeErrorWithSuccessfulSeals", func(t *testing.T) {
		// DOC-1: creation success + mode observation error + successful seals + successful cache/CPU:
		// normal mode may pass through cache, strict mode must fail, and the mode error must appear in both report forms.
		closed := false
		adapter := &memfd.SyscallAdapter{
			MemfdCreate: func(name string, flags int) (int, error) { return 107, nil },
			Fstat: func(fd int, stat *unix.Stat_t) error {
				return syscall.EBADF
			},
			FcntlInt: func(fd uintptr, cmd int, arg int) (int, error) {
				if cmd == unix.F_GET_SEALS {
					return memfd.TargetSeals, nil
				}
				return 0, nil
			},
			Close: func(fd int) error {
				closed = true
				return nil
			},
		}

		obs := CheckMemfdSupport(adapter)
		assert.False(t, obs.Available, "must not pass available on mode error")
		assert.False(t, obs.Passed, "must not pass prerequisites on mode error")
		assert.Equal(t, "fstat", obs.Phase)
		assert.Equal(t, "EBADF", obs.ErrnoName)
		assert.NotNil(t, obs.Seals, "seals observation should still be preserved")
		assert.True(t, obs.Seals.Matches, "seals succeeded independently")
		assert.True(t, closed, "descriptor must be closed")

		// Prerequisite evaluation with successful cache and CPU
		normalVerdict := EvaluatePrerequisites("v3", obs.Passed, true, false, nil)
		assert.True(t, normalVerdict.Ready, "normal mode may pass through cache")
		assert.Len(t, normalVerdict.Warnings, 1)
		assert.Contains(t, normalVerdict.Warnings[0], "In-memory memfd_create prerequisites failed")

		strictVerdict := EvaluatePrerequisites("v3", obs.Passed, true, true, nil)
		assert.False(t, strictVerdict.Ready, "strict mode must fail when memfd prerequisites fail")
		assert.Len(t, strictVerdict.Errors, 1)
		assert.Contains(t, strictVerdict.Errors[0], "Strict mode requirement failed: in-memory memfd_create prerequisites failed")
	})

	t.Run("SealsReadbackMismatchWithoutError", func(t *testing.T) {
		adapter := &memfd.SyscallAdapter{
			MemfdCreate: func(name string, flags int) (int, error) { return 108, nil },
			Fstat: func(fd int, stat *unix.Stat_t) error {
				stat.Mode = unix.S_IFREG | 0o700
				return nil
			},
			FcntlInt: func(fd uintptr, cmd int, arg int) (int, error) {
				if cmd == unix.F_GET_SEALS {
					return 0, nil // no seals returned
				}
				return 0, nil
			},
			Close: func(fd int) error { return nil },
		}

		obs := CheckMemfdSupport(adapter)
		assert.False(t, obs.Available)
		assert.False(t, obs.Passed)
		assert.Equal(t, "seals", obs.Phase)
		assert.Contains(t, obs.Status, "mandatory seals missing in readback")
	})
}

func TestResolveCandidateExplanations_Nil(t *testing.T) {
	t.Parallel()

	name, val, exp := ResolveCandidateExplanations(nil)
	assert.Empty(t, name)
	assert.Equal(t, 0, val)
	assert.Nil(t, exp)

	name, val, exp = ResolveCandidateExplanations(syscall.Errno(9999))
	assert.Equal(t, "ERRNO_9999", name)
	assert.Equal(t, 9999, val)
	assert.Len(t, exp, 1)
}
