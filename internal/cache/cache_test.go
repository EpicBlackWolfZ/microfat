package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

func TestVerifyBinary(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	validContent := []byte("cache test payload for VerifyBinary 12345")
	validSize := int64(len(validContent))
	h := sha256.Sum256(validContent)
	validSHA := hex.EncodeToString(h[:])

	validPath := filepath.Join(tempDir, "valid_file")
	err := os.WriteFile(validPath, validContent, 0o755)
	if err != nil {
		t.Fatalf("writing valid file: %v", err)
	}

	t.Run("ValidFile", func(t *testing.T) {
		if !VerifyBinary(validPath, validSize, validSHA) {
			t.Errorf("expected VerifyBinary to return true for valid file")
		}
	})

	t.Run("NonexistentFile", func(t *testing.T) {
		if VerifyBinary(filepath.Join(tempDir, "nonexistent"), validSize, validSHA) {
			t.Errorf("expected VerifyBinary to return false for nonexistent file")
		}
	})

	t.Run("DirectoryPath", func(t *testing.T) {
		if VerifyBinary(tempDir, validSize, validSHA) {
			t.Errorf("expected VerifyBinary to return false for directory path")
		}
	})

	t.Run("SizeMismatch", func(t *testing.T) {
		if VerifyBinary(validPath, validSize+10, validSHA) {
			t.Errorf("expected VerifyBinary to return false for size mismatch")
		}
	})

	t.Run("ChecksumMismatch", func(t *testing.T) {
		mismatchedSHA := "0000000000000000000000000000000000000000000000000000000000000000"
		if VerifyBinary(validPath, validSize, mismatchedSHA) {
			t.Errorf("expected VerifyBinary to return false for checksum mismatch")
		}
	})

	t.Run("SymlinkRefused", func(t *testing.T) {
		symlinkPath := filepath.Join(tempDir, "symlink_file")
		symErr := os.Symlink(validPath, symlinkPath)
		if symErr != nil {
			t.Fatalf("creating symlink: %v", symErr)
		}
		if VerifyBinary(symlinkPath, validSize, validSHA) {
			t.Errorf("expected VerifyBinary to return false for symlink")
		}
	})
}

func TestVerifyVariant(t *testing.T) {
	tempDir := t.TempDir()
	payload := []byte("cache test variant payload")
	h := sha256.Sum256(payload)
	validSHA := hex.EncodeToString(h[:])

	entry := &format.VariantEntry{
		Level:            "v1",
		SHA256:           validSHA,
		UncompressedSize: int64(len(payload)),
	}

	t.Run("InvalidChecksumFormat", func(t *testing.T) {
		invalidEntry := &format.VariantEntry{
			Level:  "v1",
			SHA256: "not-a-valid-sha256",
		}
		res := VerifyVariant(invalidEntry, tempDir)
		if res.Valid || res.Status != format.PrewarmStatusCorrupted {
			t.Errorf("expected status 'corrupted', got %+v", res)
		}
	})

	t.Run("InvalidCacheDir", func(t *testing.T) {
		res := VerifyVariant(entry, filepath.Join(tempDir, "missing_parent", "cache"))
		if res.Valid || res.Status != format.PrewarmStatusMissing {
			t.Errorf("expected status 'missing', got %+v", res)
		}
	})

	t.Run("AutoResolvedCacheDir", func(t *testing.T) {
		cacheHome := filepath.Join(tempDir, "auto_cache")
		t.Setenv("XDG_CACHE_HOME", cacheHome)
		res := VerifyVariant(entry, "")
		if res.Valid || res.Status != format.PrewarmStatusMissing {
			t.Errorf("expected status 'missing', got %+v", res)
		}
	})

	t.Run("MissingBinary", func(t *testing.T) {
		res := VerifyVariant(entry, tempDir)
		if res.Valid || res.Status != format.PrewarmStatusMissing {
			t.Errorf("expected status 'missing', got %+v", res)
		}
	})

	t.Run("TruncatedBinary", func(t *testing.T) {
		cacheDir := filepath.Join(tempDir, "trunc_dir")
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		targetPath := filepath.Join(cacheDir, entry.SHA256)
		if err := os.WriteFile(targetPath, []byte("short"), 0o700); err != nil {
			t.Fatalf("write: %v", err)
		}

		res := VerifyVariant(entry, cacheDir)
		if res.Valid || res.Status != format.PrewarmStatusCorrupted || !res.AlreadyCached {
			t.Errorf("expected corrupted and alreadyCached, got %+v", res)
		}
		// Non-modifying verify should preserve file on disk
		if _, statErr := os.Stat(targetPath); statErr != nil {
			t.Errorf("expected file to remain on disk: %v", statErr)
		}
	})

	t.Run("CorruptedBinary", func(t *testing.T) {
		cacheDir := filepath.Join(tempDir, "corrupt_dir")
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		targetPath := filepath.Join(cacheDir, entry.SHA256)
		corruptBytes := bytes.Repeat([]byte{0xEE}, int(entry.UncompressedSize))
		if err := os.WriteFile(targetPath, corruptBytes, 0o700); err != nil {
			t.Fatalf("write: %v", err)
		}

		res := VerifyVariant(entry, cacheDir)
		if res.Valid || res.Status != format.PrewarmStatusCorrupted || !res.AlreadyCached {
			t.Errorf("expected corrupted and alreadyCached, got %+v", res)
		}
		// Non-modifying verify should preserve file on disk
		if _, statErr := os.Stat(targetPath); statErr != nil {
			t.Errorf("expected file to remain on disk: %v", statErr)
		}
	})

	t.Run("ValidBinary", func(t *testing.T) {
		cacheDir := filepath.Join(tempDir, "valid_dir")
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		targetPath := filepath.Join(cacheDir, entry.SHA256)
		if err := os.WriteFile(targetPath, payload, 0o700); err != nil {
			t.Fatalf("write: %v", err)
		}

		res := VerifyVariant(entry, cacheDir)
		if !res.Valid || res.Status != format.PrewarmStatusValid || !res.AlreadyCached {
			t.Errorf("expected valid and alreadyCached, got %+v", res)
		}
	})
}

func TestOpenAndValidateFD_LifecycleAndPurge(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	payload := []byte("descriptor-bound lifecycle test content")
	h := sha256.Sum256(payload)
	validSHA := hex.EncodeToString(h[:])
	validSize := int64(len(payload))

	t.Run("NonexistentFile", func(t *testing.T) {
		fd, err := OpenAndValidateFD(filepath.Join(tempDir, "does_not_exist"), validSize, validSHA, true)
		if err == nil {
			_ = closeFD(fd)
			t.Fatalf("expected error on nonexistent file, got nil")
		}
		if fd != -1 {
			t.Fatalf("expected fd == -1 on error, got %d", fd)
		}
	})

	t.Run("CustomOpenerFailure", func(t *testing.T) {
		simErr := errors.New("simulated opener failure")
		fd, err := OpenAndValidateFDWithOpener(
			"any_path", validSize, validSHA, true,
			func(string) (int, error) { return -1, simErr },
		)
		if !errors.Is(err, simErr) {
			t.Fatalf("expected simulated error, got: %v", err)
		}
		if fd != -1 {
			t.Fatalf("expected fd == -1 on error, got %d", fd)
		}
	})

	t.Run("NonRegularDirectory_NeverPurged", func(t *testing.T) {
		dirPath := filepath.Join(tempDir, "test_dir_target")
		if err := os.MkdirAll(dirPath, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		fd, err := OpenAndValidateFD(dirPath, validSize, validSHA, true)
		if !errors.Is(err, ErrNonRegularFile) {
			t.Fatalf("expected ErrNonRegularFile, got: %v", err)
		}
		if fd != -1 {
			t.Fatalf("expected fd == -1 on error, got %d", fd)
		}

		// Assert directory was NOT unlinked
		fi, statErr := os.Stat(dirPath)
		if statErr != nil || !fi.IsDir() {
			t.Fatalf("non-regular directory should remain on disk: %v", statErr)
		}
	})

	t.Run("TruncatedFile_PurgedWhenRequested", func(t *testing.T) {
		path := filepath.Join(tempDir, "trunc_purge_test")
		if err := os.WriteFile(path, []byte("short"), 0o700); err != nil {
			t.Fatalf("write: %v", err)
		}

		fd, err := OpenAndValidateFD(path, validSize, validSHA, true)
		if !errors.Is(err, ErrSizeMismatch) {
			t.Fatalf("expected ErrSizeMismatch, got: %v", err)
		}
		if fd != -1 {
			t.Fatalf("expected fd == -1 on error, got %d", fd)
		}

		// Assert file WAS unlinked
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("expected file to be unlinked, stat returned: %v", statErr)
		}
	})

	t.Run("TruncatedFile_RetainedWhenNotRequested", func(t *testing.T) {
		path := filepath.Join(tempDir, "trunc_retain_test")
		if err := os.WriteFile(path, []byte("short"), 0o700); err != nil {
			t.Fatalf("write: %v", err)
		}

		fd, err := OpenAndValidateFD(path, validSize, validSHA, false)
		if !errors.Is(err, ErrSizeMismatch) {
			t.Fatalf("expected ErrSizeMismatch, got: %v", err)
		}
		if fd != -1 {
			t.Fatalf("expected fd == -1 on error, got %d", fd)
		}

		// Assert file WAS NOT unlinked
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("expected file to remain on disk: %v", statErr)
		}
	})

	t.Run("CorruptedFile_PurgedWhenRequested", func(t *testing.T) {
		path := filepath.Join(tempDir, "corrupt_purge_test")
		tampered := bytes.Repeat([]byte{0x77}, int(validSize))
		if err := os.WriteFile(path, tampered, 0o700); err != nil {
			t.Fatalf("write: %v", err)
		}

		fd, err := OpenAndValidateFD(path, validSize, validSHA, true)
		if !errors.Is(err, ErrChecksumMismatch) {
			t.Fatalf("expected ErrChecksumMismatch, got: %v", err)
		}
		if fd != -1 {
			t.Fatalf("expected fd == -1 on error, got %d", fd)
		}

		// Assert file WAS unlinked
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("expected file to be unlinked, stat returned: %v", statErr)
		}
	})

	t.Run("ValidFile_PinnedOpenFD", func(t *testing.T) {
		path := filepath.Join(tempDir, "valid_pinned_test")
		if err := os.WriteFile(path, payload, 0o700); err != nil {
			t.Fatalf("write: %v", err)
		}

		entry := &format.VariantEntry{
			Level:            "v1",
			SHA256:           validSHA,
			UncompressedSize: validSize,
		}

		fd, err := OpenAndValidateVariantFD(path, entry, true)
		if err != nil {
			t.Fatalf("expected success, got: %v", err)
		}
		defer func() { _ = closeFD(fd) }()
		if fd < 0 {
			t.Fatalf("expected valid non-negative fd, got %d", fd)
		}
	})
}
