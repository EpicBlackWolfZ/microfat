//go:build unix || linux || darwin || freebsd || openbsd || netbsd

package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"golang.org/x/sys/unix"
)

// OpenFileFunc defines the default file descriptor opener enforcing O_NOFOLLOW and O_CLOEXEC.
var OpenFileFunc = func(path string) (int, error) {
	return unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
}

func closeFD(fd int) error {
	return unix.Close(fd)
}

func isNotExistErr(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ENOENT)
}

// OpenAndValidateVariantFD opens and validates path against the variant entry dimensions and checksum.
// If removeOnCorrupt is true, corrupted regular files (size or checksum mismatch) are unlinked from disk.
// Non-regular files (symlinks, directories) are rejected with an error without modifying the filesystem.
func OpenAndValidateVariantFD(path string, entry *format.VariantEntry, removeOnCorrupt bool) (int, error) {
	return OpenAndValidateFDWithOpener(path, entry.UncompressedSize, entry.SHA256, removeOnCorrupt, OpenFileFunc)
}

// OpenAndValidateVariantFDWithOpener opens and validates path using an injected opener func.
func OpenAndValidateVariantFDWithOpener(
	path string,
	entry *format.VariantEntry,
	removeOnCorrupt bool,
	opener func(string) (int, error),
) (int, error) {
	return OpenAndValidateFDWithOpener(path, entry.UncompressedSize, entry.SHA256, removeOnCorrupt, opener)
}

// OpenAndValidateFD opens path with O_NOFOLLOW, asserts that the open descriptor is a regular file
// with exact byte length matching expectedSize, and verifies expectedSHA256 via pread on the same descriptor.
// If removeOnCorrupt is true, corrupted regular files are unlinked from disk to allow auto-recovery.
// Non-regular files are rejected without removing.
// On success, returns the pinned, validated descriptor. The caller is responsible for closing it.
func OpenAndValidateFD(path string, expectedSize int64, expectedSHA256 string, removeOnCorrupt bool) (int, error) {
	return OpenAndValidateFDWithOpener(path, expectedSize, expectedSHA256, removeOnCorrupt, OpenFileFunc)
}

// OpenAndValidateFDWithOpener opens and validates path using an injected descriptor opener.
func OpenAndValidateFDWithOpener(
	path string,
	expectedSize int64,
	expectedSHA256 string,
	removeOnCorrupt bool,
	opener func(string) (int, error),
) (int, error) {
	if opener == nil {
		opener = OpenFileFunc
	}

	fd, err := opener(path)
	if err != nil {
		return -1, err
	}

	var stat unix.Stat_t
	if statErr := unix.Fstat(fd, &stat); statErr != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("fstat cache descriptor: %w", statErr)
	}

	isRegular := (stat.Mode & unix.S_IFMT) == unix.S_IFREG
	if !isRegular {
		_ = unix.Close(fd)
		// Do NOT remove non-regular targets (such as symlinks or directories)
		return -1, fmt.Errorf("%w: %s (mode 0o%o)", ErrNonRegularFile, path, stat.Mode)
	}

	if stat.Size != expectedSize {
		_ = unix.Close(fd)
		if removeOnCorrupt {
			_ = os.Remove(path)
		}
		return -1, fmt.Errorf("%w: %s (expected %d bytes, got %d bytes)", ErrSizeMismatch, path, expectedSize, stat.Size)
	}

	if !verifyCachedFD(fd, expectedSHA256) {
		_ = unix.Close(fd)
		if removeOnCorrupt {
			_ = os.Remove(path)
		}
		return -1, fmt.Errorf("%w: cache file %s checksum mismatch", ErrChecksumMismatch, path)
	}

	return fd, nil
}

func verifyCachedFD(fd int, expectedHex string) bool {
	hasher := sha256.New()
	buf := make([]byte, verifyBufferSize)
	offset := int64(0)
	for {
		n, err := unix.Pread(fd, buf, offset)
		if n > 0 {
			hasher.Write(buf[:n])
			offset += int64(n)
		}
		if err != nil {
			return false
		}
		if n == 0 {
			break
		}
	}
	return hex.EncodeToString(hasher.Sum(nil)) == expectedHex
}

// VerifyBinary validates that a cached binary exists, is a regular file (not a symlink),
// matches expectedSize, and strictly matches expectedSHA256 over an open descriptor.
// It closes the descriptor prior to returning.
func VerifyBinary(path string, expectedSize int64, expectedSHA256 string) bool {
	fd, err := OpenAndValidateFD(path, expectedSize, expectedSHA256, false)
	if err != nil {
		return false
	}
	_ = unix.Close(fd)
	return true
}

// VerifyVariant inspects the cache directory for an existing variant binary, validating
// its existence, uncompressed size, and SHA-256 checksum over an open descriptor without modifying disk state.
func VerifyVariant(entry *format.VariantEntry, cacheDir string) format.PrewarmResult {
	res := format.PrewarmResult{
		Level:            entry.Level,
		SHA256:           entry.SHA256,
		UncompressedSize: entry.UncompressedSize,
	}

	if cacheDir == "" {
		resolved, err := format.ResolveCacheDir("")
		if err != nil {
			res.Status = format.PrewarmStatusMissing
			res.Error = fmt.Sprintf("resolving cache directory: %v", err)
			return res
		}
		cacheDir = resolved
	}

	if entry.SHA256 == "" || !format.ValidateChecksum(entry.SHA256) {
		res.Status = format.PrewarmStatusCorrupted
		res.Error = fmt.Sprintf("invalid checksum format %q", entry.SHA256)
		return res
	}

	cleanDir := filepath.Clean(cacheDir)
	cachedBinary := filepath.Join(cleanDir, filepath.Clean(entry.SHA256))
	res.CachedPath = cachedBinary

	fd, err := OpenAndValidateFD(cachedBinary, entry.UncompressedSize, entry.SHA256, false)
	if err != nil {
		if isNotExistErr(err) {
			res.Status = format.PrewarmStatusMissing
			res.Error = fmt.Sprintf("cached binary not found: %v", err)
			return res
		}
		res.AlreadyCached = true
		res.Status = format.PrewarmStatusCorrupted
		res.Error = err.Error()
		return res
	}
	_ = unix.Close(fd)

	res.AlreadyCached = true
	res.Valid = true
	res.Status = format.PrewarmStatusValid
	return res
}
