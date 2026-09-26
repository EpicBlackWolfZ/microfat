//go:build linux

package main

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/memfd"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func resetDoctorMockSeams(t *testing.T) {
	t.Helper()
	origMemfd := memfdProbeSyscall
	origFstat := memfdProbeFstat
	origFcntl := memfdProbeFcntl
	origClose := memfdProbeClose
	origUname := unameSyscall
	origDetect := microarchDetectFunc
	origDownclock := isAVX512DownclockingRiskFunc

	t.Cleanup(func() {
		memfdProbeSyscall = origMemfd
		memfdProbeFstat = origFstat
		memfdProbeFcntl = origFcntl
		memfdProbeClose = origClose
		unameSyscall = origUname
		microarchDetectFunc = origDetect
		isAVX512DownclockingRiskFunc = origDownclock
	})

	microarchDetectFunc = func() microarch.Info {
		return microarch.Info{
			OS:       testOSLinux,
			Arch:     testArchAMD64,
			Level:    "v3",
			Features: []string{"avx", "avx2"},
		}
	}
	isAVX512DownclockingRiskFunc = func() bool { return false }
	unameSyscall = func(buf *unix.Utsname) error {
		copy(buf.Release[:], []byte("6.8.0-test\x00"))
		return nil
	}
}

func executeDoctorBothFormats(t *testing.T, args []string) (string, DoctorReport, error) {
	t.Helper()

	textCmd := newDoctorCmd()
	var textBuf bytes.Buffer
	textCmd.SetOut(&textBuf)
	textCmd.SetArgs(args)
	textErr := textCmd.Execute()

	jsonArgs := append([]string{flagJSON}, args...)
	jsonCmd := newDoctorCmd()
	var jsonBuf bytes.Buffer
	jsonCmd.SetOut(&jsonBuf)
	jsonCmd.SetArgs(jsonArgs)
	jsonErr := jsonCmd.Execute()

	if textErr != nil && jsonErr == nil {
		t.Fatalf("expected both text and JSON runs to agree on error, textErr=%v jsonErr=nil", textErr)
	}
	if textErr == nil && jsonErr != nil {
		t.Fatalf("expected both text and JSON runs to agree on success, textErr=nil jsonErr=%v", jsonErr)
	}

	var rep DoctorReport
	err := json.Unmarshal(jsonBuf.Bytes(), &rep)
	require.NoError(t, err, "decoded JSON must match DoctorReport schema\nRaw: %s", jsonBuf.String())

	return textBuf.String(), rep, textErr
}

func TestDoctorReportMatrix_TruthfulDiagnostics(t *testing.T) {
	t.Run("CreationWrappedEPERM", func(t *testing.T) {
		resetDoctorMockSeams(t)
		privateCache := filepath.Join(t.TempDir(), "cache")

		callCount := 0
		memfdProbeSyscall = func(name string, flags int) (int, error) {
			callCount++
			return -1, fmt.Errorf("wrapped creation error: %w", syscall.EPERM)
		}

		textOut, rep, err := executeDoctorBothFormats(t, []string{flagCacheDir, privateCache})
		require.NoError(t, err, "normal mode should succeed with healthy cache fallback")

		assert.False(t, rep.Memfd.Available)
		assert.False(t, rep.Memfd.Passed)
		assert.Equal(t, "creation", rep.Memfd.Phase)
		assert.Equal(t, "create", rep.Memfd.Operation)
		assert.Equal(t, "EPERM", rep.Memfd.ErrnoName)
		assert.Equal(t, int(syscall.EPERM), rep.Memfd.ErrnoValue)
		assert.Equal(t, "not_tested", rep.Memfd.Execution)
		assert.Equal(t, "Unknown (not independently tested)", rep.Memfd.Seccomp)
		assert.Equal(t, 2, callCount, "exactly 1 call per command run; no retry should occur on EPERM denial")

		assert.Contains(t, textOut, "Failed Phase:      creation")
		assert.Contains(t, textOut, "Failed Operation:  create")
		assert.Contains(t, textOut, "Errno:             EPERM")
		assert.NotContains(t, textOut, "Restricted (seals blocked)")
	})

	t.Run("CreationEACCES", func(t *testing.T) {
		resetDoctorMockSeams(t)
		privateCache := filepath.Join(t.TempDir(), "cache")

		memfdProbeSyscall = func(name string, flags int) (int, error) {
			return -1, syscall.EACCES
		}

		textOut, rep, err := executeDoctorBothFormats(t, []string{flagCacheDir, privateCache})
		require.NoError(t, err)

		assert.False(t, rep.Memfd.Available)
		assert.False(t, rep.Memfd.Passed)
		assert.Equal(t, "creation", rep.Memfd.Phase)
		assert.Equal(t, "create", rep.Memfd.Operation)
		assert.Equal(t, "EACCES", rep.Memfd.ErrnoName)
		assert.Equal(t, int(syscall.EACCES), rep.Memfd.ErrnoValue)
		assert.NotEqual(t, "EPERM", rep.Memfd.ErrnoName, "EACCES must remain distinct from EPERM")
		assert.Contains(t, textOut, "Errno:             EACCES")
	})

	t.Run("CreationEINVAL_CompatibilityRetrySucceeds", func(t *testing.T) {
		resetDoctorMockSeams(t)
		privateCache := filepath.Join(t.TempDir(), "cache")

		callCount := 0
		memfdProbeSyscall = func(name string, flags int) (int, error) {
			callCount++
			if flags&unix.MFD_EXEC != 0 {
				return -1, syscall.EINVAL
			}
			return 42, nil
		}
		memfdProbeFstat = func(fd int, stat *unix.Stat_t) error {
			stat.Mode = unix.S_IFREG | 0o700
			return nil
		}
		memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) {
			if cmd == unix.F_GET_SEALS {
				return memfd.TargetSeals, nil
			}
			return 0, nil
		}
		memfdProbeClose = func(fd int) error { return nil }

		_, rep, err := executeDoctorBothFormats(t, []string{flagCacheDir, privateCache})
		require.NoError(t, err)

		assert.True(t, rep.Memfd.Available)
		assert.True(t, rep.Memfd.Passed)
		assert.Equal(t, "legacy retry (implicit execution)", rep.Memfd.CreationStrategy)
		assert.Equal(t, 4, callCount, "compatibility retry must have been attempted exactly once per command execution")
		assert.Equal(t, "not_tested", rep.Memfd.Execution)
	})

	t.Run("FstatEIO_SealsOtherwiseWouldSucceed", func(t *testing.T) {
		resetDoctorMockSeams(t)
		privateCache := filepath.Join(t.TempDir(), "cache")

		memfdProbeSyscall = func(name string, flags int) (int, error) { return 42, nil }
		memfdProbeFstat = func(fd int, stat *unix.Stat_t) error { return syscall.EIO }
		memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) {
			if cmd == unix.F_GET_SEALS {
				return memfd.TargetSeals, nil
			}
			return 0, nil
		}
		memfdProbeClose = func(fd int) error { return nil }

		textOut, rep, err := executeDoctorBothFormats(t, []string{flagCacheDir, privateCache})
		require.NoError(t, err)

		assert.False(t, rep.Memfd.Available)
		assert.False(t, rep.Memfd.Passed)
		assert.Equal(t, "fstat", rep.Memfd.Phase)
		assert.Equal(t, "fstat", rep.Memfd.Operation)
		assert.Equal(t, "EIO", rep.Memfd.ErrnoName)
		assert.Nil(t, rep.Memfd.Mode, "no invented non-executable-bit observation when fstat itself failed")
		assert.Contains(t, textOut, "Failed Phase:      fstat")
		assert.Contains(t, textOut, "Failed Operation:  fstat")
	})

	t.Run("ModeMissingExecuteBits", func(t *testing.T) {
		resetDoctorMockSeams(t)
		privateCache := filepath.Join(t.TempDir(), "cache")

		memfdProbeSyscall = func(name string, flags int) (int, error) { return 42, nil }
		memfdProbeFstat = func(fd int, stat *unix.Stat_t) error {
			stat.Mode = unix.S_IFREG | 0o644
			return nil
		}
		memfdProbeClose = func(fd int) error { return nil }

		textOut, rep, err := executeDoctorBothFormats(t, []string{flagCacheDir, privateCache})
		require.NoError(t, err)

		assert.False(t, rep.Memfd.Available)
		assert.False(t, rep.Memfd.Passed)
		assert.Equal(t, "mode", rep.Memfd.Phase)
		assert.Equal(t, "mode", rep.Memfd.Operation)
		require.NotNil(t, rep.Memfd.Mode)
		assert.False(t, rep.Memfd.Mode.IsExecutable)
		assert.Equal(t, "0644", rep.Memfd.Mode.ModeOctal)
		assert.Contains(t, rep.Memfd.Status, "Descriptor non-executable")
		assert.Contains(t, textOut, "0644")
	})

	t.Run("F_ADD_SEALS_Returns_EPERM", func(t *testing.T) {
		resetDoctorMockSeams(t)
		privateCache := filepath.Join(t.TempDir(), "cache")

		memfdProbeSyscall = func(name string, flags int) (int, error) { return 42, nil }
		memfdProbeFstat = func(fd int, stat *unix.Stat_t) error {
			stat.Mode = unix.S_IFREG | 0o700
			return nil
		}
		memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) {
			if cmd == unix.F_ADD_SEALS {
				return -1, syscall.EPERM
			}
			return 0, nil
		}
		memfdProbeClose = func(fd int) error { return nil }

		textOut, rep, err := executeDoctorBothFormats(t, []string{flagCacheDir, privateCache})
		require.NoError(t, err)

		assert.False(t, rep.Memfd.Available)
		assert.False(t, rep.Memfd.Passed)
		assert.Equal(t, "seals", rep.Memfd.Phase)
		assert.Equal(t, "add_seals", rep.Memfd.Operation)
		assert.Equal(t, "EPERM", rep.Memfd.ErrnoName)
		assert.Equal(t, "Unknown (not independently tested)", rep.Memfd.Seccomp)
		assert.Contains(t, rep.Memfd.Status, "Seal application denied (F_ADD_SEALS failed)")
		assert.Contains(t, textOut, "Failed Operation:  add_seals")
	})

	t.Run("F_ADD_SEALS_Succeeds_F_GET_SEALS_Returns_EIO", func(t *testing.T) {
		resetDoctorMockSeams(t)
		privateCache := filepath.Join(t.TempDir(), "cache")

		memfdProbeSyscall = func(name string, flags int) (int, error) { return 42, nil }
		memfdProbeFstat = func(fd int, stat *unix.Stat_t) error {
			stat.Mode = unix.S_IFREG | 0o700
			return nil
		}
		memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) {
			if cmd == unix.F_ADD_SEALS {
				return 0, nil
			}
			if cmd == unix.F_GET_SEALS {
				return -1, syscall.EIO
			}
			return 0, nil
		}
		memfdProbeClose = func(fd int) error { return nil }

		textOut, rep, err := executeDoctorBothFormats(t, []string{flagCacheDir, privateCache})
		require.NoError(t, err)

		assert.False(t, rep.Memfd.Available)
		assert.False(t, rep.Memfd.Passed)
		assert.Equal(t, "seals", rep.Memfd.Phase)
		assert.Equal(t, "get_seals", rep.Memfd.Operation)
		assert.Equal(t, "EIO", rep.Memfd.ErrnoName)
		assert.Contains(t, rep.Memfd.Status, "Seal verification failed (F_GET_SEALS failed)")
		assert.NotContains(t, textOut, "seals blocked")
		assert.NotContains(t, textOut, "seccomp restricted")
	})

	t.Run("SealReadbackLacksRequiredBits", func(t *testing.T) {
		resetDoctorMockSeams(t)
		privateCache := filepath.Join(t.TempDir(), "cache")

		memfdProbeSyscall = func(name string, flags int) (int, error) { return 42, nil }
		memfdProbeFstat = func(fd int, stat *unix.Stat_t) error {
			stat.Mode = unix.S_IFREG | 0o700
			return nil
		}
		memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) {
			if cmd == unix.F_GET_SEALS {
				return unix.F_SEAL_WRITE, nil // missing SEAL, SHRINK, GROW
			}
			return 0, nil
		}
		memfdProbeClose = func(fd int) error { return nil }

		textOut, rep, err := executeDoctorBothFormats(t, []string{flagCacheDir, privateCache})
		require.NoError(t, err)

		assert.False(t, rep.Memfd.Available)
		assert.False(t, rep.Memfd.Passed)
		assert.Equal(t, "seals", rep.Memfd.Phase)
		assert.Equal(t, "get_seals", rep.Memfd.Operation)
		assert.Contains(t, rep.Memfd.Status, "mandatory seals missing in readback")
		assert.Contains(t, textOut, "mandatory seals missing in readback")
	})

	t.Run("AllPrerequisitesSucceed", func(t *testing.T) {
		resetDoctorMockSeams(t)
		privateCache := filepath.Join(t.TempDir(), "cache")

		memfdProbeSyscall = func(name string, flags int) (int, error) { return 42, nil }
		memfdProbeFstat = func(fd int, stat *unix.Stat_t) error {
			stat.Mode = unix.S_IFREG | 0o700
			return nil
		}
		memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) {
			if cmd == unix.F_GET_SEALS {
				return memfd.TargetSeals, nil
			}
			return 0, nil
		}
		memfdProbeClose = func(fd int) error { return nil }

		textOut, rep, err := executeDoctorBothFormats(t, []string{flagCacheDir, privateCache})
		require.NoError(t, err)

		assert.True(t, rep.Ready)
		assert.True(t, rep.Memfd.Available)
		assert.True(t, rep.Memfd.Passed)
		assert.Equal(t, "not_tested", rep.Memfd.Execution)
		assert.Equal(t, "not_tested", rep.Execution.Status)
		assert.Equal(t, "not_tested", rep.Execution.Reason)
		assert.Contains(t, rep.Summary, "payload execution was not tested")
		assert.Contains(t, textOut, "payload execution was not tested")
	})
}

func TestDoctor_StrictAndNormalAggregation(t *testing.T) {
	resetDoctorMockSeams(t)
	validCache := filepath.Join(t.TempDir(), "valid_cache")
	invalidCache := "/dev/null/cannot_write_here"

	mockMemfdFailing := func() {
		memfdProbeSyscall = func(name string, flags int) (int, error) {
			return -1, syscall.EPERM
		}
	}
	mockMemfdSuccess := func() {
		memfdProbeSyscall = func(name string, flags int) (int, error) { return 42, nil }
		memfdProbeFstat = func(fd int, stat *unix.Stat_t) error {
			stat.Mode = unix.S_IFREG | 0o700
			return nil
		}
		memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) {
			if cmd == unix.F_GET_SEALS {
				return memfd.TargetSeals, nil
			}
			return 0, nil
		}
		memfdProbeClose = func(fd int) error { return nil }
	}

	t.Run("Normal_MemfdFails_CacheHealthy", func(t *testing.T) {
		mockMemfdFailing()
		_, rep, err := executeDoctorBothFormats(t, []string{flagCacheDir, validCache})
		assert.NoError(t, err, "normal mode succeeds when cache fallback is healthy")
		assert.True(t, rep.Ready)
		assert.NotEmpty(t, rep.Warnings)
	})

	t.Run("Strict_MemfdFails_CacheHealthy", func(t *testing.T) {
		mockMemfdFailing()
		_, rep, err := executeDoctorBothFormats(t, []string{flagStrict, flagCacheDir, validCache})
		assert.Error(t, err, "strict mode requires both memfd and cache to succeed")
		assert.False(t, rep.Ready)
		assert.NotEmpty(t, rep.Errors)
	})

	t.Run("Normal_MemfdSuccess_CacheUnhealthy", func(t *testing.T) {
		mockMemfdSuccess()
		_, rep, err := executeDoctorBothFormats(t, []string{flagCacheDir, invalidCache})
		assert.NoError(t, err, "normal mode succeeds when memfd is healthy even if cache is unhealthy")
		assert.True(t, rep.Ready)
		assert.NotEmpty(t, rep.Warnings)
	})

	t.Run("Strict_MemfdSuccess_CacheUnhealthy", func(t *testing.T) {
		mockMemfdSuccess()
		_, rep, err := executeDoctorBothFormats(t, []string{flagStrict, flagCacheDir, invalidCache})
		assert.Error(t, err, "strict mode requires both memfd and cache to succeed")
		assert.False(t, rep.Ready)
		assert.NotEmpty(t, rep.Errors)
	})

	t.Run("BothUnhealthy_FailsBothModes", func(t *testing.T) {
		mockMemfdFailing()
		_, repNormal, errNormal := executeDoctorBothFormats(t, []string{flagCacheDir, invalidCache})
		assert.Error(t, errNormal)
		assert.False(t, repNormal.Ready)

		_, repStrict, errStrict := executeDoctorBothFormats(t, []string{flagStrict, flagCacheDir, invalidCache})
		assert.Error(t, errStrict)
		assert.False(t, repStrict.Ready)
	})
}

func TestDoctor_DescriptorClosureOnInjectedFailures(t *testing.T) {
	resetDoctorMockSeams(t)

	failureScenarios := []struct {
		name  string
		setup func(closed *bool)
	}{
		{
			name: "fstat failure",
			setup: func(closed *bool) {
				memfdProbeSyscall = func(name string, flags int) (int, error) { return 42, nil }
				memfdProbeFstat = func(fd int, stat *unix.Stat_t) error { return syscall.EBADF }
				memfdProbeClose = func(fd int) error {
					if fd == 42 {
						*closed = true
					}
					return nil
				}
			},
		},
		{
			name: "mode non-executable",
			setup: func(closed *bool) {
				memfdProbeSyscall = func(name string, flags int) (int, error) { return 42, nil }
				memfdProbeFstat = func(fd int, stat *unix.Stat_t) error {
					stat.Mode = unix.S_IFREG | 0o600
					return nil
				}
				memfdProbeClose = func(fd int) error {
					if fd == 42 {
						*closed = true
					}
					return nil
				}
			},
		},
		{
			name: "F_ADD_SEALS failure",
			setup: func(closed *bool) {
				memfdProbeSyscall = func(name string, flags int) (int, error) { return 42, nil }
				memfdProbeFstat = func(fd int, stat *unix.Stat_t) error {
					stat.Mode = unix.S_IFREG | 0o700
					return nil
				}
				memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) {
					if cmd == unix.F_ADD_SEALS {
						return -1, syscall.EPERM
					}
					return 0, nil
				}
				memfdProbeClose = func(fd int) error {
					if fd == 42 {
						*closed = true
					}
					return nil
				}
			},
		},
		{
			name: "F_GET_SEALS failure",
			setup: func(closed *bool) {
				memfdProbeSyscall = func(name string, flags int) (int, error) { return 42, nil }
				memfdProbeFstat = func(fd int, stat *unix.Stat_t) error {
					stat.Mode = unix.S_IFREG | 0o700
					return nil
				}
				memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) {
					if cmd == unix.F_ADD_SEALS {
						return 0, nil
					}
					if cmd == unix.F_GET_SEALS {
						return -1, syscall.EIO
					}
					return 0, nil
				}
				memfdProbeClose = func(fd int) error {
					if fd == 42 {
						*closed = true
					}
					return nil
				}
			},
		},
		{
			name: "missing seals in readback",
			setup: func(closed *bool) {
				memfdProbeSyscall = func(name string, flags int) (int, error) { return 42, nil }
				memfdProbeFstat = func(fd int, stat *unix.Stat_t) error {
					stat.Mode = unix.S_IFREG | 0o700
					return nil
				}
				memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) {
					if cmd == unix.F_GET_SEALS {
						return unix.F_SEAL_WRITE, nil
					}
					return 0, nil
				}
				memfdProbeClose = func(fd int) error {
					if fd == 42 {
						*closed = true
					}
					return nil
				}
			},
		},
	}

	for _, tc := range failureScenarios {
		t.Run(tc.name, func(t *testing.T) {
			closed := false
			tc.setup(&closed)

			rep := probeMemfd()
			assert.False(t, rep.Passed)
			assert.True(t, closed, "descriptor fd 42 must be closed on failure path: %s", tc.name)
		})
	}
}

func TestDoctor_NegativeControl_TypedErrorPreservation(t *testing.T) {
	resetDoctorMockSeams(t)

	sentinelErr := syscall.EPERM
	memfdProbeSyscall = func(name string, flags int) (int, error) {
		return -1, sentinelErr
	}

	rep := probeMemfd()
	require.False(t, rep.Passed)
	require.NotNil(t, rep.Cause, "typed cause must be preserved in probeMemfd")
	assert.True(t, errors.Is(rep.Cause, syscall.EPERM), "errors.Is must identify the typed error")

	// Negative control proof: if we instead wrapped with errors.New(obs.Error),
	// typed error matching with errors.Is would fail!
	untypedErr := errors.New(rep.Error)
	assert.False(t, errors.Is(untypedErr, syscall.EPERM), "negative control: errors.New loses typed identity")
}
