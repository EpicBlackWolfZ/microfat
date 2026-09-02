package e2e_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	truncatedSizeBytes   = 128
	concurrentProcsCount = 10
)

func TestCacheSecurityAndFilesystemInvariants(t *testing.T) {
	t.Parallel()

	t.Run("Scenario21_MissingCacheDirectoryAutoCreation", func(t *testing.T) {
		t.Parallel()
		deepMissingCacheDir := filepath.Join(t.TempDir(), "deep", "nested", "cache", "dir")

		env := []string{
			envExecCache,
			"MICROFAT_CACHE_DIR=" + deepMissingCacheDir,
			envDebugTrue,
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("execution failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)

		fi, err := os.Stat(deepMissingCacheDir)
		if err != nil {
			t.Fatalf("stat created cache dir: %v", err)
		}
		if !fi.IsDir() {
			t.Fatalf("expected created cache path to be directory")
		}
		if fi.Mode().Perm() != privateDirPerm {
			t.Fatalf("expected cache dir permission %v, got %v", privateDirPerm, fi.Mode().Perm())
		}
	})

	t.Run("Scenario22_ValidCacheHit", func(t *testing.T) {
		t.Parallel()
		cacheDir := filepath.Join(t.TempDir(), "hit_cache")
		env := []string{
			envExecCache,
			"MICROFAT_CACHE_DIR=" + cacheDir,
			envDebugTrue,
		}

		// First execution: extracts binary
		_, _, exitCode1, err1 := executeFatBinary(t, goldenFatBin, env)
		if err1 != nil || exitCode1 != defaultExitCode {
			t.Fatalf("first execution failed (code %d): %v", exitCode1, err1)
		}

		entries, err := os.ReadDir(cacheDir)
		if err != nil || len(entries) != 1 {
			t.Fatalf("expected 1 cache entry, found %d (err: %v)", len(entries), err)
		}
		cachedPath := filepath.Join(cacheDir, entries[0].Name())
		stat1, err := os.Stat(cachedPath)
		if err != nil {
			t.Fatalf("stat cached file: %v", err)
		}

		// Second execution: must reuse existing verified cache file
		stdout2, stderr2, exitCode2, err2 := executeFatBinary(t, goldenFatBin, env)
		if err2 != nil || exitCode2 != defaultExitCode {
			t.Fatalf("second execution failed (code %d): %v\nstderr: %s", exitCode2, err2, stderr2)
		}
		assertSelectedMatchesExecuted(t, stdout2, stderr2, currentHostLevel)

		stat2, err := os.Stat(cachedPath)
		if err != nil {
			t.Fatalf("stat cached file second time: %v", err)
		}
		if stat1.ModTime() != stat2.ModTime() {
			t.Fatalf("expected cached file to be reused without modification on second execution")
		}
	})

	t.Run("Scenario23_TruncatedCacheAutoRecovery", func(t *testing.T) {
		t.Parallel()
		cacheDir := filepath.Join(t.TempDir(), "trunc_cache")
		env := []string{
			envExecCache,
			"MICROFAT_CACHE_DIR=" + cacheDir,
			envDebugTrue,
		}

		// Pre-populate cache
		_, _, exitCode, err := executeFatBinary(t, goldenFatBin, env)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("initial execution failed (code %d): %v", exitCode, err)
		}

		entries, _ := os.ReadDir(cacheDir)
		if len(entries) != 1 {
			t.Fatalf("expected 1 cached file, found %d", len(entries))
		}
		cachedPath := filepath.Join(cacheDir, entries[0].Name())

		// Truncate cached binary to 128 bytes
		if err := os.Truncate(cachedPath, truncatedSizeBytes); err != nil {
			t.Fatalf("truncating %s: %v", cachedPath, err)
		}

		// Execution must detect size mismatch, purge truncated binary, re-extract and succeed
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("execution after truncation failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)

		reStat, err := os.Stat(cachedPath)
		if err != nil {
			t.Fatalf("stat re-extracted file: %v", err)
		}
		if reStat.Size() <= truncatedSizeBytes {
			t.Fatalf("expected full re-extracted size, got truncated %d bytes", reStat.Size())
		}
	})

	t.Run("Scenario24_CorruptedCacheFileWithChecksumVerification", func(t *testing.T) {
		t.Parallel()
		cacheDir := filepath.Join(t.TempDir(), "corrupt_cache")
		env := []string{
			envExecCache,
			"MICROFAT_CACHE_DIR=" + cacheDir,
			"MICROFAT_VERIFY_CACHE=1",
			envDebugTrue,
		}

		// Pre-populate cache
		_, _, exitCode, err := executeFatBinary(t, goldenFatBin, env)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("initial execution failed (code %d): %v", exitCode, err)
		}

		entries, _ := os.ReadDir(cacheDir)
		if len(entries) != 1 {
			t.Fatalf("expected 1 cached file, found %d", len(entries))
		}
		cachedPath := filepath.Join(cacheDir, entries[0].Name())

		// Flip bytes in the middle of cached file while preserving file size
		mutateFileBytes(t, cachedPath, 512, []byte{0xDE, 0xAD, 0xBE, 0xEF})

		// Run with MICROFAT_VERIFY_CACHE=1: launcher detects checksum mismatch, re-extracts and succeeds
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env)
		if err != nil || exitCode != defaultExitCode {
			t.Fatalf("execution with verify cache failed (code %d): %v\nstderr: %s", exitCode, err, stderr)
		}
		assertSelectedMatchesExecuted(t, stdout, stderr, currentHostLevel)
	})

	t.Run("Scenario25_StrictSymlinkAttackRejection", func(t *testing.T) {
		t.Parallel()
		cacheDir := filepath.Join(t.TempDir(), "symlink_attack_cache")
		if err := os.MkdirAll(cacheDir, defaultFilePerm); err != nil {
			t.Fatalf("mkdir cache: %v", err)
		}

		_, idx := readTrailerAndIndex(t, goldenFatBin)
		entry, found := idx.FindVariant(currentHostLevel)
		if !found {
			entry = &idx.Variants[0]
		}

		// Create a symlink at the expected cached path pointing to /bin/sh
		symlinkTarget := "/bin/sh"
		cachedSymlink := filepath.Join(cacheDir, entry.SHA256)
		if err := os.Symlink(symlinkTarget, cachedSymlink); err != nil {
			t.Fatalf("creating test symlink: %v", err)
		}

		env := []string{
			envExecCache,
			"MICROFAT_CACHE_DIR=" + cacheDir,
			envDebugTrue,
		}
		stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env)
		if err == nil && exitCode == defaultExitCode {
			t.Fatalf("expected symlink rejection error, but execution succeeded\nstdout: %s", stdout)
		}

		// Assert specific refusal error diagnostics from O_NOFOLLOW / ELOOP descriptor binding
		if !strings.Contains(stderr, "refusal to execute symlink at") && !strings.Contains(stderr, "too many levels of symbolic links") {
			t.Fatalf("expected explicit symlink refusal in stderr, got:\n%s", stderr)
		}
	})

	t.Run("Scenario26_ConcurrentCacheContention", func(t *testing.T) {
		t.Parallel()
		cacheDir := filepath.Join(t.TempDir(), "concurrent_race_cache")
		env := []string{
			envExecCache,
			"MICROFAT_CACHE_DIR=" + cacheDir,
		}

		var wg sync.WaitGroup
		errChan := make(chan error, concurrentProcsCount)

		for i := 0; i < concurrentProcsCount; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				stdout, stderr, exitCode, err := executeFatBinary(t, goldenFatBin, env)
				if err != nil || exitCode != defaultExitCode {
					errChan <- err
					return
				}
				if !strings.Contains(stdout, "golden:variant="+currentHostLevel) {
					errChan <- os.ErrInvalid
					return
				}
				_ = stderr
			}()
		}

		wg.Wait()
		close(errChan)

		for err := range errChan {
			if err != nil {
				t.Fatalf("concurrent process failed under cache contention: %v", err)
			}
		}

		// Assert that exactly one canonical cache artifact exists and is verified
		entries, err := os.ReadDir(cacheDir)
		if err != nil {
			t.Fatalf("reading cache directory: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected exactly 1 canonical cached payload in %s, found %d", cacheDir, len(entries))
		}

		cachedPath := filepath.Join(cacheDir, entries[0].Name())
		cachedStat, err := os.Stat(cachedPath)
		if err != nil {
			t.Fatalf("stat cached artifact: %v", err)
		}
		if cachedStat.Size() == 0 {
			t.Fatalf("cached artifact is empty")
		}

		// Verify cached file SHA-256 matches its filename (launcher naming contract)
		f, err := os.Open(cachedPath)
		if err != nil {
			t.Fatalf("open cached artifact: %v", err)
		}
		defer func() { _ = f.Close() }()

		hasher := sha256.New()
		if _, err := io.Copy(hasher, f); err != nil {
			t.Fatalf("hashing cached artifact: %v", err)
		}
		actualSHA256 := hex.EncodeToString(hasher.Sum(nil))
		if entries[0].Name() != actualSHA256 {
			t.Fatalf("cached filename %q does not match artifact SHA-256 %q", entries[0].Name(), actualSHA256)
		}
	})
}
