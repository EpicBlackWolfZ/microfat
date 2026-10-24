//go:build linux

package main

import (
	"bytes"
	json "encoding/json/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/cache"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestCacheMaterializationHandsVerifiedDescriptorToExec(t *testing.T) {
	for _, mode := range []string{format.ExecModeCache, format.ExecModeAuto} {
		t.Run(mode, func(t *testing.T) {
			for _, execFails := range []bool{false, true} {
				t.Run(strconv.FormatBool(execFails), func(t *testing.T) {
					f, entry, dir, payload := cacheFirstFixture(t)
					t.Setenv(format.EnvExecMode, mode)
					oldOpen, oldCanonicalOpen := openCachedBinaryAtFunc, cache.OpenFileAtFunc
					t.Cleanup(func() {
						openCachedBinaryAtFunc, cache.OpenFileAtFunc = oldOpen, oldCanonicalOpen
					})
					validatedFD, successfulOpens, memfdCalls, execCalls := -1, 0, 0, 0
					cache.OpenFileAtFunc = func(dirFD int, name string) (int, error) {
						fd, err := oldCanonicalOpen(dirFD, name)
						if err == nil {
							successfulOpens++
							validatedFD = fd
						}
						return fd, err
					}
					// Count both canonical and launcher opens so a hidden validation-and-close
					// inside materialization cannot be followed by an unnoticed second validation.
					openCachedBinaryAtFunc = func(dirFD int, name string) (int, error) {
						return cache.OpenFileAtFunc(dirFD, name)
					}
					memfdCreateFunc = func(string, int) (int, error) {
						memfdCalls++
						return -1, unix.EPERM
					}
					execveFunc = func(path string, args, env []string) error {
						execCalls++
						require.Equal(t, "/proc/self/fd/"+strconv.Itoa(validatedFD), path)
						require.Equal(t, []string{testAppArg, "argument"}, args)
						require.Contains(t, env, format.EnvExecMode+"=cache")
						data, err := os.ReadFile(path)
						require.NoError(t, err)
						require.Equal(t, payload, data)
						if execFails {
							return unix.EACCES
						}
						return nil
					}
					err := dispatchCacheFirst(f, entry)
					if execFails {
						require.ErrorIs(t, err, unix.EACCES)
						require.ErrorIs(t, err, format.ErrExecve)
					} else {
						require.NoError(t, err)
					}
					require.Equal(t, 1, successfulOpens, "cold cache must validate one published descriptor")
					require.Equal(t, 1, execCalls)
					wantMemfdCalls := 0
					if mode == format.ExecModeAuto {
						wantMemfdCalls = 1
					}
					require.Equal(t, wantMemfdCalls, memfdCalls)
					var stat unix.Stat_t
					require.ErrorIs(t, unix.Fstat(validatedFD, &stat), unix.EBADF, "mock exec return must close held fd")
					data, err := os.ReadFile(filepath.Join(dir, entry.SHA256))
					require.NoError(t, err)
					require.Equal(t, payload, data)
				})
			}
		})
	}
}

func TestCacheMaterializationDescriptorReplacementBoundary(t *testing.T) {
	for _, timing := range []string{"before open", "after open", "after validation", "in-place after validation"} {
		t.Run(timing, func(t *testing.T) {
			f, entry, dir, payload := cacheFirstFixture(t)
			t.Setenv(format.EnvExecMode, format.ExecModeCache)
			oldOpen := openCachedBinaryAtFunc
			t.Cleanup(func() { openCachedBinaryAtFunc = oldOpen })
			path := filepath.Join(dir, entry.SHA256)
			hostile := bytes.Repeat([]byte{'x'}, len(payload))
			replacePath := func() {
				t.Helper()
				replacement := filepath.Join(dir, "replacement")
				require.NoError(t, os.WriteFile(replacement, hostile, 0o700))
				require.NoError(t, os.Rename(replacement, path))
			}
			openedFD := -1
			openCachedBinaryAtFunc = func(dirFD int, name string) (int, error) {
				if timing == "before open" {
					if _, err := os.Stat(path); err == nil {
						replacePath()
					}
				}
				fd, err := oldOpen(dirFD, name)
				if err == nil {
					openedFD = fd
					if timing == "after open" {
						replacePath()
					}
				}
				return fd, err
			}
			execCalls := 0
			execveFunc = func(procPath string, _, _ []string) error {
				execCalls++
				require.Equal(t, "/proc/self/fd/"+strconv.Itoa(openedFD), procPath)
				want := payload
				switch timing {
				case "after validation":
					replacePath()
				case "in-place after validation":
					// Descriptor binding prevents pathname redirection, but cache files remain mutable
					// by a trusted same-UID writer. Only the memfd execution path is sealed.
					require.NoError(t, os.WriteFile(path, hostile, 0o700))
					want = hostile
				}
				data, err := os.ReadFile(procPath)
				require.NoError(t, err)
				require.Equal(t, want, data)
				return nil
			}
			err := dispatchCacheFirst(f, entry)
			if timing == "before open" {
				require.ErrorIs(t, err, format.ErrCacheWrite)
				require.ErrorIs(t, err, format.ErrPayloadCorrupted)
				require.Zero(t, execCalls)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, execCalls)
			}
			var stat unix.Stat_t
			require.ErrorIs(t, unix.Fstat(openedFD, &stat), unix.EBADF)
		})
	}
}

func TestCacheMaterializationFailureClosesDescriptors(t *testing.T) {
	for _, failure := range []string{"extraction", "open", "digest", "unsafe"} {
		t.Run(failure, func(t *testing.T) {
			f, entry, dir, payload := cacheFirstFixture(t)
			t.Setenv(format.EnvExecMode, format.ExecModeCache)
			oldOpen, oldResolve := openCachedBinaryAtFunc, resolveCacheDirFunc
			t.Cleanup(func() { openCachedBinaryAtFunc = oldOpen })
			directoryFD, openedFD := -1, -1
			resolveCacheDirFunc = func(custom string) (int, string, error) {
				fd, path, err := oldResolve(custom)
				directoryFD = fd
				return fd, path, err
			}
			if failure == "extraction" {
				require.NoError(t, os.WriteFile(f.Name(), []byte("damaged source"), 0o600))
			}
			openCachedBinaryAtFunc = func(dirFD int, name string) (int, error) {
				path := filepath.Join(dir, name)
				if _, err := os.Stat(path); err == nil {
					switch failure {
					case "open":
						return -1, unix.EACCES
					case "digest":
						require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte{'x'}, len(payload)), 0o700))
					case "unsafe":
						require.NoError(t, os.Chmod(path, 0o777))
					}
				}
				fd, err := oldOpen(dirFD, name)
				if err == nil {
					openedFD = fd
				}
				return fd, err
			}
			execveFunc = func(string, []string, []string) error { t.Fatal("failed materialization must not exec"); return nil }
			err := executeViaCache(f.Name(), f, entry, nil, []string{testAppArg}, nil,
				microarch.Info{}, microarch.PolicyResult{}, nil, time.Now())
			require.Error(t, err)
			if failure == "extraction" {
				require.ErrorIs(t, err, format.ErrCacheExtract)
			} else {
				require.ErrorIs(t, err, format.ErrCacheWrite)
				require.NotErrorIs(t, err, format.ErrCacheExtract)
			}
			var stat unix.Stat_t
			require.ErrorIs(t, unix.Fstat(directoryFD, &stat), unix.EBADF)
			if openedFD >= 0 {
				require.ErrorIs(t, unix.Fstat(openedFD, &stat), unix.EBADF)
			}
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Empty(t, entries, "failed materialization must remove temporary and invalid published files")
		})
	}
}

func TestCacheMaterializationCorruptionStages(t *testing.T) {
	for _, stage := range []string{format.StageCacheExtract, format.StageCacheCreateTemp} {
		t.Run(stage, func(t *testing.T) {
			for _, withPrimary := range []bool{false, true} {
				t.Run(strconv.FormatBool(withPrimary), func(t *testing.T) {
					f, entry, dir, payload := cacheFirstFixture(t)
					t.Setenv(format.EnvExecMode, format.ExecModeCache)
					t.Setenv(format.EnvLog, "json")
					t.Setenv(format.EnvDebug, "")
					hostile := bytes.Repeat([]byte{'x'}, len(payload))
					if stage == format.StageCacheExtract {
						require.NoError(t, os.WriteFile(f.Name(), hostile, 0o600))
					} else {
						oldOpen := openCachedBinaryAtFunc
						t.Cleanup(func() { openCachedBinaryAtFunc = oldOpen })
						openCachedBinaryAtFunc = func(dirFD int, name string) (int, error) {
							fd, err := oldOpen(dirFD, name)
							if err == nil {
								require.NoError(t, os.WriteFile(filepath.Join(dir, name), hostile, 0o700))
							}
							return fd, err
						}
					}
					logFile, err := os.CreateTemp(t.TempDir(), "telemetry")
					require.NoError(t, err)
					oldStderr := os.Stderr
					t.Cleanup(func() { os.Stderr = oldStderr; require.NoError(t, logFile.Close()) })
					os.Stderr = logFile
					var primary error
					if withPrimary {
						primary = format.ErrMemfdCreate
					}
					err = executeViaCache(f.Name(), f, entry, nil, []string{testAppArg}, nil,
						microarch.Info{}, microarch.PolicyResult{}, primary, time.Now())
					os.Stderr = oldStderr
					require.ErrorIs(t, err, format.ErrPayloadCorrupted)
					if stage == format.StageCacheExtract {
						require.ErrorIs(t, err, format.ErrCacheExtract)
						require.NotErrorIs(t, err, format.ErrCacheWrite)
					} else {
						require.ErrorIs(t, err, format.ErrCacheWrite)
						require.NotErrorIs(t, err, format.ErrCacheExtract)
					}
					if withPrimary {
						var dispatchErr *format.DispatchError
						require.ErrorAs(t, err, &dispatchErr)
						require.Len(t, dispatchErr.Attempts, 2)
						require.Equal(t, stage, dispatchErr.Attempts[1].Stage)
					}
					data, err := os.ReadFile(logFile.Name())
					require.NoError(t, err)
					var telemetry format.ErrorTelemetry
					require.NoError(t, json.Unmarshal(bytes.TrimSpace([]byte(strings.TrimPrefix(string(data), "[microfat] "))), &telemetry))
					require.Equal(t, stage, telemetry.Stage)
				})
			}
		})
	}
}
