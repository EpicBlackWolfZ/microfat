//go:build linux

package seccomp_test

import (
	"bytes"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/EpicBlackWolfZ/microfat/internal/testutil/seccomp"
)

const (
	testTID         uintptr = 1337
	testFallbackTID uintptr = 42
	testRunnerBin           = "runner"
	testEchoBin             = "/bin/echo"
	testTrueBin             = "/bin/true"
)

func TestClassifySeccompResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		r1         uintptr
		errno      syscall.Errno
		wantAction seccomp.SeccompAction
		wantErrStr string
	}{
		{
			name:       "Success_ZeroReturnZeroErrno",
			r1:         0,
			errno:      0,
			wantAction: seccomp.SeccompActionSuccess,
		},
		{
			name:       "ThreadError_NonZeroTIDZeroErrno",
			r1:         testTID,
			errno:      0,
			wantAction: seccomp.SeccompActionThreadError,
			wantErrStr: "seccomp TSYNC failed on thread 1337",
		},
		{
			name:       "FallbackPermitted_ENOSYS",
			r1:         0,
			errno:      unix.ENOSYS,
			wantAction: seccomp.SeccompActionFallbackPermitted,
			wantErrStr: "fallback permitted",
		},
		{
			name:       "FallbackPermitted_EINVAL",
			r1:         0,
			errno:      unix.EINVAL,
			wantAction: seccomp.SeccompActionFallbackPermitted,
			wantErrStr: "fallback permitted",
		},
		{
			name:       "HardError_EPERM",
			r1:         0,
			errno:      unix.EPERM,
			wantAction: seccomp.SeccompActionHardError,
			wantErrStr: "operation not permitted",
		},
		{
			name:       "HardError_EACCES",
			r1:         0,
			errno:      unix.EACCES,
			wantAction: seccomp.SeccompActionHardError,
			wantErrStr: "permission denied",
		},
		{
			name:       "HardError_UnexpectedErrno",
			r1:         0,
			errno:      unix.ENOENT,
			wantAction: seccomp.SeccompActionHardError,
			wantErrStr: "no such file or directory",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			action, err := seccomp.ClassifySeccompResult(tc.r1, tc.errno)
			assert.Equal(t, tc.wantAction, action)
			if tc.wantErrStr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrStr)
				if tc.wantAction == seccomp.SeccompActionThreadError {
					assert.ErrorIs(t, err, seccomp.ErrSeccompTSYNCFailed)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestInstallStrictMemfdDenialFilterWithAdapter(t *testing.T) {
	t.Parallel()

	t.Run("PrctlNoNewPrivsFailure", func(t *testing.T) {
		t.Parallel()
		adapter := seccomp.SyscallAdapter{
			Prctl: func(option int, _, _, _, _ uintptr) error {
				if option == unix.PR_SET_NO_NEW_PRIVS {
					return unix.EPERM
				}
				return nil
			},
			RawSyscall: func(_, _, _, _ uintptr) (uintptr, uintptr, syscall.Errno) {
				return 0, 0, 0
			},
		}
		err := seccomp.InstallStrictMemfdDenialFilterWithAdapter(adapter)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "prctl PR_SET_NO_NEW_PRIVS")
	})

	t.Run("SeccompSuccess", func(t *testing.T) {
		t.Parallel()
		adapter := seccomp.SyscallAdapter{
			Prctl: func(_ int, _, _, _, _ uintptr) error { return nil },
			RawSyscall: func(trap, _, _, _ uintptr) (uintptr, uintptr, syscall.Errno) {
				if trap == unix.SYS_SECCOMP {
					return 0, 0, 0
				}
				return 0, 0, unix.EINVAL
			},
		}
		err := seccomp.InstallStrictMemfdDenialFilterWithAdapter(adapter)
		require.NoError(t, err)
	})

	t.Run("SeccompPositiveTIDThreadError", func(t *testing.T) {
		t.Parallel()
		fallbackCalled := false
		adapter := seccomp.SyscallAdapter{
			Prctl: func(_ int, _, _, _, _ uintptr) error { return nil },
			RawSyscall: func(trap, _, _, _ uintptr) (uintptr, uintptr, syscall.Errno) {
				if trap == unix.SYS_SECCOMP {
					return testTID, 0, 0
				}
				if trap == syscall.SYS_PRCTL {
					fallbackCalled = true
				}
				return 0, 0, 0
			},
		}
		err := seccomp.InstallStrictMemfdDenialFilterWithAdapter(adapter)
		require.Error(t, err)
		assert.ErrorIs(t, err, seccomp.ErrSeccompTSYNCFailed)
		assert.Contains(t, err.Error(), "seccomp TSYNC failed on thread 1337")
		assert.False(t, fallbackCalled, "fallback must not be attempted on positive TID")
	})

	t.Run("SeccompFallbackPermitted_Success", func(t *testing.T) {
		t.Parallel()
		fallbackCalled := false
		adapter := seccomp.SyscallAdapter{
			Prctl: func(_ int, _, _, _, _ uintptr) error { return nil },
			RawSyscall: func(trap, _, _, _ uintptr) (uintptr, uintptr, syscall.Errno) {
				if trap == unix.SYS_SECCOMP {
					return 0, 0, unix.ENOSYS
				}
				if trap == syscall.SYS_PRCTL {
					fallbackCalled = true
					return 0, 0, 0
				}
				return 0, 0, unix.EINVAL
			},
		}
		err := seccomp.InstallStrictMemfdDenialFilterWithAdapter(adapter)
		require.NoError(t, err)
		assert.True(t, fallbackCalled)
	})

	t.Run("SeccompFallbackPermitted_ErrnoFailure", func(t *testing.T) {
		t.Parallel()
		adapter := seccomp.SyscallAdapter{
			Prctl: func(_ int, _, _, _, _ uintptr) error { return nil },
			RawSyscall: func(trap, _, _, _ uintptr) (uintptr, uintptr, syscall.Errno) {
				if trap == unix.SYS_SECCOMP {
					return 0, 0, unix.EINVAL
				}
				if trap == syscall.SYS_PRCTL {
					return 0, 0, unix.EPERM
				}
				return 0, 0, 0
			},
		}
		err := seccomp.InstallStrictMemfdDenialFilterWithAdapter(adapter)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "prctl seccomp fallback failed")
		assert.Contains(t, err.Error(), "operation not permitted")
	})

	t.Run("SeccompFallbackPermitted_ThreadFailure", func(t *testing.T) {
		t.Parallel()
		adapter := seccomp.SyscallAdapter{
			Prctl: func(_ int, _, _, _, _ uintptr) error { return nil },
			RawSyscall: func(trap, _, _, _ uintptr) (uintptr, uintptr, syscall.Errno) {
				if trap == unix.SYS_SECCOMP {
					return 0, 0, unix.ENOSYS
				}
				if trap == syscall.SYS_PRCTL {
					return testFallbackTID, 0, 0
				}
				return 0, 0, 0
			},
		}
		err := seccomp.InstallStrictMemfdDenialFilterWithAdapter(adapter)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "prctl seccomp fallback failed on thread 42")
	})

	t.Run("SeccompHardError_EPERM", func(t *testing.T) {
		t.Parallel()
		fallbackCalled := false
		adapter := seccomp.SyscallAdapter{
			Prctl: func(_ int, _, _, _, _ uintptr) error { return nil },
			RawSyscall: func(trap, _, _, _ uintptr) (uintptr, uintptr, syscall.Errno) {
				if trap == unix.SYS_SECCOMP {
					return 0, 0, unix.EPERM
				}
				if trap == syscall.SYS_PRCTL {
					fallbackCalled = true
				}
				return 0, 0, 0
			},
		}
		err := seccomp.InstallStrictMemfdDenialFilterWithAdapter(adapter)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "seccomp filter installation failed")
		assert.False(t, fallbackCalled, "fallback must not be called on hard error")
	})
}

func TestRunStandardSyscallCheck(t *testing.T) {
	t.Parallel()
	err := seccomp.RunStandardSyscallCheck()
	require.NoError(t, err)
}

func TestRunRunner_Orchestration(t *testing.T) {
	t.Parallel()

	t.Run("UsageErrorWhenNoTarget", func(t *testing.T) {
		t.Parallel()
		var stderr bytes.Buffer
		code := seccomp.RunRunner([]string{testRunnerBin}, seccomp.DefaultSyscallAdapter(), nil, &stderr)
		assert.Equal(t, seccomp.ExitCodeUsageError, code)
		assert.Contains(t, stderr.String(), "usage:")
	})

	t.Run("PositiveTIDAbortsWithoutExec", func(t *testing.T) {
		t.Parallel()
		var stderr bytes.Buffer
		execCalled := false
		adapter := seccomp.SyscallAdapter{
			Prctl: func(_ int, _, _, _, _ uintptr) error { return nil },
			RawSyscall: func(trap, _, _, _ uintptr) (uintptr, uintptr, syscall.Errno) {
				if trap == unix.SYS_SECCOMP {
					return testTID, 0, 0
				}
				return 0, 0, 0
			},
		}
		execFn := func(_ string, _ []string, _ []string) error {
			execCalled = true
			return nil
		}
		code := seccomp.RunRunner([]string{testRunnerBin, testTrueBin}, adapter, execFn, &stderr)
		assert.Equal(t, seccomp.ExitCodeSelfTestFailure, code)
		assert.Contains(t, stderr.String(), "filter installation failed")
		assert.Contains(t, stderr.String(), "seccomp TSYNC failed on thread 1337")
		assert.False(t, execCalled, "target must not be executed on installation failure")
	})

	t.Run("HardErrorAbortsWithoutExec", func(t *testing.T) {
		t.Parallel()
		var stderr bytes.Buffer
		execCalled := false
		adapter := seccomp.SyscallAdapter{
			Prctl: func(_ int, _, _, _, _ uintptr) error { return nil },
			RawSyscall: func(trap, _, _, _ uintptr) (uintptr, uintptr, syscall.Errno) {
				if trap == unix.SYS_SECCOMP {
					return 0, 0, unix.EPERM
				}
				return 0, 0, 0
			},
		}
		execFn := func(_ string, _ []string, _ []string) error {
			execCalled = true
			return nil
		}
		code := seccomp.RunRunner([]string{testRunnerBin, testTrueBin}, adapter, execFn, &stderr)
		assert.Equal(t, seccomp.ExitCodeSelfTestFailure, code)
		assert.Contains(t, stderr.String(), "operation not permitted")
		assert.False(t, execCalled, "target must not be executed on hard error")
	})

	t.Run("InvariantFailureWithoutActualFilterAbortsWithoutExec", func(t *testing.T) {
		t.Parallel()
		var stderr bytes.Buffer
		execCalled := false
		// Adapter fakes success for SECCOMP, but since no real filter is active,
		// MemfdCreate in invariant check will succeed and trigger invariant failure!
		adapter := seccomp.SyscallAdapter{
			Prctl: func(_ int, _, _, _, _ uintptr) error { return nil },
			RawSyscall: func(_ uintptr, _, _, _ uintptr) (uintptr, uintptr, syscall.Errno) {
				return 0, 0, 0
			},
		}
		execFn := func(_ string, _ []string, _ []string) error {
			execCalled = true
			return nil
		}
		code := seccomp.RunRunner([]string{testRunnerBin, testTrueBin}, adapter, execFn, &stderr)
		assert.Equal(t, seccomp.ExitCodeSelfTestFailure, code)
		assert.Contains(t, stderr.String(), "single injected failure invariant failed")
		assert.False(t, execCalled, "target must not be executed on invariant failure")
	})
}

func TestResolveRunnerArgsAndAdapter(t *testing.T) {
	t.Run("NoSimulation", func(t *testing.T) {
		args := []string{testRunnerBin, testEchoBin, "hello"}
		clean, adapter := seccomp.ResolveRunnerArgsAndAdapter(args)
		assert.Equal(t, args, clean)
		assert.NotNil(t, adapter.RawSyscall)
		assert.NotNil(t, adapter.Prctl)
	})

	t.Run("FlagSimulateTsyncTID", func(t *testing.T) {
		args := []string{testRunnerBin, "--simulate-tsync-tid=999", testEchoBin, "hello"}
		clean, adapter := seccomp.ResolveRunnerArgsAndAdapter(args)
		assert.Equal(t, []string{testRunnerBin, testEchoBin, "hello"}, clean)
		r1, _, errno := adapter.RawSyscall(unix.SYS_SECCOMP, 0, 0, 0)
		assert.Equal(t, uintptr(999), r1)
		assert.Equal(t, syscall.Errno(0), errno)
	})

	t.Run("FlagSimulateSeccompErrno", func(t *testing.T) {
		args := []string{testRunnerBin, "--simulate-seccomp-errno=EPERM", testEchoBin}
		clean, adapter := seccomp.ResolveRunnerArgsAndAdapter(args)
		assert.Equal(t, []string{testRunnerBin, testEchoBin}, clean)
		r1, _, errno := adapter.RawSyscall(unix.SYS_SECCOMP, 0, 0, 0)
		assert.Equal(t, uintptr(0), r1)
		assert.Equal(t, unix.EPERM, errno)
	})

	t.Run("FlagSimulateFallbackErrnoAndTID", func(t *testing.T) {
		args := []string{
			testRunnerBin,
			"--simulate-fallback-errno=EACCES",
			"--simulate-fallback-tid=77",
			testEchoBin,
		}
		clean, adapter := seccomp.ResolveRunnerArgsAndAdapter(args)
		assert.Equal(t, []string{testRunnerBin, testEchoBin}, clean)
		r1, _, errno := adapter.RawSyscall(syscall.SYS_PRCTL, uintptr(unix.PR_SET_SECCOMP), 0, 0)
		assert.Equal(t, uintptr(77), r1)
		assert.Equal(t, syscall.Errno(0), errno)
	})

	t.Run("EnvSimulateTsyncTID", func(t *testing.T) {
		t.Setenv(seccomp.EnvSimulateTsyncTID, "888")
		args := []string{testRunnerBin, testEchoBin}
		clean, adapter := seccomp.ResolveRunnerArgsAndAdapter(args)
		assert.Equal(t, args, clean)
		r1, _, errno := adapter.RawSyscall(unix.SYS_SECCOMP, 0, 0, 0)
		assert.Equal(t, uintptr(888), r1)
		assert.Equal(t, syscall.Errno(0), errno)
	})

	t.Run("FlagSimulateNoNewPrivsErr", func(t *testing.T) {
		args := []string{testRunnerBin, "--simulate-no-new-privs-err", testEchoBin}
		clean, adapter := seccomp.ResolveRunnerArgsAndAdapter(args)
		assert.Equal(t, []string{testRunnerBin, testEchoBin}, clean)
		err := adapter.Prctl(unix.PR_SET_NO_NEW_PRIVS, 0, 0, 0, 0)
		require.ErrorIs(t, err, unix.EPERM)
	})
}

func TestParseErrnoName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, unix.ENOSYS, seccomp.ParseErrnoNameForTest("ENOSYS"))
	assert.Equal(t, unix.EINVAL, seccomp.ParseErrnoNameForTest("einval"))
	assert.Equal(t, unix.EPERM, seccomp.ParseErrnoNameForTest("EPERM"))
	assert.Equal(t, unix.EACCES, seccomp.ParseErrnoNameForTest("EACCES"))
	assert.Equal(t, unix.ENOENT, seccomp.ParseErrnoNameForTest("enoent"))
	assert.Equal(t, syscall.Errno(42), seccomp.ParseErrnoNameForTest("42"))
	assert.Equal(t, syscall.Errno(0), seccomp.ParseErrnoNameForTest("NOT_AN_ERRNO"))
}

func TestInstallStrictMemfdDenialFilter(t *testing.T) {
	restore := seccomp.SetDefaultSyscallAdapter(func() seccomp.SyscallAdapter {
		return seccomp.SyscallAdapter{
			Prctl: func(_ int, _, _, _, _ uintptr) error { return nil },
			RawSyscall: func(_ uintptr, _, _, _ uintptr) (uintptr, uintptr, syscall.Errno) {
				return 0, 0, 0
			},
		}
	})
	defer restore()

	err := seccomp.InstallStrictMemfdDenialFilter()
	require.NoError(t, err)
}

func TestRunStandardSyscallCheck_OpenDevNullFailure(t *testing.T) {
	restore := seccomp.SetOpenDevNullProbe(func() (int, error) {
		return -1, unix.EACCES
	})
	defer restore()

	err := seccomp.RunStandardSyscallCheck()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open /dev/null")
}

func TestVerifySingleInjectedFailureInvariant_Branches(t *testing.T) {
	t.Run("StandardSyscallCheckFailure", func(t *testing.T) {
		restore := seccomp.SetOpenDevNullProbe(func() (int, error) {
			return -1, unix.EACCES
		})
		defer restore()

		err := seccomp.VerifySingleInjectedFailureInvariant()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "standard syscall failed under filter")
	})

	t.Run("MemfdUnexpectedSuccess", func(t *testing.T) {
		restore := seccomp.SetMemfdCreateProbe(func(_ string, _ int) (int, error) {
			return 100, nil
		})
		defer restore()

		err := seccomp.VerifySingleInjectedFailureInvariant()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "memfd_create unexpectedly succeeded")
	})

	t.Run("MemfdUnexpectedErrno", func(t *testing.T) {
		restore := seccomp.SetMemfdCreateProbe(func(_ string, _ int) (int, error) {
			return -1, unix.EPERM
		})
		defer restore()

		err := seccomp.VerifySingleInjectedFailureInvariant()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected ENOSYS error on memfd_create")
	})

	t.Run("MemfdExpectedENOSYS", func(t *testing.T) {
		restore := seccomp.SetMemfdCreateProbe(func(_ string, _ int) (int, error) {
			return -1, unix.ENOSYS
		})
		defer restore()

		err := seccomp.VerifySingleInjectedFailureInvariant()
		require.NoError(t, err)
	})
}

func TestRunRunner_PreFilterFailureAndSuccess(t *testing.T) {
	t.Run("PreFilterFailure", func(t *testing.T) {
		restore := seccomp.SetOpenDevNullProbe(func() (int, error) {
			return -1, unix.EACCES
		})
		defer restore()

		var stderr bytes.Buffer
		code := seccomp.RunRunner([]string{testRunnerBin, testTrueBin}, seccomp.DefaultSyscallAdapter(), nil, &stderr)
		assert.Equal(t, seccomp.ExitCodeSelfTestFailure, code)
		assert.Contains(t, stderr.String(), "pre-filter self-test failed")
	})

	t.Run("SuccessAndExecError", func(t *testing.T) {
		restore := seccomp.SetMemfdCreateProbe(func(_ string, _ int) (int, error) {
			return -1, unix.ENOSYS
		})
		defer restore()

		adapter := seccomp.SyscallAdapter{
			Prctl: func(_ int, _, _, _, _ uintptr) error { return nil },
			RawSyscall: func(_ uintptr, _, _, _ uintptr) (uintptr, uintptr, syscall.Errno) {
				return 0, 0, 0
			},
		}

		var stderr bytes.Buffer
		execCalled := false
		execFn := func(_ string, _ []string, _ []string) error {
			execCalled = true
			return unix.ENOENT
		}

		code := seccomp.RunRunner([]string{testRunnerBin, testTrueBin}, adapter, execFn, &stderr)
		assert.Equal(t, seccomp.ExitCodeExecFailure, code)
		assert.True(t, execCalled)
		assert.Contains(t, stderr.String(), "execve /bin/true failed")
	})

	t.Run("FullSuccess", func(t *testing.T) {
		restore := seccomp.SetMemfdCreateProbe(func(_ string, _ int) (int, error) {
			return -1, unix.ENOSYS
		})
		defer restore()

		adapter := seccomp.SyscallAdapter{
			Prctl: func(_ int, _, _, _, _ uintptr) error { return nil },
			RawSyscall: func(_ uintptr, _, _, _ uintptr) (uintptr, uintptr, syscall.Errno) {
				return 0, 0, 0
			},
		}

		var stderr bytes.Buffer
		execCalled := false
		execFn := func(_ string, _ []string, _ []string) error {
			execCalled = true
			return nil
		}

		code := seccomp.RunRunner([]string{testRunnerBin, testTrueBin}, adapter, execFn, &stderr)
		assert.Equal(t, 0, code)
		assert.True(t, execCalled)
		assert.Empty(t, stderr.String())
	})
}

func TestResolveRunnerArgsAndAdapter_EnvAndEdgeCases(t *testing.T) {
	t.Run("EnvSimulateSeccompErrno", func(t *testing.T) {
		t.Setenv(seccomp.EnvSimulateSeccompErrno, "ENOSYS")
		_, adapter := seccomp.ResolveRunnerArgsAndAdapter([]string{testRunnerBin, testEchoBin})
		_, _, errno := adapter.RawSyscall(unix.SYS_SECCOMP, 0, 0, 0)
		assert.Equal(t, unix.ENOSYS, errno)
	})

	t.Run("EnvSimulateFallbackErrno", func(t *testing.T) {
		t.Setenv(seccomp.EnvSimulateFallbackErrno, "EINVAL")
		_, adapter := seccomp.ResolveRunnerArgsAndAdapter([]string{testRunnerBin, testEchoBin})
		_, _, errno := adapter.RawSyscall(unix.SYS_SECCOMP, 0, 0, 0)
		assert.Equal(t, unix.ENOSYS, errno)
		_, _, errno = adapter.RawSyscall(syscall.SYS_PRCTL, uintptr(unix.PR_SET_SECCOMP), 0, 0)
		assert.Equal(t, unix.EINVAL, errno)
	})

	t.Run("EnvSimulateFallbackTID", func(t *testing.T) {
		t.Setenv(seccomp.EnvSimulateFallbackTID, "123")
		_, adapter := seccomp.ResolveRunnerArgsAndAdapter([]string{testRunnerBin, testEchoBin})
		_, _, errno := adapter.RawSyscall(unix.SYS_SECCOMP, 0, 0, 0)
		assert.Equal(t, unix.ENOSYS, errno)
		r1, _, _ := adapter.RawSyscall(syscall.SYS_PRCTL, uintptr(unix.PR_SET_SECCOMP), 0, 0)
		assert.Equal(t, uintptr(123), r1)
	})

	t.Run("EnvSimulateNoNewPrivsErr", func(t *testing.T) {
		t.Setenv(seccomp.EnvSimulateNoNewPrivsErr, "1")
		_, adapter := seccomp.ResolveRunnerArgsAndAdapter([]string{testRunnerBin, testEchoBin})
		err := adapter.Prctl(unix.PR_SET_NO_NEW_PRIVS, 0, 0, 0, 0)
		assert.Equal(t, unix.EPERM, err)
		_ = adapter.Prctl(unix.PR_GET_NAME, 0, 0, 0, 0)
	})

	t.Run("BuildSimulatedSyscallAdapter_Passthrough", func(t *testing.T) {
		adapter := seccomp.BuildSimulatedSyscallAdapter(seccomp.SimulationConfig{})
		pid, _, _ := adapter.RawSyscall(syscall.SYS_GETPID, 0, 0, 0)
		assert.True(t, pid > 0)
	})
}
