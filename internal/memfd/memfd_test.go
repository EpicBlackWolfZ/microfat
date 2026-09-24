//go:build linux

package memfd

import (
	"errors"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	testMockValidFD  = 100
	testMockLegacyFD = 101
)

func TestCreateExecutable_SuccessPreferred(t *testing.T) {
	t.Parallel()

	calls := 0
	adapter := &SyscallAdapter{
		MemfdCreate: func(name string, flags int) (int, error) {
			calls++
			assert.Equal(t, "test_name", name)
			assert.Equal(t, PreferredFlags, flags)
			return testMockValidFD, nil
		},
	}

	res, err := CreateExecutable("test_name", adapter)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Equal(t, testMockValidFD, res.FD)
	assert.Equal(t, PreferredFlags, res.Flags)
	assert.True(t, res.ExplicitExecSucceeded)
	assert.False(t, res.LegacyRetryUsed)
	assert.NoError(t, res.FirstErr)
}

func TestCreateExecutable_EINVAL_LegacyRetrySuccess(t *testing.T) {
	t.Parallel()

	calls := 0
	adapter := &SyscallAdapter{
		MemfdCreate: func(name string, flags int) (int, error) {
			calls++
			if calls == 1 {
				assert.Equal(t, PreferredFlags, flags)
				return -1, unix.EINVAL
			}
			assert.Equal(t, LegacyFlags, flags)
			return testMockLegacyFD, nil
		},
	}

	res, err := CreateExecutable("test_name", adapter)
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	assert.Equal(t, testMockLegacyFD, res.FD)
	assert.Equal(t, LegacyFlags, res.Flags)
	assert.False(t, res.ExplicitExecSucceeded)
	assert.True(t, res.LegacyRetryUsed)
	require.ErrorIs(t, res.FirstErr, unix.EINVAL)
}

func TestCreateExecutable_EINVAL_LegacyRetryFailure(t *testing.T) {
	t.Parallel()

	calls := 0
	closed := 0
	adapter := &SyscallAdapter{
		MemfdCreate: func(name string, flags int) (int, error) {
			calls++
			if calls == 1 {
				return -1, unix.EINVAL
			}
			return testMockLegacyFD, unix.EPERM
		},
		Close: func(fd int) error {
			closed++
			assert.Equal(t, testMockLegacyFD, fd)
			return nil
		},
	}

	res, err := CreateExecutable("test_name", adapter)
	require.ErrorIs(t, err, unix.EPERM)
	assert.Equal(t, 2, calls)
	assert.Equal(t, 1, closed, "descriptor returned on failed retry must be closed")
	assert.Equal(t, -1, res.FD)
	assert.True(t, res.LegacyRetryUsed)
	require.ErrorIs(t, res.FirstErr, unix.EINVAL)
}

func TestCreateExecutable_WrappedEINVAL_Retries(t *testing.T) {
	t.Parallel()

	calls := 0
	adapter := &SyscallAdapter{
		MemfdCreate: func(name string, flags int) (int, error) {
			calls++
			if calls == 1 {
				return -1, errors.Join(unix.EINVAL, errors.New("unsupported flag"))
			}
			return testMockLegacyFD, nil
		},
	}

	res, err := CreateExecutable("test_name", adapter)
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	assert.Equal(t, testMockLegacyFD, res.FD)
	assert.True(t, res.LegacyRetryUsed)
}

func TestCreateExecutable_NonRetryableErrors(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		err  error
	}{
		{name: "EPERM policy denial", err: unix.EPERM},
		{name: "EACCES permission denial", err: unix.EACCES},
		{name: "ENOSYS syscall missing", err: unix.ENOSYS},
		{name: "EMFILE process fd exhaustion", err: unix.EMFILE},
		{name: "ENFILE system fd exhaustion", err: unix.ENFILE},
		{name: "generic unexpected error", err: errors.New("custom error")},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			closed := 0
			adapter := &SyscallAdapter{
				MemfdCreate: func(name string, flags int) (int, error) {
					calls++
					return testMockValidFD, tc.err
				},
				Close: func(fd int) error {
					closed++
					assert.Equal(t, testMockValidFD, fd)
					return nil
				},
			}

			res, err := CreateExecutable("test_name", adapter)
			require.ErrorIs(t, err, tc.err)
			assert.Equal(t, 1, calls, "non-EINVAL errors must not be retried")
			assert.Equal(t, 1, closed, "stray descriptor returned with error must be cleaned up")
			assert.Equal(t, -1, res.FD)
			assert.False(t, res.LegacyRetryUsed)
			assert.False(t, res.ExplicitExecSucceeded)
		})
	}
}

func TestApplyTargetSeals_And_GetSeals(t *testing.T) {
	t.Parallel()

	t.Run("successful seal application and query", func(t *testing.T) {
		currentSeals := 0
		adapter := &SyscallAdapter{
			FcntlInt: func(fd uintptr, cmd int, arg int) (int, error) {
				assert.Equal(t, uintptr(testMockValidFD), fd)
				switch cmd {
				case unix.F_ADD_SEALS:
					currentSeals |= arg
					return 0, nil
				case unix.F_GET_SEALS:
					return currentSeals, nil
				default:
					return -1, unix.EINVAL
				}
			},
		}

		err := ApplyTargetSeals(testMockValidFD, adapter)
		require.NoError(t, err)

		seals, err := GetSeals(testMockValidFD, adapter)
		require.NoError(t, err)
		assert.Equal(t, TargetSeals, seals)
	})

	t.Run("fcntl seal application error", func(t *testing.T) {
		adapter := &SyscallAdapter{
			FcntlInt: func(fd uintptr, cmd int, arg int) (int, error) {
				return -1, unix.EPERM
			},
		}

		err := ApplyTargetSeals(testMockValidFD, adapter)
		require.ErrorIs(t, err, unix.EPERM)
	})
}

func TestVerifySeals(t *testing.T) {
	t.Parallel()

	t.Run("matches required seals", func(t *testing.T) {
		adapter := &SyscallAdapter{
			FcntlInt: func(fd uintptr, cmd int, arg int) (int, error) {
				if cmd == unix.F_ADD_SEALS {
					return 0, nil
				}
				if cmd == unix.F_GET_SEALS {
					return TargetSeals, nil
				}
				return -1, unix.EINVAL
			},
		}

		obs, err := VerifySeals(testMockValidFD, adapter)
		require.NoError(t, err)
		assert.True(t, obs.Matches)
		assert.Equal(t, TargetSeals, obs.TargetMask)
		assert.Equal(t, TargetSeals, obs.ObservedMask)
	})

	t.Run("missing required seals", func(t *testing.T) {
		adapter := &SyscallAdapter{
			FcntlInt: func(fd uintptr, cmd int, arg int) (int, error) {
				if cmd == unix.F_ADD_SEALS {
					return 0, nil
				}
				if cmd == unix.F_GET_SEALS {
					// Missing F_SEAL_SEAL
					return unix.F_SEAL_WRITE | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW, nil
				}
				return -1, unix.EINVAL
			},
		}

		obs, err := VerifySeals(testMockValidFD, adapter)
		require.NoError(t, err)
		assert.False(t, obs.Matches)
	})

	t.Run("apply seals failure", func(t *testing.T) {
		adapter := &SyscallAdapter{
			FcntlInt: func(fd uintptr, cmd int, arg int) (int, error) {
				if cmd == unix.F_ADD_SEALS {
					return -1, unix.EPERM
				}
				return 0, nil
			},
		}

		obs, err := VerifySeals(testMockValidFD, adapter)
		require.Error(t, err)
		assert.False(t, obs.Matches)
	})

	t.Run("get seals failure", func(t *testing.T) {
		adapter := &SyscallAdapter{
			FcntlInt: func(fd uintptr, cmd int, arg int) (int, error) {
				if cmd == unix.F_ADD_SEALS {
					return 0, nil
				}
				if cmd == unix.F_GET_SEALS {
					return -1, unix.EBADF
				}
				return -1, unix.EINVAL
			},
		}

		obs, err := VerifySeals(testMockValidFD, adapter)
		require.Error(t, err)
		assert.False(t, obs.Matches)
	})
}

func TestCheckExecutableMode(t *testing.T) {
	t.Parallel()

	t.Run("executable mode 0777", func(t *testing.T) {
		adapter := &SyscallAdapter{
			Fstat: func(fd int, stat *unix.Stat_t) error {
				stat.Mode = syscall.S_IFREG | 0o777
				return nil
			},
		}

		obs, err := CheckExecutableMode(testMockValidFD, adapter)
		require.NoError(t, err)
		assert.True(t, obs.IsExecutable)
	})

	t.Run("non-executable mode 0666", func(t *testing.T) {
		adapter := &SyscallAdapter{
			Fstat: func(fd int, stat *unix.Stat_t) error {
				stat.Mode = syscall.S_IFREG | 0o666
				return nil
			},
		}

		obs, err := CheckExecutableMode(testMockValidFD, adapter)
		require.NoError(t, err)
		assert.False(t, obs.IsExecutable)
	})

	t.Run("fstat failure", func(t *testing.T) {
		adapter := &SyscallAdapter{
			Fstat: func(fd int, stat *unix.Stat_t) error {
				return unix.EBADF
			},
		}

		obs, err := CheckExecutableMode(testMockValidFD, adapter)
		require.ErrorIs(t, err, unix.EBADF)
		assert.False(t, obs.IsExecutable)
	})
}

func TestRealSystemMemfd(t *testing.T) {
	t.Parallel()

	res, err := CreateExecutable("microfat_test_real", nil)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
		t.Skipf("skipping live memfd test on host with restricted environment: %v", err)
	}
	require.NoError(t, err)
	defer func() {
		_ = unix.Close(res.FD)
	}()

	assert.GreaterOrEqual(t, res.FD, 0)

	modeObs, err := CheckExecutableMode(res.FD, nil)
	require.NoError(t, err)
	assert.True(t, modeObs.IsExecutable, "created memfd must be executable")

	sealsObs, err := VerifySeals(res.FD, nil)
	require.NoError(t, err)
	assert.True(t, sealsObs.Matches, "target seals must match observed seals")
}
