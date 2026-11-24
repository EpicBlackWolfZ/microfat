//go:build linux

package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/runtimeinfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	envRustStubPath               = "MICROFAT_RUST_STUB_PATH"
	envRustStubTests              = "MICROFAT_RUST_STUB_TESTS"
	rustStubRequiredValue         = "required"
	rustStubPrivilegedTimeout     = 30 * time.Second
	rustStubWhitespaceTimeout     = 10 * time.Second
	rustStubReadOnlyMode          = 0o500
	rustStubNonExecMode           = 0o600
	rustStubInsecureMode          = 0o777
	rustStubCorruptByteOffset     = 512
	rustStubCorruptMarkerByte0    = 0xDE
	rustStubCorruptMarkerByte1    = 0xAD
	rustStubCorruptMarkerByte2    = 0xBE
	rustStubCorruptMarkerByte3    = 0xEF
	rustStubPayloadTag            = "golden:variant="
	rustStubExecModeMemfdEnv      = "MICROFAT_EXEC_MODE=memfd"
	rustStubForceLevelEnvPrefix   = "MICROFAT_FORCE_LEVEL="
	rustStubMaxLevelEnvPrefix     = "MICROFAT_MAX_LEVEL="
	rustStubDisableVariantsPrefix = "MICROFAT_DISABLE_VARIANTS="
	rustStubInfoFlag              = "--microfat:info"
	rustStubMinimalRefusalMsg     = "meta-commands are disabled in minimal launcher stub profile"
	rustStubZstdMagicByte0        = 0x28
	rustStubZstdMagicByte1        = 0xB5
	rustStubZstdMagicByte2        = 0x2F
	rustStubZstdMagicByte3        = 0xFD
	rustStubZstdFHDWindowOnly     = 0x00
	rustStubZstdWindow256MiB      = 0x90
	rustStubPrewarmFlag           = "--microfat:" + "prewarm"
	rustStubTrimFlag              = "--microfat:" + "trim"
	rustStubTier2ARM64            = "v8." + "2"
	rustStubStateAbsent           = "ab" + "sent"
	rustStubDamageSize            = "si" + "ze"
	rustStubVerifyFalse           = "fal" + "se"
	rustStubKindDir               = "direc" + "tory"
	rustStubModeSetuid            = "set" + "uid"
	rustStubZstdDictLabel         = "zstd-" + "dictionary"
)

func requireRustStubPath(t *testing.T) string {
	t.Helper()
	path := strings.TrimSpace(os.Getenv(envRustStubPath))
	if path == "" {
		if strings.EqualFold(strings.TrimSpace(os.Getenv(envRustStubTests)), rustStubRequiredValue) {
			t.Fatalf("%s must be set when %s=%s", envRustStubPath, envRustStubTests, rustStubRequiredValue)
		}
		t.Skip("set MICROFAT_RUST_STUB_PATH (or run task test-rust-stub-e2e) to run microfat-stub-rs E2E parity suite")
	}
	info, err := os.Stat(path)
	require.NoError(t, err, "stat rust stub %s", path)
	require.True(t, info.Mode().IsRegular(), "rust stub %s must be a regular file", path)
	return path
}

func packRustStubGolden(t *testing.T, rustStub, dir, name string, enableDict bool, compression string) string {
	t.Helper()
	out := filepath.Join(dir, name)
	args := []string{
		inputPackCommand,
		mountArchFlag, currentHostArch,
		inputStubFlag, rustStub,
		"--name", name,
		"-o", out,
	}
	if compression != "" {
		args = append(args, creationEpochCompressionFlag, compression)
	}
	if enableDict {
		args = append(args, "--dict")
	}
	for _, level := range creationEpochLevels() {
		args = append(args, "-v", level+"="+goldenVariantBins[level])
	}
	creationEpochCommand(t, nil, args...)
	verifyFatIntegrity(t, out)
	return out
}

func rustStubBaseLevel() string {
	if currentHostArch == archARM64 {
		return manifestARM64Base
	}
	return "v1"
}

func TestRustStubMinimalProfileMetaCommandRefusal(t *testing.T) {
	t.Parallel()
	rustStub := requireRustStubPath(t)
	fat := packRustStubGolden(t, rustStub, t.TempDir(), "rust-meta.fat", false, "")

	for _, cmdArg := range []string{rustStubInfoFlag, stubInfoJSONFlag, rustStubPrewarmFlag, rustStubTrimFlag} {
		t.Run("normal/"+cmdArg, func(t *testing.T) {
			t.Parallel()
			cacheDir := filepath.Join(t.TempDir(), "cache")
			stdout, stderr, exitCode, err := executeFatBinary(t, fat, cacheFirstEnv(cacheDir), cmdArg)
			require.Error(t, err)
			require.NotEqual(t, defaultExitCode, exitCode)
			require.NotContains(t, stdout, rustStubPayloadTag)
			require.Contains(t, stderr, rustStubMinimalRefusalMsg)
		})

		t.Run("selection-failure/"+cmdArg, func(t *testing.T) {
			t.Parallel()
			cacheDir := filepath.Join(t.TempDir(), "cache")
			env := append(cacheFirstEnv(cacheDir), rustStubForceLevelEnvPrefix+"v99")
			stdout, stderr, exitCode, err := executeFatBinary(t, fat, env, cmdArg)
			require.Error(t, err)
			require.NotEqual(t, defaultExitCode, exitCode)
			require.NotContains(t, stdout, rustStubPayloadTag)
			require.Contains(t, stderr, rustStubMinimalRefusalMsg)
		})
	}
}

func TestRustStubVariantSelectionAndPolicy(t *testing.T) {
	t.Parallel()
	rustStub := requireRustStubPath(t)
	fat := packRustStubGolden(t, rustStub, t.TempDir(), "rust-policy.fat", false, "")

	t.Run("BaselineHostSelection", func(t *testing.T) {
		t.Parallel()
		stdout, stderr, exitCode, err := executeFatBinary(t, fat, []string{envDebugTrue})
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)
	})

	t.Run("SafeAVX512Policy", func(t *testing.T) {
		t.Parallel()
		stdout, stderr, exitCode, err := executeFatBinary(t, fat, []string{"MICROFAT_POLICY=safe_avx512", envDebugTrue})
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		if currentHostArch == archAMD64 && currentHostLevel == "v4" && strings.Contains(stderr, "avx512_downclock_protection") {
			assertSelectedMatchesExecuted(t, stdout, stderr, "v3")
			return
		}
		assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)
	})

	t.Run("ForcedLevelBaseline", func(t *testing.T) {
		t.Parallel()
		base := rustStubBaseLevel()
		stdout, stderr, exitCode, err := executeFatBinary(t, fat, []string{rustStubForceLevelEnvPrefix + base, envDebugTrue})
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		assertSelectedMatchesExecuted(t, stdout, stderr, base)
	})

	t.Run("ForcedLevelIncompatible", func(t *testing.T) {
		t.Parallel()
		stdout, stderr, exitCode, err := executeFatBinary(t, fat, []string{rustStubForceLevelEnvPrefix + "v99", envDebugTrue})
		require.Error(t, err)
		require.NotEqual(t, defaultExitCode, exitCode)
		require.NotContains(t, stdout, rustStubPayloadTag)
		assert.True(t, strings.Contains(stderr, "incompatible") || strings.Contains(stderr, "unknown forced level"),
			"expected incompatible/unknown forced level error in stderr: %s", stderr)
	})

	t.Run("MaxLevelCapBaseline", func(t *testing.T) {
		t.Parallel()
		base := rustStubBaseLevel()
		stdout, stderr, exitCode, err := executeFatBinary(t, fat, []string{rustStubMaxLevelEnvPrefix + base, envDebugTrue})
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		assertSelectedMatchesExecuted(t, stdout, stderr, base)
	})

	t.Run("MaxLevelCapTier2", func(t *testing.T) {
		t.Parallel()
		if !hostSupportsAtLeastTier2() {
			t.Skipf("host level %s is baseline tier, skipping tier-2 max level test", currentHostLevel)
		}
		targetCap := "v2"
		if currentHostArch == archARM64 {
			targetCap = rustStubTier2ARM64
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, fat, []string{rustStubMaxLevelEnvPrefix + targetCap, envDebugTrue})
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		assertSelectedMatchesExecuted(t, stdout, stderr, targetCap)
	})

	t.Run("DisabledVariantsFallbackToTier2", func(t *testing.T) {
		t.Parallel()
		if !hostSupportsAtLeastTier2() {
			t.Skipf("host level %s is baseline tier, skipping tier-2 fallback test", currentHostLevel)
		}
		targetFallback, disabledVal := "v2", "v3,v4"
		if currentHostArch == archARM64 {
			targetFallback, disabledVal = rustStubTier2ARM64, "v9.0"
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, fat, []string{rustStubDisableVariantsPrefix + disabledVal, envDebugTrue})
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		assertSelectedMatchesExecuted(t, stdout, stderr, targetFallback)
	})

	t.Run("DisabledVariantsFallbackToBaseline", func(t *testing.T) {
		t.Parallel()
		targetFallback, disabledVal := "v1", "v2,v3,v4"
		if currentHostArch == archARM64 {
			targetFallback, disabledVal = manifestARM64Base, "v8.2,v9.0"
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, fat, []string{rustStubDisableVariantsPrefix + disabledVal, envDebugTrue})
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		assertSelectedMatchesExecuted(t, stdout, stderr, targetFallback)
	})

	t.Run("AllVariantsDisabled", func(t *testing.T) {
		t.Parallel()
		disabledVal := "v1,v2,v3,v4"
		if currentHostArch == archARM64 {
			disabledVal = "v8.0,v8.2,v9.0"
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, fat, []string{rustStubDisableVariantsPrefix + disabledVal, envDebugTrue})
		require.Error(t, err)
		require.NotEqual(t, defaultExitCode, exitCode)
		require.NotContains(t, stdout, rustStubPayloadTag)
		require.Contains(t, stderr, "no compatible microarchitecture variant found")
	})
}

func rewriteRustStubIndexWithValidTrailer(t *testing.T, fatPath string, idx *format.Index, indexOffset int64) {
	t.Helper()
	f, err := os.OpenFile(fatPath, os.O_RDWR, 0)
	require.NoError(t, err, "open %s", fatPath)
	require.NoError(t, f.Truncate(indexOffset), "truncate %s", fatPath)
	_, err = f.Seek(indexOffset, ioSeekStart)
	require.NoError(t, err, "seek %s", fatPath)
	_, err = format.WriteIndexAndTrailer(f, idx, indexOffset)
	require.NoError(t, err, "write index/trailer %s", fatPath)
	require.NoError(t, f.Sync())
	require.NoError(t, f.Close())
	const delayedFputGrace = 10 * time.Millisecond
	time.Sleep(delayedFputGrace)
}

func TestRustStubCorruptionAndSecurityBoundary(t *testing.T) {
	t.Parallel()
	rustStub := requireRustStubPath(t)
	fixtureDir := t.TempDir()
	baseFat := packRustStubGolden(t, rustStub, fixtureDir, "rust-corrupt-base.fat", false, mountCodecZstd)
	dictFat := packRustStubGolden(t, rustStub, fixtureDir, "rust-corrupt-dict.fat", true, mountCodecZstd)

	t.Run("Scenario10_MutatedMagicTrailer", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "corrupt_magic.fat")
		fileSize := copyFile(t, baseFat, corruptPath)
		mutateFileBytes(t, corruptPath, fileSize-trailerMagicSizeBytes, []byte(bogusMagicString))
		stdout, stderr, exitCode, err := executeFatBinary(t, corruptPath, nil)
		require.Error(t, err)
		require.NotEqual(t, defaultExitCode, exitCode)
		require.NotContains(t, stdout, rustStubPayloadTag)
		assert.True(t, strings.Contains(stderr, "missing magic trailer") || strings.Contains(stderr, "not a valid microfat fat binary"),
			"unexpected stderr: %s", stderr)
	})

	t.Run("Scenario11_MutatedIndexOffset", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "corrupt_offset.fat")
		fileSize := copyFile(t, baseFat, corruptPath)
		buf := make([]byte, format.OffsetLen)
		binary.LittleEndian.PutUint64(buf, uint64(fileSize+invalidOffsetBeyondEOF))
		mutateFileBytes(t, corruptPath, fileSize-trailerOffsetIndexOffset, buf)
		stdout, stderr, exitCode, err := executeFatBinary(t, corruptPath, nil)
		require.Error(t, err)
		require.NotEqual(t, defaultExitCode, exitCode)
		require.NotContains(t, stdout, rustStubPayloadTag)
		assert.True(t, strings.Contains(stderr, "beyond trailer") || strings.Contains(stderr, "reading binary manifest"),
			"unexpected stderr: %s", stderr)
	})

	t.Run("Scenario12_MutatedIndexSize", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "corrupt_size.fat")
		fileSize := copyFile(t, baseFat, corruptPath)
		buf := make([]byte, format.SizeLen)
		binary.LittleEndian.PutUint64(buf, invalidSmallSize)
		mutateFileBytes(t, corruptPath, fileSize-trailerOffsetIndexSize, buf)
		stdout, stderr, exitCode, err := executeFatBinary(t, corruptPath, nil)
		require.Error(t, err)
		require.NotEqual(t, defaultExitCode, exitCode)
		require.NotContains(t, stdout, rustStubPayloadTag)
		assert.True(t, strings.Contains(stderr, "does not match trailer boundary") || strings.Contains(stderr, "reading binary manifest"),
			"unexpected stderr: %s", stderr)
	})

	t.Run("Scenario13_MutatedIndexBytes", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "corrupt_index.fat")
		_ = copyFile(t, baseFat, corruptPath)
		trailer, _ := readTrailerAndIndex(t, corruptPath)
		flipFixtureByte(t, corruptPath, trailer.IndexOffset+tamperedIndexOffsetPadding)
		stdout, stderr, exitCode, err := executeFatBinary(t, corruptPath, nil)
		require.Error(t, err)
		require.NotEqual(t, defaultExitCode, exitCode)
		require.NotContains(t, stdout, rustStubPayloadTag)
		require.Contains(t, stderr, "index SHA-256 checksum mismatch")
	})

	t.Run("Scenario14_MutatedPayloadBytes", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "corrupt_payload.fat")
		_ = copyFile(t, baseFat, corruptPath)
		_, idx := readTrailerAndIndex(t, corruptPath)
		entry, found := idx.FindVariant(currentHostLevel)
		if !found {
			entry = &idx.Variants[0]
		}
		flipFixtureByte(t, corruptPath, entry.Offset+tamperedPayloadOffsetPadding)
		stdout, stderr, exitCode, err := executeFatBinary(t, corruptPath, []string{envDebugTrue, rustStubExecModeMemfdEnv})
		require.Error(t, err)
		require.NotEqual(t, defaultExitCode, exitCode)
		require.NotContains(t, stdout, rustStubPayloadTag)
		assert.True(t, strings.Contains(stderr, "decompressing variant payload") || strings.Contains(stderr, "checksum mismatch"),
			"unexpected stderr: %s", stderr)
	})

	t.Run("Scenario15_TamperedPayloadMetadataWithValidOuterChecksum", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "tampered_meta.fat")
		_ = copyFile(t, baseFat, corruptPath)
		trailer, idx := readTrailerAndIndex(t, corruptPath)
		entry, found := idx.FindVariant(currentHostLevel)
		if !found {
			entry = &idx.Variants[0]
		}
		entry.UncompressedSize = tamperedInvalidSize
		rewriteRustStubIndexWithValidTrailer(t, corruptPath, idx, trailer.IndexOffset)
		stdout, stderr, exitCode, err := executeFatBinary(t, corruptPath, []string{envDebugTrue, rustStubExecModeMemfdEnv})
		require.Error(t, err)
		require.NotEqual(t, defaultExitCode, exitCode)
		require.NotContains(t, stdout, rustStubPayloadTag)
		assert.True(t, strings.Contains(stderr, "decompressing variant payload") || strings.Contains(stderr, "exceeds declared"),
			"unexpected stderr: %s", stderr)
	})

	t.Run("Scenario16_SharedDictionaryTampering", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "corrupt_dict.fat")
		_ = copyFile(t, dictFat, corruptPath)
		_, idx := readTrailerAndIndex(t, corruptPath)
		if idx.DictionarySize <= 0 {
			t.Skip("dictionary size is 0")
		}
		flipFixtureByte(t, corruptPath, idx.DictionaryOffset+tamperedDictOffsetPadding)
		stdout, stderr, exitCode, err := executeFatBinary(t, corruptPath, []string{envDebugTrue, rustStubExecModeMemfdEnv})
		require.Error(t, err)
		require.NotEqual(t, defaultExitCode, exitCode)
		require.NotContains(t, stdout, rustStubPayloadTag)
		require.Contains(t, stderr, "shared dictionary SHA-256 checksum mismatch")
	})

	t.Run("Scenario17_PlausibleMalformedSemanticIndex", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "overlap_index.fat")
		_ = copyFile(t, baseFat, corruptPath)
		trailer, idx := readTrailerAndIndex(t, corruptPath)
		if len(idx.Variants) < 2 {
			t.Skip("need at least 2 variants to test overlap")
		}
		idx.Variants[1].Offset = idx.Variants[0].Offset + format.OffsetLen
		rewriteRustStubIndexWithValidTrailer(t, corruptPath, idx, trailer.IndexOffset)
		stdout, stderr, exitCode, err := executeFatBinary(t, corruptPath, []string{envDebugTrue, rustStubExecModeMemfdEnv})
		require.Error(t, err)
		require.NotEqual(t, defaultExitCode, exitCode)
		require.NotContains(t, stdout, rustStubPayloadTag)
		assert.True(t, strings.Contains(stderr, "variant payloads overlap or are unsorted") ||
			strings.Contains(stderr, "validating variant boundaries"),
			"unexpected stderr: %s", stderr)
	})

	t.Run("Scenario17b_OversizedZstdFrameWindowRejectedWithoutCgroups", func(t *testing.T) {
		t.Parallel()
		corruptPath := filepath.Join(t.TempDir(), "oversized_window.fat")
		_ = copyFile(t, baseFat, corruptPath)
		_, idx := readTrailerAndIndex(t, corruptPath)
		entry, found := idx.FindVariant(currentHostLevel)
		if !found {
			entry = &idx.Variants[0]
		}
		craftedHeader := []byte{
			rustStubZstdMagicByte0, rustStubZstdMagicByte1, rustStubZstdMagicByte2, rustStubZstdMagicByte3,
			rustStubZstdFHDWindowOnly, rustStubZstdWindow256MiB, 0x00, 0x00, 0x00,
		}
		mutateFileBytes(t, corruptPath, entry.Offset, craftedHeader)
		stdout, stderr, exitCode, err := executeFatBinary(t, corruptPath, []string{envDebugTrue, rustStubExecModeMemfdEnv})
		require.Error(t, err)
		require.NotEqual(t, defaultExitCode, exitCode)
		require.NotContains(t, stdout, rustStubPayloadTag)
		assert.Contains(t, strings.ToLower(stderr), "window")
	})
}

func TestRustStubMemfdAndCacheFallback(t *testing.T) {
	t.Parallel()
	rustStub := requireRustStubPath(t)
	fat := packRustStubGolden(t, rustStub, t.TempDir(), "rust-memfd.fat", false, mountCodecZstd)

	t.Run("Scenario18_CleanMemfdExecution", func(t *testing.T) {
		t.Parallel()
		cacheDir := filepath.Join(t.TempDir(), "memfd_cache_empty")
		require.NoError(t, os.MkdirAll(cacheDir, defaultFilePerm))
		stdout, stderr, exitCode, err := executeFatBinary(t, fat, []string{"MICROFAT_CACHE_DIR=" + cacheDir, envDebugTrue})
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		require.Contains(t, stderr, "exec_mode=memfd")
		assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)
		entries, err := os.ReadDir(cacheDir)
		require.NoError(t, err)
		require.Empty(t, entries)
	})

	t.Run("Scenario19_ExplicitCacheMode", func(t *testing.T) {
		t.Parallel()
		cacheDir := filepath.Join(t.TempDir(), "explicit_cache")
		env := []string{envExecCache, "MICROFAT_CACHE_DIR=" + cacheDir, envDebugTrue}
		stdout, stderr, exitCode, err := executeFatBinary(t, fat, env)
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		require.Contains(t, stderr, "exec_mode=cache")
		assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)
		entries, err := os.ReadDir(cacheDir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
	})

	t.Run("Scenario20_SeccompMemfdFallback", func(t *testing.T) {
		t.Parallel()
		cacheDir := filepath.Join(t.TempDir(), "seccomp_cache")
		env := []string{"MICROFAT_CACHE_DIR=" + cacheDir, envDebugTrue}
		stdout, stderr, exitCode, err := executeWithSeccompBlockedMemfd(t, fat, env)
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		require.Contains(t, stderr, "memfd_create")
		require.Contains(t, stderr, "exec_mode=cache")
		assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)
		entries, err := os.ReadDir(cacheDir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
	})
}

func TestRustStubCacheFirstAutoAndUnsafeChaos(t *testing.T) {
	t.Parallel()
	rustStub := requireRustStubPath(t)
	fat := packRustStubGolden(t, rustStub, t.TempDir(), "rust-cache-chaos.fat", false, mountCodecZstd)

	t.Run("VerifiedWarmCacheAndForcedModes", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "cache")
		entry := prepareCacheFirstEntry(t, fat, dir)
		original, err := os.Stat(entry)
		require.NoError(t, err)
		require.NoError(t, os.Chmod(entry, rustStubReadOnlyMode))
		require.NoError(t, os.Chmod(dir, rustStubReadOnlyMode))
		t.Cleanup(func() { require.NoError(t, os.Chmod(dir, privateDirPerm)) })

		before := cacheTreeSnapshot(t, root)
		env := cacheFirstEnv(dir)
		requireCacheFirstMode(t, fat, env, execModeCache)
		requireCacheFirstMode(t, fat, append(slices.Clone(env), rustStubExecModeMemfdEnv), execModeMemfd)
		requireCacheFirstMode(t, fat, append(slices.Clone(env), envExecCache), execModeCache)
		require.Equal(t, before, cacheTreeSnapshot(t, root))

		after, err := os.Stat(entry)
		require.NoError(t, err)
		require.True(t, os.SameFile(original, after))
		require.Equal(t, original.ModTime(), after.ModTime())
	})

	for _, state := range []string{rustStubStateAbsent, "empty", "insecure-directory"} {
		t.Run("ColdMiss/"+state, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "cache")
			if state != rustStubStateAbsent {
				require.NoError(t, os.Mkdir(dir, privateDirPerm))
			}
			if state == "insecure-directory" {
				require.NoError(t, os.Chmod(dir, rustStubInsecureMode))
			}
			before := cacheTreeSnapshot(t, root)
			requireCacheFirstMode(t, fat, cacheFirstEnv(dir), execModeMemfd)
			require.Equal(t, before, cacheTreeSnapshot(t, root))
		})
	}

	for _, damage := range []string{rustStubDamageSize, "checksum"} {
		for _, verify := range []string{"", "0", rustStubVerifyFalse} {
			t.Run("CorruptEntry/"+damage+"/verify="+verify, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				dir := filepath.Join(root, "cache")
				entry := prepareCacheFirstEntry(t, fat, dir)
				original, err := os.ReadFile(entry)
				require.NoError(t, err)
				corrupt := bytes.Clone(original)
				if damage == rustStubDamageSize {
					corrupt = corrupt[:1]
				} else {
					corrupt[0] ^= 0xff
				}
				require.NoError(t, retryFixtureBusy(func() error { return os.WriteFile(entry, corrupt, defaultFilePerm) }))
				before := cacheTreeSnapshot(t, root)
				env := append(cacheFirstEnv(dir), "MICROFAT_VERIFY_CACHE="+verify)
				requireCacheFirstMode(t, fat, env, execModeMemfd)
				require.Equal(t, before, cacheTreeSnapshot(t, root))
				requireCacheFirstMode(t, fat, append(slices.Clone(env), envExecCache), execModeCache)
				repaired, err := os.ReadFile(entry)
				require.NoError(t, err)
				require.Equal(t, original, repaired)
			})
		}
	}

	t.Run("MissingExecutePermissionRecovery", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		entry := prepareCacheFirstEntry(t, fat, dir)
		require.NoError(t, os.Chmod(entry, rustStubNonExecMode))
		before, err := os.Stat(entry)
		require.NoError(t, err)
		requireCacheFirstMode(t, fat, cacheFirstEnv(dir), execModeMemfd)
		after, err := os.Stat(entry)
		require.NoError(t, err)
		require.True(t, os.SameFile(before, after))
		require.Equal(t, before.Mode(), after.Mode())

		requireCacheFirstMode(t, fat, append(cacheFirstEnv(dir), envExecCache), execModeCache)
		after, err = os.Stat(entry)
		require.NoError(t, err)
		require.Equal(t, privateDirPerm, after.Mode().Perm())
	})

	for _, kind := range []string{"fifo", "socket", cacheFirstSymlink, rustStubKindDir, "group-write", "other-write", rustStubModeSetuid} {
		t.Run("UnsafeEntry/"+kind, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "cache")
			entry := prepareCacheFirstEntry(t, fat, dir)
			prepareUnsafeCacheFirstEntry(t, entry, kind)
			before := cacheTreeSnapshot(t, root)
			for _, mode := range []string{"", execModeCache} {
				env := append(cacheFirstEnv(dir), "MICROFAT_EXEC_MODE="+mode)
				stdout, stderr, err := runCacheFirstProcess(t, fat, env, "--echo-env", "MICROFAT_EXEC_MODE")
				require.Error(t, err, "unsafe entry %s accepted in mode %q: %s", kind, mode, stderr)
				require.Empty(t, stdout)
				if kind == cacheFirstSymlink {
					assert.True(t, strings.Contains(stderr, "refusal to execute symlink at") ||
						strings.Contains(stderr, "too many levels of symbolic links"), "stderr=%s", stderr)
				}
				require.Equal(t, before, cacheTreeSnapshot(t, root))
			}
		})
	}

	t.Run("WarmHitAndSourceCorruption", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "cache")
		entry := prepareCacheFirstEntry(t, fat, dir)
		corruptFat := filepath.Join(root, "corrupt-fat")
		copyFile(t, fat, corruptFat)
		_, index := readTrailerAndIndex(t, corruptFat)
		found := false
		for _, variant := range index.Variants {
			if variant.SHA256 == filepath.Base(entry) {
				mutateFileBytes(t, corruptFat, variant.Offset, bytes.Repeat([]byte{0xff}, int(trailerMagicSizeBytes)))
				found = true
				break
			}
		}
		require.True(t, found)
		before := cacheTreeSnapshot(t, root)
		requireCacheFirstMode(t, corruptFat, cacheFirstEnv(dir), execModeCache)
		for _, env := range [][]string{
			append(cacheFirstEnv(dir), rustStubExecModeMemfdEnv),
			cacheFirstEnv(filepath.Join(root, "absent-cache")),
		} {
			stdout, stderr, err := runCacheFirstProcess(t, corruptFat, env, "--echo-env", "MICROFAT_EXEC_MODE")
			require.Error(t, err, stderr)
			require.Empty(t, stdout)
		}
		require.Equal(t, before, cacheTreeSnapshot(t, root))
	})

	t.Run("Scenario21_MissingCacheDirCreation", func(t *testing.T) {
		t.Parallel()
		deepDir := filepath.Join(t.TempDir(), "deep", "nested", "cache", "dir")
		env := []string{envExecCache, "MICROFAT_CACHE_DIR=" + deepDir, envDebugTrue}
		stdout, stderr, exitCode, err := executeFatBinary(t, fat, env)
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)
		fi, err := os.Stat(deepDir)
		require.NoError(t, err)
		require.True(t, fi.IsDir())
		require.Equal(t, privateDirPerm, fi.Mode().Perm())
	})

	t.Run("Scenario26_ConcurrentCacheContention", func(t *testing.T) {
		t.Parallel()
		cacheDir := filepath.Join(t.TempDir(), "concurrent_cache")
		env := []string{envExecCache, "MICROFAT_CACHE_DIR=" + cacheDir}
		var wg sync.WaitGroup
		errChan := make(chan error, concurrentProcsCount)
		for range concurrentProcsCount {
			wg.Go(func() {
				stdout, _, exitCode, err := executeFatBinary(t, fat, env)
				if err != nil || exitCode != defaultExitCode {
					errChan <- fmt.Errorf("exit=%d err=%w", exitCode, err)
					return
				}
				if !strings.Contains(stdout, rustStubPayloadTag+currentHostLevel) {
					errChan <- os.ErrInvalid
				}
			})
		}
		wg.Wait()
		close(errChan)
		for err := range errChan {
			require.NoError(t, err)
		}
		entries, err := os.ReadDir(cacheDir)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		f, err := os.Open(filepath.Join(cacheDir, entries[0].Name()))
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		hasher := sha256.New()
		_, err = io.Copy(hasher, f)
		require.NoError(t, err)
		require.Equal(t, hex.EncodeToString(hasher.Sum(nil)), entries[0].Name())
	})
}

func TestRustStubVerifiedCacheDescriptorAndChecksumCasing(t *testing.T) {
	t.Parallel()
	rustStub := requireRustStubPath(t)
	levels := creationEpochLevels()
	require.NotEmpty(t, levels)

	codecs := []checksumCasingCodec{
		{name: codec.AlgorithmNone, algorithm: codec.AlgorithmNone},
		{name: "lz4-fast", algorithm: "lz4:fast"},
		{name: "lz4-1", algorithm: "lz4:1"},
		{name: "lz4-9", algorithm: "lz4:9"},
		{name: creationEpochZstd, algorithm: creationEpochZstd},
		{name: "zstd-default", algorithm: "zstd:default"},
		{name: "zstd-best-dict", algorithm: "zstd:best", dictionary: true},
	}

	for _, c := range codecs {
		t.Run("Handoff/"+c.name, func(t *testing.T) {
			t.Parallel()
			fat := packRustStubGolden(t, rustStub, t.TempDir(), "handoff-"+c.name+".fat", c.dictionary, c.algorithm)
			index := inspectCreationEpoch(t, fat)
			if c.dictionary {
				require.Positive(t, index.DictionarySize)
			} else {
				require.Zero(t, index.DictionarySize)
			}
			requireCacheHandoffLaunches(t, fat, levels[0])
		})
	}

	casingCodecs := []checksumCasingCodec{
		{name: codec.AlgorithmNone, algorithm: codec.AlgorithmNone},
		{name: codec.AlgorithmLZ4, algorithm: codec.AlgorithmLZ4},
		{name: mountCodecZstd, algorithm: mountCodecZstd},
		{name: rustStubZstdDictLabel, algorithm: mountCodecZstd, dictionary: true},
	}
	casings := []struct {
		name   string
		change func(string) string
	}{
		{name: "lowercase", change: strings.ToLower},
		{name: "uppercase", change: strings.ToUpper},
		{name: "mixed-case", change: mixedChecksumCasing},
	}

	baseDir := t.TempDir()
	for _, c := range casingCodecs {
		base := packRustStubGolden(t, rustStub, baseDir, "casing-"+c.name+".fat", c.dictionary, c.algorithm)
		for _, casing := range casings {
			t.Run("ChecksumCasing/"+c.name+"/"+casing.name, func(t *testing.T) {
				t.Parallel()
				fat, digest := rewriteChecksumCasingFixture(t, base, format.FormatVersion2, casing.change, c.dictionary)
				lowercase, _ := rewriteChecksumCasingFixture(t, base, format.FormatVersion2, strings.ToLower, c.dictionary)
				verifyFatIntegrity(t, fat)
				assertChecksumCasingDispatch(t, fat, lowercase, digest)
			})
		}
	}
}

func TestRustStubProcessFidelityAndWhitespacePaths(t *testing.T) {
	t.Parallel()
	rustStub := requireRustStubPath(t)
	dir := t.TempDir()
	fat := packRustStubGolden(t, rustStub, dir, "rust-fidelity.fat", false, "")

	t.Run("Scenario27_ComplexArgs", func(t *testing.T) {
		t.Parallel()
		args := []string{"--echo-args", "arg with spaces", "--flag=val", "quote's test", "unicode:🚀-⚡"}
		stdout, stderr, exitCode, err := executeFatBinary(t, fat, nil, args...)
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		require.Equal(t, "arg with spaces||--flag=val||quote's test||unicode:🚀-⚡", stdout)
	})

	t.Run("Scenario28_HighVolumeArgs", func(t *testing.T) {
		t.Parallel()
		args := make([]string, 0, highVolumeArgCount+1)
		args = append(args, "--echo-args")
		parts := make([]string, 0, highVolumeArgCount)
		for i := range highVolumeArgCount {
			v := fmt.Sprintf("arg_%03d", i)
			args = append(args, v)
			parts = append(parts, v)
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, fat, nil, args...)
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		require.Equal(t, strings.Join(parts, "||"), stdout)
	})

	t.Run("Scenario29_EnvForwarding", func(t *testing.T) {
		t.Parallel()
		stdout, stderr, exitCode, err := executeFatBinary(t, fat, []string{"TEST_CUSTOM_KEY=secret_token_42_xyz"},
			"--echo-env", "TEST_CUSTOM_KEY")
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		require.Equal(t, "secret_token_42_xyz", stdout)
	})

	for _, tc := range []struct {
		name     string
		arg      string
		expected int
	}{
		{"ExitCode_0", "0", defaultExitCode},
		{"ExitCode_42", "42", expectedExitCode42},
		{"ExitCode_101", "101", expectedExitCode101},
	} {
		t.Run("Scenario30_"+tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, exitCode, _ := executeFatBinary(t, fat, nil, "--exit-code", tc.arg)
			require.Equal(t, tc.expected, exitCode)
		})
	}

	t.Run("Scenario31_StdinPipeline", func(t *testing.T) {
		t.Parallel()
		inputData := "hello from piped stdin stream to microfat-stub-rs\nsecond line"
		cmd := exec.Command(fat, "--cat-stdin")
		cmd.Stdin = strings.NewReader(inputData)
		var stdoutBuf, stderrBuf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdoutBuf, &stderrBuf
		require.NoError(t, cmd.Run(), "stderr=%s", stderrBuf.String())
		require.Equal(t, inputData, stdoutBuf.String())
	})

	t.Run("Scenario32_And_33_SpacesAndOriginalExe", func(t *testing.T) {
		t.Parallel()
		spacedDir := filepath.Join(t.TempDir(), "space test dir", "nested sub dir")
		require.NoError(t, os.MkdirAll(spacedDir, defaultFilePerm))
		spacedFat := filepath.Join(spacedDir, "my fat app with spaces.fat")
		_ = copyFile(t, fat, spacedFat)
		absSpacedFat, err := filepath.Abs(spacedFat)
		require.NoError(t, err)

		stdout, stderr, exitCode, err := executeFatBinary(t, spacedFat, nil)
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		require.Contains(t, stdout, rustStubPayloadTag+currentHostLevel)

		stdout, stderr, exitCode, err = executeFatBinary(t, spacedFat, []string{"MICROFAT_ORIGINAL_EXE=/spoofed/path"},
			"--echo-env", "MICROFAT_ORIGINAL_EXE")
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		require.Equal(t, absSpacedFat, stdout)

		stdout, stderr, exitCode, err = executeFatBinary(t, spacedFat, []string{envExecCache},
			"--echo-env", "MICROFAT_ORIGINAL_EXE")
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		require.Equal(t, absSpacedFat, stdout)

		stdout, stderr, exitCode, err = executeWithSeccompBlockedMemfd(t, spacedFat,
			[]string{"MICROFAT_CACHE_DIR=" + filepath.Join(t.TempDir(), "seccomp-cache")},
			"--echo-env", "MICROFAT_ORIGINAL_EXE")
		require.NoError(t, err, "exit=%d stderr=%s", exitCode, stderr)
		require.Equal(t, absSpacedFat, stdout)
	})

	t.Run("WhitespaceFilenamesAndDecoySibling", func(t *testing.T) {
		t.Parallel()
		wsDir := t.TempDir()
		reporterBin := filepath.Join(wsDir, "reporter")
		require.NoError(t, compileBinary("./testdata/whitespace_reporter", reporterBin, nil))
		decoyBin := filepath.Join(wsDir, "decoy")
		require.NoError(t, compileBinary(goldenAppPkg, decoyBin, nil))
		base := rustStubBaseLevel()

		for _, mode := range []string{execModeMemfd, execModeCache} {
			t.Run(mode, func(t *testing.T) {
				testDir := t.TempDir()
				cacheDir := filepath.Join(testDir, "cache")
				require.NoError(t, os.MkdirAll(cacheDir, privateDirPerm))
				targetFat := filepath.Join(testDir, "app ")
				decoyFat := filepath.Join(testDir, "app")
				require.NoError(t, packBinary(cliPath, rustStub, "ws-rs", targetFat, map[string]string{base: reporterBin}))
				require.NoError(t, packBinary(cliPath, rustStub, "decoy-rs", decoyFat, map[string]string{base: decoyBin}))

				commonEnv := []string{
					"MICROFAT_EXEC_MODE=" + mode,
					"MICROFAT_CACHE_DIR=" + cacheDir,
					"XDG_CACHE_HOME=" + cacheDir,
					"MICROFAT_ORIGINAL_EXE=/spoofed/fake/hint",
				}
				ctx, cancel := context.WithTimeout(context.Background(), rustStubWhitespaceTimeout)
				defer cancel()
				testArgs := []string{"--flag", "param with space", "literal\targ"}
				cmd := exec.CommandContext(ctx, targetFat, testArgs...)
				cmd.Env = append(os.Environ(), commonEnv...)
				var stdoutBuf, stderrBuf bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdoutBuf, &stderrBuf
				require.NoError(t, cmd.Run(), "stderr=%s", stderrBuf.String())

				var rep payloadReport
				require.NoError(t, json.Unmarshal(stdoutBuf.Bytes(), &rep), "stdout=%s", stdoutBuf.String())
				assert.Equal(t, targetFat, rep.OriginalExeHint)
				assert.Equal(t, targetFat, rep.Executable)
				assert.NotEqual(t, decoyFat, rep.Executable)
				assert.Equal(t, testArgs, rep.Args)
			})
		}
	})
}

func TestRustStubRunningImageSurvivesDeploymentChange(t *testing.T) {
	t.Parallel()
	rustStub := requireRustStubPath(t)
	base := rustStubBaseLevel()
	dir := t.TempDir()
	oldFat := filepath.Join(dir, "old-rs.fat")
	newFat := filepath.Join(dir, "new-rs.fat")
	require.NoError(t, packBinary(cliPath, rustStub, "old-rs", oldFat, map[string]string{base: goldenVariantBins[base]}))
	require.NoError(t, packBinary(cliPath, rustStub, "new-rs", newFat, map[string]string{base: cliPath}))
	original, err := os.ReadFile(oldFat)
	require.NoError(t, err)
	replacement, err := os.ReadFile(newFat)
	require.NoError(t, err)

	for _, mode := range []string{execModeMemfd, execModeCache} {
		for _, action := range []string{"unchanged", mountUnlink, mountReplace} {
			t.Run(mode+"/"+action, func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, "deployment")
				require.NoError(t, os.WriteFile(path, original, defaultFilePerm))
				output, runErr := runPausedImage(t, path, nil, []string{"MICROFAT_EXEC_MODE=" + mode}, func() {
					changeDeployment(t, action, path, replacement)
				})
				require.NoError(t, runErr, output)
				require.Contains(t, output, rustStubPayloadTag+base)
				assertDeployment(t, action, path, original, replacement)
			})
		}
	}
}

func TestRustStubPrivilegedLauncherRejection(t *testing.T) {
	rustStub := requireRustStubPath(t)
	for _, tool := range []string{"unshare", "setpriv", "setcap"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("privileged fixture unavailable: %s", tool)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), rustStubPrivilegedTimeout)
	defer cancel()
	probe := exec.CommandContext(ctx, "unshare", "--user", "--map-auto", "--map-root-user", "setpriv",
		"--reuid=1", "--regid=1", "--clear-groups", "/usr/bin/true")
	if output, err := probe.CombinedOutput(); err != nil {
		t.Skipf("subordinate UID/GID mappings unavailable: %v: %s", err, output)
	}

	dir, err := os.MkdirTemp("", "microfat-rs-priv-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.Chmod(dir, defaultFilePerm))

	base := rustStubBaseLevel()
	fat := filepath.Join(dir, "rs-priv.fat")
	require.NoError(t, packBinary(cliPath, rustStub, "rs-priv", fat, map[string]string{base: goldenVariantBins[base]}))

	for _, mode := range []string{"ordinary-root", rustStubModeSetuid, "setgid", "capability"} {
		t.Run(mode, func(t *testing.T) {
			target := filepath.Join(dir, "rs-"+mode)
			script := `cp "$1" "$2"
chmod 755 "$2"
case "$3" in
 setuid) chmod u+s "$2" ;;
 setgid) chmod g+s "$2" ;;
 capability) setcap cap_net_bind_service=ep "$2" ;;
 ordinary-root) exec "$2" ;;
esac
exec setpriv --reuid=1 --regid=1 --clear-groups "$2" --microfat-info`
			cmd := exec.CommandContext(ctx, "unshare", "--user", "--map-auto", "--map-root-user", "sh", "-eu", "-c",
				script, "fixture", fat, target, mode)
			cmd.Env = append(os.Environ(), rustStubExecModeMemfdEnv)
			output, cmdErr := cmd.CombinedOutput()
			require.NoError(t, ctx.Err(), "privileged fixture timed out")
			if mode == "ordinary-root" {
				require.NoError(t, cmdErr, string(output))
				require.Contains(t, string(output), rustStubPayloadTag+base)
				return
			}
			if mode == "capability" && strings.Contains(string(output), "setcap:") {
				t.Skipf("filesystem capability fixture unavailable: %s", output)
			}
			require.Error(t, cmdErr, string(output))
			require.Contains(t, string(output), "privilege-bearing launcher execution is unsupported")
		})
	}
}

func TestRustStubTelemetryAndRuntimeInfoParity(t *testing.T) {
	t.Parallel()
	rustStub := requireRustStubPath(t)
	levels := creationEpochLevels()
	require.NotEmpty(t, levels)

	codecs := []checksumCasingCodec{
		{name: creationEpochNone, algorithm: creationEpochNone},
		{name: creationEpochLZ4, algorithm: "lz4:fast"},
		{name: mountCodecZstd, algorithm: creationEpochZstd},
		{name: rustStubZstdDictLabel, algorithm: creationEpochZstd, dictionary: true},
	}

	diagBase := t.TempDir()
	for _, c := range codecs {
		t.Run("Telemetry/"+c.name, func(t *testing.T) {
			t.Parallel()
			fat := packRustStubGolden(t, rustStub, diagBase, "telem-"+c.name+".fat", c.dictionary, c.algorithm)
			for _, diagnostic := range telemetryDiagnosticModes() {
				for _, path := range []string{execModeMemfd, telemetryColdCache, telemetryWarmCache} {
					t.Run(diagnostic.name+"/"+path, func(t *testing.T) {
						env, mode := telemetryProcessEnvironment(t, fat, levels[0], path, diagnostic)
						stdout, stderr, err := runTelemetryProcess(t, fat, env)
						require.NoError(t, err, stderr)
						require.Equal(t, rustStubPayloadTag+levels[0]+"\ngolden:env=telemetry-preserved\n", stdout)
						requireTelemetryDiagnostics(t, diagnostic, stderr, levels[0], mode, path == telemetryWarmCache)
					})
				}
			}
		})
	}

	t.Run("TelemetryNonemptyAndAutoWarmAndDisabledError", func(t *testing.T) {
		t.Parallel()
		fat := packRustStubGolden(t, rustStub, t.TempDir(), "telem-edge.fat", false, creationEpochZstd)
		for _, diagnostic := range []telemetryDiagnosticMode{
			{name: "unrecognized-debug", debug: "enabled", output: telemetryDebug},
			{name: "whitespace-debug", debug: " false ", output: telemetryDebug},
			{name: "zero-log", log: "0", output: telemetryDebug},
			{name: "false-log", log: rustStubVerifyFalse, output: telemetryDebug},
			{name: "json-with-disabled-debug", debug: "0", log: telemetryJSON, output: telemetryJSON},
		} {
			env, mode := telemetryProcessEnvironment(t, fat, levels[0], execModeMemfd, diagnostic)
			stdout, stderr, err := runTelemetryProcess(t, fat, env)
			require.NoError(t, err, stderr)
			require.Contains(t, stdout, rustStubPayloadTag+levels[0])
			requireTelemetryDiagnostics(t, diagnostic, stderr, levels[0], mode, false)
		}

		for _, diagnostic := range telemetryDiagnosticModes() {
			env, _ := telemetryProcessEnvironment(t, fat, levels[0], telemetryWarmCache, diagnostic)
			for i, setting := range env {
				if strings.HasPrefix(setting, "MICROFAT_EXEC_MODE=") {
					env[i] = "MICROFAT_EXEC_MODE=auto"
				}
			}
			stdout, stderr, err := runTelemetryProcess(t, fat, env, "--echo-env", "MICROFAT_EXEC_MODE")
			require.NoError(t, err, stderr)
			require.Equal(t, execModeCache, stdout)
			requireTelemetryDiagnostics(t, diagnostic, stderr, levels[0], execModeCache, true)
			if diagnostic.output == "" {
				badEnv, _ := telemetryProcessEnvironment(t, fat, "invalid-level", execModeMemfd, diagnostic)
				badOut, badErr, errFail := runTelemetryProcess(t, fat, badEnv)
				require.Error(t, errFail)
				require.Empty(t, badOut)
				require.Contains(t, badErr, "[microfat] error:")
				require.NotRegexp(t, `decompress_us=|total_us=|"event":"dispatch"`, badErr)
			}
		}
	})

	t.Run("RuntimeInfoAndInternalEnvScrubbing", func(t *testing.T) {
		t.Parallel()
		riDir := t.TempDir()
		reporter := filepath.Join(riDir, "runtimeinfo-reporter")
		require.NoError(t, compileBinary(runtimeInfoReporterPkg, reporter, []string{envStatic, envBaselineAMD64, runtimeInfoARM64Base}))
		base := rustStubBaseLevel()
		riFat := filepath.Join(riDir, "rs-runtimeinfo.fat")
		require.NoError(t, packBinary(cliPath, rustStub, "rs-ri", riFat, map[string]string{base: reporter}))

		payload, err := os.ReadFile(reporter)
		require.NoError(t, err)
		digest := sha256.Sum256(payload)
		nativeDefaults := runRuntimeInfoReporter(t, reporter, stubInfoProcessEnvironment(t.TempDir()))

		for _, mode := range []string{execModeMemfd, execModeCache} {
			for _, tuning := range []struct{ name, key, value string }{
				{name: "AutotuneDisabled", key: "MICROFAT_AUTOTUNE", value: "0"},
				{name: "DryRun", key: "MICROFAT_DRY_RUN", value: "1"},
			} {
				t.Run(mode+"/"+tuning.name, func(t *testing.T) {
					t.Parallel()
					env := runtimeInfoEnvironment(t,
						tuning.key+"="+tuning.value,
						"MICROFAT_EXEC_MODE="+mode,
						"MICROFAT_POLICY_APPLIED=spoofed_policy",
						"MICROFAT_OVERRIDE_REASON=spoofed_reason",
						"MICROFAT_CGROUP_GOMEMLIMIT=1B",
						"MICROFAT_CGROUP_GOGC=999",
					)
					snapshot := runRuntimeInfoReporter(t, riFat, env)
					assertRuntimeInfoExplicitEnvironment(t, snapshot.Configured)
					assertRuntimeInfoObserved(t, snapshot.Observed, runtimeInfoMemory, runtimeInfoMaxProcs, runtimeInfoGCPercent)
					assertRuntimeInfoField(t, snapshot.Launcher.SelectedVariant, base)
					assertRuntimeInfoField(t, snapshot.Launcher.HostArch, currentHostArch)
					assertRuntimeInfoField(t, snapshot.Launcher.HostLevel, currentHostLevel)
					assertRuntimeInfoField(t, snapshot.Launcher.ExecMode, mode)
					assertRuntimeInfoField(t, snapshot.Launcher.DispatchMode, mode)
					assertRuntimeInfoField(t, snapshot.Launcher.SelectedSHA256, hex.EncodeToString(digest[:]))
					assertRuntimeInfoField(t, snapshot.Launcher.SelectedSizeBytes, int64(len(payload)))
					assertRuntimeInfoField(t, snapshot.Launcher.OriginalExecutable, riFat)
					require.Equal(t, runtimeinfo.StatusMissing, snapshot.Launcher.PolicyApplied.Status)
					require.Equal(t, runtimeinfo.StatusMissing, snapshot.Launcher.OverrideReason.Status)
					require.Equal(t, runtimeinfo.StatusMissing, snapshot.Launcher.Cgroup.RecommendedGOGC.Status)

					unsetEnv := stubInfoProcessEnvironment(t.TempDir(), tuning.key+"="+tuning.value, "MICROFAT_EXEC_MODE="+mode)
					untuned := runRuntimeInfoReporter(t, riFat, unsetEnv)
					assert.Equal(t, runtimeinfo.StatusMissing, untuned.Configured.GOMEMLIMIT.Status)
					assert.Equal(t, runtimeinfo.StatusMissing, untuned.Configured.GOMAXPROCS.Status)
					assert.Equal(t, runtimeinfo.StatusMissing, untuned.Configured.GOGC.Status)
					assert.Equal(t, nativeDefaults.Observed, untuned.Observed)
				})
			}
		}

		// Explicit GC/CPU policy parity check
		t.Run("CPUPolicyNativeAndGCProfile", func(t *testing.T) {
			t.Parallel()
			env := stubInfoProcessEnvironment(t.TempDir(),
				"MICROFAT_EXEC_MODE=memfd",
				"MICROFAT_CPU_POLICY=native",
				"MICROFAT_GC_PROFILE=latency_critical",
			)
			snapshot := runRuntimeInfoReporter(t, riFat, env)
			if snapshot.Launcher.Cgroup.Version.Status == runtimeinfo.StatusPresent {
				assertRuntimeInfoField(t, snapshot.Launcher.Cgroup.GCProfile, "latency_critical")
				assert.Equal(t, "75", snapshot.Configured.GOGC.Value)
			}
			assert.Equal(t, runtimeinfo.StatusMissing, snapshot.Configured.GOMAXPROCS.Status)
		})
	})
}
