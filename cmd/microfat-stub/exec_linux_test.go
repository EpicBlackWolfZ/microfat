//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testExecPathEnv = "PATH=/custom/bin"

func createTestVariantFile(t *testing.T, dir string, content []byte) (*format.VariantEntry, *os.File) {
	t.Helper()
	filePath := filepath.Join(dir, "test_container")
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0o755)
	require.NoError(t, err, "creating test variant file")

	var zstdBuf bytes.Buffer
	enc, err := zstd.NewWriter(&zstdBuf)
	require.NoError(t, err, "creating zstd writer")
	_, err = enc.Write(content)
	require.NoError(t, err, "writing to zstd writer")
	require.NoError(t, enc.Close(), "closing zstd writer")

	const offset = int64(100)
	_, err = f.WriteAt(zstdBuf.Bytes(), offset)
	require.NoError(t, err, "writing payload at offset")

	hash := sha256.Sum256(content)
	entry := &format.VariantEntry{
		Level:            "v1",
		Offset:           offset,
		CompressedSize:   int64(zstdBuf.Len()),
		UncompressedSize: int64(len(content)),
		SHA256:           hex.EncodeToString(hash[:]),
		Compression:      codec.AlgorithmZstd,
	}

	return entry, f
}

func TestLauncherOriginalExeWhitespace(t *testing.T) {
	wd, err := os.Getwd()
	require.NoError(t, err, "failed to get current working directory")

	entry := &format.VariantEntry{
		Level:            "v1",
		Offset:           0,
		CompressedSize:   500,
		UncompressedSize: 1000,
		SHA256:           "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v1"}
	policyRes := microarch.PolicyResult{}

	t.Run("BuildAutoTunedEnviron_WhitespacePreservation", func(t *testing.T) {
		tests := []struct {
			name        string
			selfPath    string
			baseEnv     []string
			wantOrigExe string
			wantPresent bool
		}{
			{
				name:        "OrdinaryFilename",
				selfPath:    "/usr/local/bin/microfat",
				baseEnv:     []string{testExecPathEnv},
				wantOrigExe: "/usr/local/bin/microfat",
				wantPresent: true,
			},
			{
				name:        "LeadingSpaceInBasename",
				selfPath:    "/usr/local/bin/ microfat",
				baseEnv:     []string{testExecPathEnv},
				wantOrigExe: "/usr/local/bin/ microfat",
				wantPresent: true,
			},
			{
				name:        "LeadingSpaceInRelativePath",
				selfPath:    " bin/microfat",
				baseEnv:     []string{testExecPathEnv},
				wantOrigExe: filepath.Join(wd, " bin", "microfat"),
				wantPresent: true,
			},
			{
				name:        "TrailingSpaceInBasename",
				selfPath:    "/usr/local/bin/microfat ",
				baseEnv:     []string{testExecPathEnv},
				wantOrigExe: "/usr/local/bin/microfat ",
				wantPresent: true,
			},
			{
				name:        "SpacesInParentDirectories",
				selfPath:    "/opt/my custom path/bin/microfat",
				baseEnv:     []string{testExecPathEnv},
				wantOrigExe: "/opt/my custom path/bin/microfat",
				wantPresent: true,
			},
			{
				name:        "TabInFilename",
				selfPath:    "/usr/local/bin/my\tfat",
				baseEnv:     []string{testExecPathEnv},
				wantOrigExe: "/usr/local/bin/my\tfat",
				wantPresent: true,
			},
			{
				name:        "NewlineInFilename",
				selfPath:    "/usr/local/bin/my\nfat",
				baseEnv:     []string{testExecPathEnv},
				wantOrigExe: "/usr/local/bin/my\nfat",
				wantPresent: true,
			},
			{
				name:        "NonemptyAllSpaceRelativePath",
				selfPath:    "   ",
				baseEnv:     []string{testExecPathEnv},
				wantOrigExe: filepath.Join(wd, "   "),
				wantPresent: true,
			},
			{
				name:        "EmptySelfPath_NotPresent",
				selfPath:    "",
				baseEnv:     []string{testExecPathEnv},
				wantPresent: false,
			},
			{
				name:        "ScrubSpoofedInheritedHint_WhenSelfPathEmpty",
				selfPath:    "",
				baseEnv:     []string{testExecPathEnv, format.EnvOriginalExe + "=/spoofed/fake"},
				wantPresent: false,
			},
			{
				name:        "ScrubSpoofedInheritedHint_WhenSelfPathGiven",
				selfPath:    "/usr/bin/real_app ",
				baseEnv:     []string{testExecPathEnv, format.EnvOriginalExe + "=/spoofed/fake"},
				wantOrigExe: "/usr/bin/real_app ",
				wantPresent: true,
			},
		}

		for _, tt := range tests {
			tc := tt
			t.Run(tc.name, func(t *testing.T) {
				env, _ := buildAutoTunedEnviron(tc.selfPath, tc.baseEnv, entry, format.ExecModeMemfd, hostInfo, policyRes)

				var gotOrigExe string
				var found bool
				seenCount := 0
				for _, e := range env {
					if strings.HasPrefix(e, format.EnvOriginalExe+"=") {
						seenCount++
						found = true
						gotOrigExe = strings.TrimPrefix(e, format.EnvOriginalExe+"=")
					}
				}

				if tc.wantPresent {
					require.True(t, found, "expected %s in env", format.EnvOriginalExe)
					require.Equal(t, 1, seenCount, "expected exactly 1 occurrence of %s", format.EnvOriginalExe)
					assert.Equal(t, tc.wantOrigExe, gotOrigExe)
					if strings.HasSuffix(tc.wantOrigExe, " ") {
						assert.True(t, strings.HasSuffix(gotOrigExe, " "), "trailing whitespace must be preserved")
					}
				} else {
					assert.False(t, found, "expected %s to NOT be present, got %q", format.EnvOriginalExe, gotOrigExe)
				}
			})
		}
	})

	t.Run("BuildAutoTunedEnviron_AbsErrorFallsBackToClean", func(t *testing.T) {
		origAbs := filepathAbsFunc
		defer func() { filepathAbsFunc = origAbs }()
		filepathAbsFunc = func(string) (string, error) {
			return "", errors.New("simulated abs error")
		}

		env, _ := buildAutoTunedEnviron(" ./path with space/app ", []string{testExecPathEnv}, entry, format.ExecModeMemfd, hostInfo, policyRes)
		var gotOrigExe string
		var found bool
		for _, e := range env {
			if strings.HasPrefix(e, format.EnvOriginalExe+"=") {
				found = true
				gotOrigExe = strings.TrimPrefix(e, format.EnvOriginalExe+"=")
			}
		}
		require.True(t, found, "expected %s in env", format.EnvOriginalExe)
		assert.Equal(t, filepath.Clean(" ./path with space/app "), gotOrigExe)
	})

	t.Run("ExecutionPropagation_WhitespacePreservedInExecve", func(t *testing.T) {
		tempDir := t.TempDir()
		testEntry, rawFile := createTestVariantFile(t, tempDir, []byte("EXEC_WHITESPACE_PAYLOAD"))
		defer func() { _ = rawFile.Close() }()

		privateCache := filepath.Join(tempDir, "isolated_cache")
		require.NoError(t, os.MkdirAll(privateCache, 0o700))
		t.Setenv(format.EnvCacheDir, privateCache)
		t.Setenv("XDG_CACHE_HOME", filepath.Join(tempDir, "xdg_cache"))

		origExecve := execveFunc
		defer func() { execveFunc = origExecve }()

		wsPath := filepath.Join(tempDir, "my app ")

		for _, mode := range []string{format.ExecModeMemfd, format.ExecModeCache} {
			t.Run(mode, func(t *testing.T) {
				var capturedEnv []string
				execveFunc = func(_ string, _ []string, envv []string) error {
					capturedEnv = envv
					return nil
				}

				var err error
				if mode == format.ExecModeMemfd {
					err = executeViaMemfd(wsPath, rawFile, testEntry, nil, []string{"--test"}, []string{testExecPathEnv}, hostInfo, policyRes, time.Now())
				} else {
					err = executeViaCache(
						wsPath, rawFile, testEntry, nil, []string{"--test"}, []string{testExecPathEnv}, hostInfo, policyRes, nil, time.Now(),
					)
				}
				require.NoError(t, err)

				var gotHint string
				var found bool
				for _, e := range capturedEnv {
					if strings.HasPrefix(e, format.EnvOriginalExe+"=") {
						found = true
						gotHint = strings.TrimPrefix(e, format.EnvOriginalExe+"=")
					}
				}
				require.True(t, found, "missing %s in execve environment", format.EnvOriginalExe)
				assert.Equal(t, wsPath, gotHint)
				assert.True(t, strings.HasSuffix(gotHint, " "), "trailing space must survive execution dispatch")
			})
		}
	})
}

func TestHandleCacheError_Branches(t *testing.T) {
	t.Parallel()

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{SelectedVariant: "v3", PolicyApplied: "direct match"}
	entry := &format.VariantEntry{Level: "v3"}

	err1 := handleCacheError(
		format.ErrExecve,
		format.StageCacheExec,
		errors.New("permission denied"),
		nil,
		format.ExecModeAuto,
		hostInfo,
		entry,
		policyRes,
		"/path/to/proc",
	)
	require.Error(t, err1)
	assert.Contains(t, err1.Error(), "cache execve failed (/path/to/proc)")

	err2 := handleCacheError(
		format.ErrCacheInit,
		format.StageCacheDirInit,
		errors.New("operation not permitted"),
		nil,
		format.ExecModeAuto,
		hostInfo,
		entry,
		policyRes,
		"cache directory creation failed",
	)
	require.Error(t, err2)
	assert.ErrorIs(t, err2, format.ErrCacheInit)

	// 3. With primaryErr != nil
	primaryErr := fmt.Errorf("%w: memfd failed", format.ErrMemfdCreate)
	err3 := handleCacheError(
		format.ErrCacheInit,
		format.StageCacheDirInit,
		syscall.EACCES,
		primaryErr,
		format.ExecModeAuto,
		hostInfo,
		entry,
		policyRes,
		"cache directory creation failed",
	)
	require.Error(t, err3)
	assert.ErrorIs(t, err3, format.ErrCacheInit)
}

func TestExecuteViaMemfdAndCache_ErrorHandling(t *testing.T) {
	tempDir := t.TempDir()
	entry, f := createTestVariantFile(t, tempDir, []byte("echo hi"))
	defer f.Close()

	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v1"}
	policyRes := microarch.PolicyResult{SelectedVariant: "v1"}

	// 1. executeViaMemfd failure with forced memfd vs auto mode
	oldMemfd := memfdCreateFunc
	memfdCreateFunc = func(name string, flags int) (int, error) {
		return -1, syscall.EPERM
	}
	t.Cleanup(func() { memfdCreateFunc = oldMemfd })

	t.Setenv(format.EnvExecMode, format.ExecModeAuto)
	errAuto := executeViaMemfd(
		f.Name(), f, entry, nil,
		[]string{testAppArg}, []string{testPathEnv},
		hostInfo, policyRes, time.Now(),
	)
	require.Error(t, errAuto)
	assert.ErrorIs(t, errAuto, format.ErrMemfdCreate)

	t.Setenv(format.EnvExecMode, format.ExecModeMemfd)
	errForced := executeViaMemfd(
		f.Name(), f, entry, nil,
		[]string{testAppArg}, []string{testPathEnv},
		hostInfo, policyRes, time.Now(),
	)
	require.Error(t, errForced)
	assert.ErrorIs(t, errForced, format.ErrMemfdCreate)
	assert.Contains(t, errForced.Error(), "forced memfd mode")

	// 2. executeViaCache with primary error and successful execve
	oldExecve := execveFunc
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		return nil
	}
	t.Cleanup(func() { execveFunc = oldExecve })

	primaryErr := fmt.Errorf("%w: memfd failed", format.ErrMemfdCreate)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tempDir, "cache"))

	errCacheSuccess := executeViaCache(
		f.Name(), f, entry, nil,
		[]string{testAppArg}, []string{testPathEnv},
		hostInfo, policyRes, primaryErr, time.Now(),
	)
	require.NoError(t, errCacheSuccess)

	// 3. executeViaCache with execve failure
	execveFunc = func(argv0 string, argv []string, envv []string) error {
		return syscall.EACCES
	}
	errExecFail := executeViaCache(
		f.Name(), f, entry, nil,
		[]string{testAppArg}, []string{testPathEnv},
		hostInfo, policyRes, primaryErr, time.Now(),
	)
	require.Error(t, errExecFail)
	assert.ErrorIs(t, errExecFail, format.ErrExecve)

	// 4. executeViaCache with invalid variant checksum
	invalidEntry := &format.VariantEntry{
		Level:  "v1",
		SHA256: "invalid-sha",
	}
	errBadChecksum := executeViaCache(
		f.Name(), f, invalidEntry, nil,
		[]string{testAppArg}, []string{testPathEnv},
		hostInfo, policyRes, primaryErr, time.Now(),
	)
	require.Error(t, errBadChecksum)
	assert.ErrorIs(t, errBadChecksum, format.ErrCacheWrite)

	// 5. executeViaCache with forbidden cache directories
	t.Setenv("XDG_CACHE_HOME", "/dev/null/forbidden_primary")
	t.Setenv("TMPDIR", "/dev/null/forbidden_secondary")
	errNoCache := executeViaCache(
		f.Name(), f, entry, nil,
		[]string{testAppArg}, []string{testPathEnv},
		hostInfo, policyRes, primaryErr, time.Now(),
	)
	require.Error(t, errNoCache)
	assert.ErrorIs(t, errNoCache, format.ErrCacheInit)
}

func TestErrorTelemetryFormatting(t *testing.T) {
	t.Parallel()

	tel := format.ErrorTelemetry{
		Event:             format.EventError,
		TimestampUnixNano: 123456789,
		HostArch:          testArchAMD64,
		HostLevel:         "v3",
		SelectedVariant:   "v3",
		PolicyApplied:     "safe_avx512",
		PolicyReason:      "downclock_risk",
		RequestedMode:     "auto",
		AttemptedMode:     "memfd",
		Stage:             format.StageMemfdCreate,
		Error:             "operation not permitted",
		Errno:             1,
		ErrnoName:         "EPERM",
		Attempts: []format.ExecutionAttempt{
			{
				Stage:         format.StageMemfdCreate,
				RequestedMode: "auto",
				AttemptedMode: "memfd",
				Error:         "operation not permitted",
				Errno:         1,
				ErrnoName:     "EPERM",
			},
			{
				Stage:         format.StageCacheDirInit,
				RequestedMode: "auto",
				AttemptedMode: "cache",
				Error:         "permission denied",
				Errno:         13,
				ErrnoName:     "EACCES",
			},
		},
		Details: "falling back to cache",
		Hint:    format.HintMemfdEPERM,
	}

	raw := formatErrorTelemetryJSON(tel)
	require.NotEmpty(t, raw)
	assert.Contains(t, raw, `"requested_mode":"auto"`)
	assert.Contains(t, raw, `"attempted_mode":"memfd"`)
	assert.Contains(t, raw, `"errno":1`)
	assert.Contains(t, raw, `"errno_name":"EPERM"`)
	assert.Contains(t, raw, `"attempts":[`)
	assert.Contains(t, raw, `"stage":"memfd_create"`)
	assert.Contains(t, raw, `"stage":"cache_dir_init"`)
}

func TestCombinedDispatchError(t *testing.T) {
	t.Parallel()

	primaryErr := fmt.Errorf("%w: memfd_create failed: %w", format.ErrMemfdCreate, syscall.EPERM)
	cacheErr := fmt.Errorf("%w: cache dir init failed: %w", format.ErrCacheInit, syscall.EACCES)

	dispErr := buildCombinedDispatchError(format.ErrCacheInit, format.ExecModeAuto, primaryErr, format.StageCacheDirInit, cacheErr)
	require.NotNil(t, dispErr)

	// Check Is and Unwrap
	assert.True(t, errors.Is(dispErr, format.ErrCacheInit))
	assert.Equal(t, format.ErrCacheInit, errors.Unwrap(dispErr))

	// Check As
	var target *format.DispatchError
	assert.True(t, errors.As(dispErr, &target))
	assert.Equal(t, format.ExecModeAuto, target.RequestedMode)
	require.Len(t, target.Attempts, 2)
	assert.Equal(t, format.ExecModeMemfd, target.Attempts[0].AttemptedMode)
	assert.Equal(t, format.ExecModeCache, target.Attempts[1].AttemptedMode)
	assert.Equal(t, 1, target.Attempts[0].Errno)
	assert.Equal(t, "EPERM", target.Attempts[0].ErrnoName)
	assert.Equal(t, 13, target.Attempts[1].Errno)
	assert.Equal(t, "EACCES", target.Attempts[1].ErrnoName)

	// Check error string contains both attempts and primary memfd error
	errStr := dispErr.Error()
	assert.Contains(t, errStr, "primary memfd error")
	assert.Contains(t, errStr, "attempt 1: memfd")
	assert.Contains(t, errStr, "attempt 2: cache")

	// Check empty summary fallback
	emptyDispErr := &format.DispatchError{PrimarySentinel: format.ErrExecve}
	assert.Contains(t, emptyDispErr.Error(), format.ErrExecve.Error())
	assert.Contains(t, emptyDispErr.Error(), "dispatch failed")

	// Check all switch branches in buildCombinedDispatchError:
	// 1. ErrExecve -> StageMemfdExec
	dExec := buildCombinedDispatchError(
		format.ErrExecve, format.ExecModeAuto,
		fmt.Errorf("%w: failed", format.ErrExecve),
		format.StageCacheExec, cacheErr,
	)
	assert.Equal(t, format.StageMemfdExec, dExec.Attempts[0].Stage)

	// 2. ErrMemfdSealingFailed -> StageMemfdSeal
	dSeal := buildCombinedDispatchError(
		format.ErrMemfdSealingFailed, format.ExecModeAuto,
		fmt.Errorf("%w: failed", format.ErrMemfdSealingFailed),
		format.StageCacheDirInit, cacheErr,
	)
	assert.Equal(t, format.StageMemfdSeal, dSeal.Attempts[0].Stage)

	// 3. ErrMemfdExtract -> StageMemfdExtract
	dExt := buildCombinedDispatchError(
		format.ErrMemfdExtract, format.ExecModeAuto,
		fmt.Errorf("%w: failed", format.ErrMemfdExtract),
		format.StageCacheDirInit, cacheErr,
	)
	assert.Equal(t, format.StageMemfdExtract, dExt.Attempts[0].Stage)
}

func TestExtractErrno(t *testing.T) {
	t.Parallel()

	num, name := format.ExtractErrno(syscall.ENOENT)
	assert.Equal(t, 2, num)
	assert.Equal(t, "ENOENT", name)

	num, name = format.ExtractErrno(fmt.Errorf("wrapped: %w", syscall.EACCES))
	assert.Equal(t, 13, num)
	assert.Equal(t, "EACCES", name)

	num, name = format.ExtractErrno(errors.New("generic error without errno"))
	assert.Equal(t, 0, num)
	assert.Empty(t, name)

	num, name = format.ExtractErrno(nil)
	assert.Equal(t, 0, num)
	assert.Empty(t, name)
}

func TestLogErrorDiagnosticsBranches(t *testing.T) {
	hostInfo := microarch.Info{Arch: testArchAMD64, Level: "v3"}
	policyRes := microarch.PolicyResult{SelectedVariant: "v3", PolicyApplied: "direct match"}
	entry := &format.VariantEntry{Level: "v3"}

	t.Run("JSON_Logging", func(t *testing.T) {
		t.Setenv(format.EnvLog, "json")

		// With entry and attempts
		dispErr := buildCombinedDispatchError(
			format.ErrCacheInit, format.ExecModeAuto,
			fmt.Errorf("%w: %w", format.ErrMemfdCreate, syscall.EPERM),
			format.StageCacheDirInit,
			fmt.Errorf("%w: %w", format.ErrCacheInit, syscall.EACCES),
		)
		logErrorDiagnostics(
			format.StageCacheDirInit,
			dispErr,
			hostInfo,
			entry,
			policyRes,
			"details message",
		)

		// Without entry and with non-dispatch error
		logErrorDiagnostics(
			format.StageMemfdCreate,
			fmt.Errorf("%w: %w", format.ErrMemfdCreate, syscall.EPERM),
			hostInfo,
			nil,
			policyRes,
			"",
		)
	})

	t.Run("Debug_Logging", func(t *testing.T) {
		t.Setenv(format.EnvDebug, "1")
		t.Setenv(format.EnvLog, "")

		logErrorDiagnostics(
			format.StageMemfdCreate,
			fmt.Errorf("%w: %w", format.ErrMemfdCreate, syscall.EPERM),
			hostInfo,
			entry,
			policyRes,
			"",
		)
	})
}
