//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	json "encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/cache"
	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const cacheFirstCorruption = "corruption"
const cacheFirstSymlink = "symlink"

func cacheFirstFixture(t *testing.T) (*os.File, *format.VariantEntry, string, []byte) {
	t.Helper()
	oldExisting, oldResolve := resolveExistingCacheDirFunc, resolveCacheDirFunc
	oldCreate, oldSeal, oldExec, oldLimits := memfdCreateFunc, memfdSealFunc, execveFunc, readCgroupLimitsFunc
	t.Cleanup(func() {
		resolveExistingCacheDirFunc, resolveCacheDirFunc = oldExisting, oldResolve
		memfdCreateFunc, memfdSealFunc, execveFunc, readCgroupLimitsFunc = oldCreate, oldSeal, oldExec, oldLimits
	})
	t.Setenv(format.EnvExecMode, "")
	t.Setenv(format.EnvDispatchMode, "")
	readCgroupLimitsFunc = func() (cgroup.Limits, error) { return cgroup.Limits{}, nil }
	dir := t.TempDir()
	resolveExistingCacheDirFunc = func(string) (int, string, error) {
		fd, err := format.OpenAndValidateCacheDirFD(dir, false)
		return fd, dir, err
	}
	resolveCacheDirFunc = func(string) (int, string, error) {
		fd, err := format.OpenAndValidateCacheDirFD(dir, false)
		return fd, dir, err
	}
	payload := []byte("cache-first verified payload")
	source := filepath.Join(t.TempDir(), "source")
	require.NoError(t, os.WriteFile(source, payload, 0o600))
	f, err := os.Open(source)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, f.Close()) })
	hash := sha256.Sum256(payload)
	entry := &format.VariantEntry{Level: "v1", Compression: codec.AlgorithmNone, CompressedSize: int64(len(payload)),
		UncompressedSize: int64(len(payload)), SHA256: hex.EncodeToString(hash[:])}
	return f, entry, dir, payload
}

func dispatchCacheFirst(f *os.File, entry *format.VariantEntry) error {
	return executeVariant(f.Name(), f, entry, nil, []string{"app", "argument"}, []string{"GOMAXPROCS=2"},
		microarch.Info{}, microarch.PolicyResult{}, time.Now())
}

func TestCacheFirstVerifiedHitPinsDescriptor(t *testing.T) {
	for _, verify := range []string{"0", "false", "1"} {
		t.Run(verify, func(t *testing.T) {
			f, entry, dir, payload := cacheFirstFixture(t)
			t.Setenv("MICROFAT_VERIFY_CACHE", verify)
			path := filepath.Join(dir, entry.SHA256)
			require.NoError(t, os.WriteFile(path, payload, 0o500))
			require.NoError(t, os.Chmod(dir, 0o500))
			t.Cleanup(func() { require.NoError(t, os.Chmod(dir, 0o700)) })
			memfdCreateFunc = func(string, int) (int, error) { t.Fatal("warm hit must bypass memfd"); return -1, nil }
			resolveCacheDirFunc = func(string) (int, string, error) { t.Fatal("hit must not materialize"); return -1, "", nil }
			// Warm dispatch verifies the cached bytes without reading the compressed source.
			require.NoError(t, os.WriteFile(f.Name(), []byte("damaged source"), 0o600))
			var executedFD int
			execveFunc = func(path string, args, env []string) error {
				require.Equal(t, []string{"app", "argument"}, args)
				require.Contains(t, env, format.EnvExecMode+"=cache")
				require.Contains(t, env, "GOMAXPROCS=2")
				fd, err := strconv.Atoi(strings.TrimPrefix(path, "/proc/self/fd/"))
				require.NoError(t, err)
				executedFD = fd
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, payload, data)
				return nil
			}
			require.NoError(t, dispatchCacheFirst(f, entry))
			var stat unix.Stat_t
			require.ErrorIs(t, unix.Fstat(executedFD, &stat), unix.EBADF)
		})
	}

	t.Run("pathname replacement cannot redirect exec", func(t *testing.T) {
		f, entry, dir, payload := cacheFirstFixture(t)
		path := filepath.Join(dir, entry.SHA256)
		require.NoError(t, os.WriteFile(path, payload, 0o700))
		execveFunc = func(procPath string, _, _ []string) error {
			require.NoError(t, os.Rename(path, path+".old"))
			require.NoError(t, os.WriteFile(path, []byte("replacement"), 0o700))
			data, err := os.ReadFile(procPath)
			require.NoError(t, err)
			require.Equal(t, payload, data)
			return nil
		}
		require.NoError(t, dispatchCacheFirst(f, entry))
	})
}

func TestCacheFirstMissDoesNotMutate(t *testing.T) {
	for _, state := range []string{"absent directory", "absent entry", "wrong size", "wrong digest", "directory denied"} {
		t.Run(state, func(t *testing.T) {
			f, entry, dir, payload := cacheFirstFixture(t)
			path := filepath.Join(dir, entry.SHA256)
			var corrupt []byte
			switch state {
			case "absent directory":
				dir = filepath.Join(dir, "absent")
				resolveExistingCacheDirFunc = format.ResolveExistingCacheDirFD
				t.Setenv(format.EnvCacheDir, dir)
			case "wrong size":
				corrupt = []byte("short")
			case "wrong digest":
				corrupt = []byte(strings.Repeat("x", len(payload)))
			case "directory denied":
				resolveExistingCacheDirFunc = func(string) (int, string, error) { return -1, "", unix.EACCES }
			}
			if corrupt != nil {
				require.NoError(t, os.WriteFile(path, corrupt, 0o700))
			}
			resolveCacheDirFunc = func(string) (int, string, error) {
				t.Fatal("successful memfd must not materialize")
				return -1, "", nil
			}
			execveFunc = func(path string, _, env []string) error {
				require.Contains(t, env, format.EnvExecMode+"=memfd")
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, payload, data)
				return nil
			}
			require.NoError(t, dispatchCacheFirst(f, entry))
			if state == "absent directory" {
				_, err := os.Stat(dir)
				require.ErrorIs(t, err, os.ErrNotExist)
			} else if corrupt != nil {
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, corrupt, data)
			}
		})
	}
}

func TestCacheFirstRejectsUnsafeEntries(t *testing.T) {
	for _, state := range []string{cacheFirstSymlink, "fifo", "directory", "writable", "setuid"} {
		t.Run(state, func(t *testing.T) {
			f, entry, dir, payload := cacheFirstFixture(t)
			path := filepath.Join(dir, entry.SHA256)
			switch state {
			case cacheFirstSymlink:
				require.NoError(t, os.Symlink(f.Name(), path))
			case "fifo":
				require.NoError(t, unix.Mkfifo(path, 0o700))
			case "directory":
				require.NoError(t, os.Mkdir(path, 0o700))
			default:
				require.NoError(t, os.WriteFile(path, payload, 0o700))
				mode := uint32(0o722)
				if state == "setuid" {
					mode = 0o4700
				}
				require.NoError(t, unix.Chmod(path, mode))
			}
			memfdCreateFunc = func(string, int) (int, error) { t.Fatal("unsafe hit must not fallback"); return -1, nil }
			execveFunc = func(string, []string, []string) error { t.Fatal("unsafe hit must not execute"); return nil }
			require.ErrorIs(t, dispatchCacheFirst(f, entry), format.ErrCacheWrite)
			_, err := os.Lstat(path)
			require.NoError(t, err, "unsafe entries must not be removed")
		})
	}
}

func TestCacheFirstWarmDenialAttempts(t *testing.T) {
	for _, failure := range []string{"success", "create", "seal", "exec", cacheFirstCorruption} {
		t.Run(failure, func(t *testing.T) {
			f, entry, dir, payload := cacheFirstFixture(t)
			require.NoError(t, os.WriteFile(filepath.Join(dir, entry.SHA256), payload, 0o700))
			resolveCacheDirFunc = func(string) (int, string, error) {
				t.Fatal("denied warm exec must not retry cache")
				return -1, "", nil
			}
			if failure == "create" {
				memfdCreateFunc = func(string, int) (int, error) { return -1, unix.EPERM }
			}
			if failure == "seal" {
				memfdSealFunc = func(int, int) error { return unix.EPERM }
			}
			if failure == cacheFirstCorruption {
				require.NoError(t, os.WriteFile(f.Name(), []byte(strings.Repeat("x", len(payload))), 0o600))
			}
			calls := 0
			execveFunc = func(_ string, _, env []string) error {
				calls++
				if calls == 1 {
					require.Contains(t, env, format.EnvExecMode+"=cache")
					return unix.EACCES
				}
				require.Contains(t, env, format.EnvExecMode+"=memfd")
				if failure == "exec" {
					return unix.ENOEXEC
				}
				return nil
			}
			err := dispatchCacheFirst(f, entry)
			if failure == "success" {
				require.NoError(t, err)
				require.Equal(t, 2, calls)
				return
			}
			var dispatchErr *format.DispatchError
			require.ErrorAs(t, err, &dispatchErr)
			require.Len(t, dispatchErr.Attempts, 2)
			require.Equal(t, format.ExecModeCache, dispatchErr.Attempts[0].AttemptedMode)
			require.Equal(t, format.ExecModeMemfd, dispatchErr.Attempts[1].AttemptedMode)
			require.ErrorIs(t, err, unix.EACCES)
			if failure == cacheFirstCorruption {
				require.ErrorIs(t, err, format.ErrPayloadCorrupted)
			}
		})
	}
}

func TestCacheFirstColdFallback(t *testing.T) {
	for _, failure := range []string{"create", "seal", "exec", cacheFirstCorruption, "invalid checksum"} {
		t.Run(failure, func(t *testing.T) {
			f, entry, dir, payload := cacheFirstFixture(t)
			switch failure {
			case "create":
				memfdCreateFunc = func(string, int) (int, error) { return -1, unix.EPERM }
			case "seal":
				memfdSealFunc = func(int, int) error { return unix.EPERM }
			case cacheFirstCorruption:
				require.NoError(t, os.WriteFile(f.Name(), []byte(strings.Repeat("x", len(payload))), 0o600))
			case "invalid checksum":
				entry.SHA256 = "../unsafe"
			}
			execveFunc = func(_ string, _, env []string) error {
				if failure == "exec" && !strings.Contains(strings.Join(env, "\n"), format.EnvExecMode+"=cache") {
					return unix.EACCES
				}
				require.Contains(t, env, format.EnvExecMode+"=cache")
				return nil
			}
			err := dispatchCacheFirst(f, entry)
			if failure == cacheFirstCorruption || failure == "invalid checksum" {
				require.Error(t, err)
				require.True(t, isPayloadCorruptionOrDecompressionError(err))
				entries, readErr := os.ReadDir(dir)
				require.NoError(t, readErr)
				require.Empty(t, entries)
				return
			}
			require.NoError(t, err)
			fd, err := cache.OpenAndValidateVariantFD(filepath.Join(dir, entry.SHA256), entry, false)
			require.NoError(t, err)
			require.NoError(t, unix.Close(fd))
		})
	}
}

func TestCacheFirstExplicitMemfdBypassesLookup(t *testing.T) {
	f, entry, _, _ := cacheFirstFixture(t)
	t.Setenv(format.EnvExecMode, format.ExecModeMemfd)
	resolveExistingCacheDirFunc = func(string) (int, string, error) { t.Fatal("explicit memfd must bypass lookup"); return -1, "", nil }
	memfdSealFunc = func(int, int) error { return unix.EPERM }
	err := dispatchCacheFirst(f, entry)
	require.ErrorIs(t, err, format.ErrMemfdSealingFailed)
	require.False(t, errors.Is(err, format.ErrCacheWrite))
}

func TestCacheFirstErrorTelemetryUsesFinalAttempt(t *testing.T) {
	for _, warm := range []bool{true, false} {
		t.Run(strconv.FormatBool(warm), func(t *testing.T) {
			f, entry, dir, payload := cacheFirstFixture(t)
			if warm {
				require.NoError(t, os.WriteFile(filepath.Join(dir, entry.SHA256), payload, 0o700))
			}
			logFile, err := os.CreateTemp(t.TempDir(), "telemetry")
			require.NoError(t, err)
			oldStderr := os.Stderr
			t.Cleanup(func() { os.Stderr = oldStderr; require.NoError(t, logFile.Close()) })
			os.Stderr = logFile
			t.Setenv(format.EnvLog, "json")
			t.Setenv(format.EnvDebug, "")
			memfdCreateFunc = func(string, int) (int, error) { return -1, unix.EPERM }
			execveFunc = func(string, []string, []string) error { return unix.EACCES }
			err = dispatchCacheFirst(f, entry)
			require.ErrorIs(t, err, unix.EPERM)
			require.ErrorIs(t, err, unix.EACCES)
			os.Stderr = oldStderr
			data, readErr := os.ReadFile(logFile.Name())
			require.NoError(t, readErr)
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			var telemetry format.ErrorTelemetry
			require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(lines[len(lines)-1], "[microfat] ")), &telemetry))
			require.Len(t, telemetry.Attempts, 2)
			if warm {
				require.Equal(t, format.StageMemfdCreate, telemetry.Stage)
				require.Equal(t, format.ExecModeMemfd, telemetry.AttemptedMode)
				require.Equal(t, "EPERM", telemetry.ErrnoName)
			} else {
				require.Equal(t, format.StageCacheExec, telemetry.Stage)
				require.Equal(t, format.ExecModeCache, telemetry.AttemptedMode)
				require.Equal(t, "EACCES", telemetry.ErrnoName)
			}
		})
	}
}
