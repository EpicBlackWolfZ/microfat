package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"golang.org/x/sys/unix"
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
	if err := os.Chmod(tempDir, 0o700); err != nil {
		t.Fatalf("chmod tempDir: %v", err)
	}
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

	t.Run("InsecurePermissionsCacheDir", func(t *testing.T) {
		insecureDir := filepath.Join(tempDir, "insecure_perms")
		if err := os.MkdirAll(insecureDir, 0o777); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		_ = os.Chmod(insecureDir, 0o777)
		res := VerifyVariant(entry, insecureDir)
		if res.Valid || res.Status != format.PrewarmStatusCorrupted {
			t.Errorf("expected status 'corrupted' for insecure cacheDir permissions, got %+v", res)
		}
	})

	t.Run("SymlinkCacheDir", func(t *testing.T) {
		realDir := filepath.Join(tempDir, "real_cache")
		if err := os.MkdirAll(realDir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		symlinkDir := filepath.Join(tempDir, "symlink_cache")
		if err := os.Symlink(realDir, symlinkDir); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		res := VerifyVariant(entry, symlinkDir)
		if res.Valid || res.Status != format.PrewarmStatusCorrupted {
			t.Errorf("expected status 'corrupted' for symlink cacheDir root, got %+v", res)
		}
	})

	t.Run("SymlinkCacheDirWithTrailingSlash", func(t *testing.T) {
		realDir := filepath.Join(tempDir, "real_cache_slash")
		if err := os.MkdirAll(realDir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		symlinkDir := filepath.Join(tempDir, "symlink_cache_slash")
		if err := os.Symlink(realDir, symlinkDir); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		res := VerifyVariant(entry, symlinkDir+"/")
		if res.Valid || res.Status != format.PrewarmStatusCorrupted {
			t.Errorf("expected status 'corrupted' for symlink cacheDir root with trailing slash, got %+v", res)
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
		_ = closeFD(fd)

		fd2, err2 := OpenAndValidateVariantFDWithOpener(path, entry, true, OpenFileFunc)
		if err2 != nil {
			t.Fatalf("expected success with OpenAndValidateVariantFDWithOpener, got: %v", err2)
		}
		_ = closeFD(fd2)

		fd3, err3 := OpenAndValidateFDWithOpener(path, validSize, validSHA, true, nil)
		if err3 != nil {
			t.Fatalf("expected success with nil opener, got: %v", err3)
		}
		_ = closeFD(fd3)
	})

	t.Run("ValidFile_DescriptorPinningSurvivesUnlinkAndReplace", func(t *testing.T) {
		path := filepath.Join(tempDir, "pinning_unlink_test")
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

		// Attacker replaces pathname by renaming a tampered file over it
		tamperedBytes := bytes.Repeat([]byte{0xBA, 0xAD}, int(validSize))
		tamperedPath := path + ".tampered"
		if err := os.WriteFile(tamperedPath, tamperedBytes, 0o700); err != nil {
			t.Fatalf("write tampered replacement: %v", err)
		}
		if err := os.Rename(tamperedPath, path); err != nil {
			t.Fatalf("rename over path: %v", err)
		}

		// Verify that reading from the pinned FD still yields the EXACT original validated bytes
		readBuf := make([]byte, validSize)
		n, preadErr := unix.Pread(fd, readBuf, 0)
		if preadErr != nil {
			t.Fatalf("pread from pinned descriptor failed: %v", preadErr)
		}
		if int64(n) != validSize {
			t.Fatalf("expected %d bytes from pinned descriptor, got %d", validSize, n)
		}
		if !bytes.Equal(readBuf, payload) {
			t.Fatalf("pinned descriptor leaked tampered content: expected %q, got %q", payload, readBuf)
		}

		// Also test unlinking completely
		if err := os.Remove(path); err != nil {
			t.Fatalf("unlink path: %v", err)
		}

		// The unlinked descriptor remains completely readable and valid
		n2, preadErr2 := unix.Pread(fd, readBuf, 0)
		if preadErr2 != nil || int64(n2) != validSize || !bytes.Equal(readBuf, payload) {
			t.Fatalf("pinned descriptor failed after unlink: err=%v, n=%d", preadErr2, n2)
		}
	})
}

func TestOpenAndValidateAtFD_LifecycleAndPurge(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	dirFD, err := unix.Open(tempDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open tempDir: %v", err)
	}
	defer func() { _ = unix.Close(dirFD) }()

	payload := []byte("descriptor-bound at-fd lifecycle test content 12345")
	h := sha256.Sum256(payload)
	validSHA := hex.EncodeToString(h[:])
	validSize := int64(len(payload))

	t.Run("NonexistentFile", func(t *testing.T) {
		fd, err := OpenAndValidateAtFD(dirFD, "does_not_exist", validSize, validSHA, true)
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
		fd, err := OpenAndValidateAtFDWithOpener(
			dirFD, "any_name", validSize, validSHA, true,
			func(int, string) (int, error) { return -1, simErr },
		)
		if !errors.Is(err, simErr) {
			t.Fatalf("expected simulated error, got: %v", err)
		}
		if fd != -1 {
			t.Fatalf("expected fd == -1 on error, got %d", fd)
		}
	})

	t.Run("NilOpenerDefaultsToOpenFileAtFunc", func(t *testing.T) {
		fileName := "nil_opener_test"
		if err := os.WriteFile(filepath.Join(tempDir, fileName), payload, 0o700); err != nil {
			t.Fatalf("write file: %v", err)
		}
		fd, err := OpenAndValidateAtFDWithOpener(dirFD, fileName, validSize, validSHA, false, nil)
		if err != nil {
			t.Fatalf("expected nil opener to use default opener, got: %v", err)
		}
		_ = closeFD(fd)
	})

	t.Run("NonRegularDirectory_NeverPurged", func(t *testing.T) {
		subDirName := "test_subdir_target"
		subDirPath := filepath.Join(tempDir, subDirName)
		if err := os.MkdirAll(subDirPath, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		fd, err := OpenAndValidateAtFD(dirFD, subDirName, validSize, validSHA, true)
		if !errors.Is(err, ErrNonRegularFile) {
			t.Fatalf("expected ErrNonRegularFile, got: %v", err)
		}
		if fd != -1 {
			t.Fatalf("expected fd == -1 on error, got %d", fd)
		}

		// Assert directory was NOT unlinked
		fi, statErr := os.Stat(subDirPath)
		if statErr != nil || !fi.IsDir() {
			t.Fatalf("non-regular directory should remain on disk: %v", statErr)
		}
	})

	t.Run("TruncatedFile_PurgedWhenRequested", func(t *testing.T) {
		fileName := "trunc_purge_at_test"
		filePath := filepath.Join(tempDir, fileName)
		if err := os.WriteFile(filePath, []byte("short"), 0o700); err != nil {
			t.Fatalf("write: %v", err)
		}

		fd, err := OpenAndValidateAtFD(dirFD, fileName, validSize, validSHA, true)
		if !errors.Is(err, ErrSizeMismatch) {
			t.Fatalf("expected ErrSizeMismatch, got: %v", err)
		}
		if fd != -1 {
			t.Fatalf("expected fd == -1 on error, got %d", fd)
		}

		// Assert file WAS unlinked
		if _, statErr := os.Stat(filePath); !os.IsNotExist(statErr) {
			t.Fatalf("expected file to be unlinked, stat returned: %v", statErr)
		}
	})

	t.Run("TruncatedFile_RetainedWhenNotRequested", func(t *testing.T) {
		fileName := "trunc_retain_at_test"
		filePath := filepath.Join(tempDir, fileName)
		if err := os.WriteFile(filePath, []byte("short"), 0o700); err != nil {
			t.Fatalf("write: %v", err)
		}

		fd, err := OpenAndValidateAtFD(dirFD, fileName, validSize, validSHA, false)
		if !errors.Is(err, ErrSizeMismatch) {
			t.Fatalf("expected ErrSizeMismatch, got: %v", err)
		}
		if fd != -1 {
			t.Fatalf("expected fd == -1 on error, got %d", fd)
		}

		// Assert file WAS NOT unlinked
		if _, statErr := os.Stat(filePath); statErr != nil {
			t.Fatalf("expected file to remain on disk: %v", statErr)
		}
	})

	t.Run("CorruptedFile_PurgedWhenRequested", func(t *testing.T) {
		fileName := "corrupt_purge_at_test"
		filePath := filepath.Join(tempDir, fileName)
		tampered := bytes.Repeat([]byte{0x77}, int(validSize))
		if err := os.WriteFile(filePath, tampered, 0o700); err != nil {
			t.Fatalf("write: %v", err)
		}

		fd, err := OpenAndValidateAtFD(dirFD, fileName, validSize, validSHA, true)
		if !errors.Is(err, ErrChecksumMismatch) {
			t.Fatalf("expected ErrChecksumMismatch, got: %v", err)
		}
		if fd != -1 {
			t.Fatalf("expected fd == -1 on error, got %d", fd)
		}

		// Assert file WAS unlinked
		if _, statErr := os.Stat(filePath); !os.IsNotExist(statErr) {
			t.Fatalf("expected file to be unlinked, stat returned: %v", statErr)
		}
	})

	t.Run("ValidFile_VariantEntryAndOpener", func(t *testing.T) {
		fileName := "valid_variant_at_test"
		filePath := filepath.Join(tempDir, fileName)
		if err := os.WriteFile(filePath, payload, 0o700); err != nil {
			t.Fatalf("write: %v", err)
		}

		entry := &format.VariantEntry{
			Level:            "v1",
			SHA256:           validSHA,
			UncompressedSize: validSize,
		}

		fd, err := OpenAndValidateVariantAtFD(dirFD, fileName, entry, true)
		if err != nil {
			t.Fatalf("expected success with OpenAndValidateVariantAtFD, got: %v", err)
		}
		_ = closeFD(fd)

		fd2, err := OpenAndValidateVariantAtFDWithOpener(dirFD, fileName, entry, true, OpenFileAtFunc)
		if err != nil {
			t.Fatalf("expected success with OpenAndValidateVariantAtFDWithOpener, got: %v", err)
		}
		_ = closeFD(fd2)
	})
}

func TestMaterializeVariantAtFD(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.Chmod(tempDir, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	dirFD, err := unix.Open(tempDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open dirFD: %v", err)
	}
	defer func() { _ = unix.Close(dirFD) }()

	payload := []byte("materialize-test-payload-bytes-1234567890")
	h := sha256.Sum256(payload)
	validSHA := hex.EncodeToString(h[:])
	validSize := int64(len(payload))

	entry := &format.VariantEntry{
		Level:            "v1",
		SHA256:           validSHA,
		UncompressedSize: validSize,
	}

	t.Run("Success", func(t *testing.T) {
		path, err := MaterializeVariantAtFD(dirFD, tempDir, entry, func(w io.Writer) error {
			_, writeErr := w.Write(payload)
			return writeErr
		})
		if err != nil {
			t.Fatalf("unexpected error from MaterializeVariantAtFD: %v", err)
		}
		expectedPath := filepath.Join(tempDir, validSHA)
		if path != expectedPath {
			t.Errorf("expected path %q, got %q", expectedPath, path)
		}

		// Verify content
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed reading materialized file: %v", err)
		}
		if !bytes.Equal(data, payload) {
			t.Errorf("content mismatch")
		}

		// Verify permissions (0700)
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Errorf("expected permissions 0700, got %04o", fi.Mode().Perm())
		}
	})

	t.Run("AtomicOverwrite_CorruptedFile", func(t *testing.T) {
		targetPath := filepath.Join(tempDir, validSHA)
		corrupt := bytes.Repeat([]byte{0xAA}, int(validSize))
		if err := os.WriteFile(targetPath, corrupt, 0o700); err != nil {
			t.Fatalf("write corrupted: %v", err)
		}

		path, err := MaterializeVariantAtFD(dirFD, tempDir, entry, func(w io.Writer) error {
			_, writeErr := w.Write(payload)
			return writeErr
		})
		if err != nil {
			t.Fatalf("unexpected error on atomic overwrite: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading overwritten file: %v", err)
		}
		if !bytes.Equal(data, payload) {
			t.Errorf("expected recovered payload, got corrupted data")
		}
	})

	t.Run("PayloadWriteError_CleansUp", func(t *testing.T) {
		failEntry := &format.VariantEntry{
			Level:            "v2",
			SHA256:           strings.Repeat("b", 64),
			UncompressedSize: 100,
		}
		writeErrSim := errors.New("simulated stream error")
		_, err := MaterializeVariantAtFD(dirFD, tempDir, failEntry, func(w io.Writer) error {
			_, _ = w.Write([]byte("partial"))
			return writeErrSim
		})
		if err == nil || !errors.Is(err, format.ErrCacheExtract) {
			t.Fatalf("expected ErrCacheExtract, got: %v", err)
		}

		// Verify no dangling .tmp files
		entries, _ := os.ReadDir(tempDir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tmp") {
				t.Errorf("found dangling temp file: %s", e.Name())
			}
		}
	})

	t.Run("ChecksumMismatch_CleansUp", func(t *testing.T) {
		failEntry := &format.VariantEntry{
			Level:            "v3",
			SHA256:           strings.Repeat("c", 64),
			UncompressedSize: int64(len("wrong content")),
		}
		_, err := MaterializeVariantAtFD(dirFD, tempDir, failEntry, func(w io.Writer) error {
			_, _ = w.Write([]byte("wrong content"))
			return nil
		})
		if err == nil || !errors.Is(err, format.ErrPayloadCorrupted) {
			t.Fatalf("expected ErrPayloadCorrupted, got: %v", err)
		}

		// Verify target file was not created
		if _, statErr := os.Stat(filepath.Join(tempDir, failEntry.SHA256)); !os.IsNotExist(statErr) {
			t.Errorf("expected target file not to exist on checksum mismatch")
		}
	})

	t.Run("InvalidInputs", func(t *testing.T) {
		// Invalid dirFD
		if _, err := MaterializeVariantAtFD(-1, tempDir, entry, func(w io.Writer) error { return nil }); err == nil {
			t.Errorf("expected error for invalid dirFD")
		}

		// Nil entry
		if _, err := MaterializeVariantAtFD(dirFD, tempDir, nil, func(w io.Writer) error { return nil }); err == nil {
			t.Errorf("expected error for nil entry")
		}

		// Empty or malformed SHA256
		badSHA := &format.VariantEntry{Level: "v1", SHA256: "", UncompressedSize: 100}
		if _, err := MaterializeVariantAtFD(dirFD, tempDir, badSHA, func(w io.Writer) error { return nil }); err == nil {
			t.Errorf("expected error for empty SHA256")
		}

		// Invalid uncompressed size
		badSize := &format.VariantEntry{Level: "v1", SHA256: validSHA, UncompressedSize: 0}
		if _, err := MaterializeVariantAtFD(dirFD, tempDir, badSize, func(w io.Writer) error { return nil }); err == nil {
			t.Errorf("expected error for zero uncompressed size")
		}

		// Nil writePayload
		if _, err := MaterializeVariantAtFD(dirFD, tempDir, entry, nil); err == nil {
			t.Errorf("expected error for nil writePayload")
		}

		// Empty dirPath
		if _, err := MaterializeVariantAtFD(dirFD, "", entry, func(w io.Writer) error { return nil }); err == nil {
			t.Errorf("expected error for empty dirPath")
		}
	})

	t.Run("RenameFailure_CleansUpTemp", func(t *testing.T) {
		renameSubDir := filepath.Join(tempDir, "rename_sub")
		if err := os.MkdirAll(renameSubDir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		renameFD, err := unix.Open(renameSubDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer func() { _ = unix.Close(renameFD) }()

		dirTarget := filepath.Join(renameSubDir, validSHA)
		if err := os.MkdirAll(dirTarget, 0o700); err != nil {
			t.Fatalf("mkdir dirTarget: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dirTarget, "child"), []byte("data"), 0o600); err != nil {
			t.Fatalf("write child: %v", err)
		}

		_, err = MaterializeVariantAtFD(renameFD, renameSubDir, entry, func(w io.Writer) error {
			_, writeErr := w.Write(payload)
			return writeErr
		})
		if err == nil || !errors.Is(err, format.ErrCacheWrite) {
			t.Fatalf("expected ErrCacheWrite on rename failure, got: %v", err)
		}

		// Verify no dangling temp files
		entries, _ := os.ReadDir(renameSubDir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tmp") {
				t.Errorf("found dangling temp file after rename failure: %s", e.Name())
			}
		}
	})

	t.Run("PostRenameValidationFailure_PurgesCorruptedTarget", func(t *testing.T) {
		failSubDir := filepath.Join(tempDir, "fail_sub")
		if err := os.MkdirAll(failSubDir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		failFD, err := unix.Open(failSubDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer func() { _ = unix.Close(failFD) }()

		origOpener := OpenFileAtFunc
		OpenFileAtFunc = func(fd int, name string) (int, error) {
			if name == validSHA {
				return -1, errors.New("simulated opener failure on post-rename verification")
			}
			return origOpener(fd, name)
		}
		defer func() { OpenFileAtFunc = origOpener }()

		_, err = MaterializeVariantAtFD(failFD, failSubDir, entry, func(w io.Writer) error {
			_, writeErr := w.Write(payload)
			return writeErr
		})
		if err == nil || !errors.Is(err, format.ErrCacheWrite) {
			t.Fatalf("expected ErrCacheWrite on post-rename validation failure, got: %v", err)
		}

		// Verify target file was unlinked and purged
		targetPath := filepath.Join(failSubDir, validSHA)
		if _, statErr := os.Stat(targetPath); !os.IsNotExist(statErr) {
			t.Errorf("expected target file %s to be unlinked after validation failure, got: %v", targetPath, statErr)
		}
	})
}
