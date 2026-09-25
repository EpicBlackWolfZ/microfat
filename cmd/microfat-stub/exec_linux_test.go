//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	hostInfo := microarch.Info{Arch: "amd64", Level: "v1"}
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

	hostInfo := microarch.Info{Arch: "amd64", Level: "v3"}
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
}
