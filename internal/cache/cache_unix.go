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

// OpenFileAtFunc defines the directory-relative descriptor opener enforcing O_NOFOLLOW and O_CLOEXEC.
var OpenFileAtFunc = func(dirFD int, name string) (int, error) {
	return unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
}

var unlinkAtFunc = unix.Unlinkat

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

// OpenAndValidateVariantAtFD opens and validates variant name relative to dirFD against expected size and SHA-256.
// If removeOnCorrupt is true, corrupted regular files are unlinked relative to dirFD via unlinkat.
// Non-regular files (symlinks, directories) are rejected with an error without modifying the filesystem.
func OpenAndValidateVariantAtFD(dirFD int, name string, entry *format.VariantEntry, removeOnCorrupt bool) (int, error) {
	return OpenAndValidateAtFDWithOpener(dirFD, name, entry.UncompressedSize, entry.SHA256, removeOnCorrupt, OpenFileAtFunc)
}

// OpenAndValidateVariantAtFDWithOpener opens and validates variant name relative to dirFD using an injected opener func.
func OpenAndValidateVariantAtFDWithOpener(
	dirFD int,
	name string,
	entry *format.VariantEntry,
	removeOnCorrupt bool,
	opener func(int, string) (int, error),
) (int, error) {
	return OpenAndValidateAtFDWithOpener(dirFD, name, entry.UncompressedSize, entry.SHA256, removeOnCorrupt, opener)
}

// OpenAndValidateAtFD opens name relative to dirFD with O_NOFOLLOW, asserts that the open descriptor is a regular file
// with exact byte length matching expectedSize, and verifies expectedSHA256 via pread on the same descriptor.
// If removeOnCorrupt is true, corrupted regular files are unlinked relative to dirFD via unlinkat.
// Non-regular files are rejected without removing.
// On success, returns the pinned, validated descriptor. The caller is responsible for closing it.
func OpenAndValidateAtFD(dirFD int, name string, expectedSize int64, expectedSHA256 string, removeOnCorrupt bool) (int, error) {
	return OpenAndValidateAtFDWithOpener(dirFD, name, expectedSize, expectedSHA256, removeOnCorrupt, OpenFileAtFunc)
}

// OpenAndValidateAtFDWithOpener opens name relative to dirFD using an injected opener func.
func OpenAndValidateAtFDWithOpener(
	dirFD int,
	name string,
	expectedSize int64,
	expectedSHA256 string,
	removeOnCorrupt bool,
	opener func(int, string) (int, error),
) (int, error) {
	if opener == nil {
		opener = OpenFileAtFunc
	}

	fd, err := opener(dirFD, name)
	if err != nil {
		return -1, err
	}

	onCorrupt := func() {
		if removeOnCorrupt {
			_ = unlinkAtFunc(dirFD, name, 0)
		}
	}

	return validateOpenedDescriptor(fd, name, expectedSize, expectedSHA256, onCorrupt)
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

	onCorrupt := func() {
		if removeOnCorrupt {
			_ = os.Remove(path)
		}
	}

	return validateOpenedDescriptor(fd, path, expectedSize, expectedSHA256, onCorrupt)
}

func validateOpenedDescriptor(fd int, label string, expectedSize int64, expectedSHA256 string, onCorrupt func()) (int, error) {
	var stat unix.Stat_t
	if statErr := unix.Fstat(fd, &stat); statErr != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("fstat cache descriptor: %w", statErr)
	}

	isRegular := (stat.Mode & unix.S_IFMT) == unix.S_IFREG
	if !isRegular {
		_ = unix.Close(fd)
		// Do NOT remove non-regular targets (such as symlinks or directories)
		return -1, fmt.Errorf("%w: %s (mode 0o%o)", ErrNonRegularFile, label, stat.Mode)
	}

	if stat.Size != expectedSize {
		_ = unix.Close(fd)
		if onCorrupt != nil {
			onCorrupt()
		}
		return -1, fmt.Errorf("%w: %s (expected %d bytes, got %d bytes)", ErrSizeMismatch, label, expectedSize, stat.Size)
	}

	if !verifyCachedFD(fd, expectedSHA256) {
		_ = unix.Close(fd)
		if onCorrupt != nil {
			onCorrupt()
		}
		return -1, fmt.Errorf("%w: cache file %s checksum mismatch", ErrChecksumMismatch, label)
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

	var dirFD = -1
	if cacheDir == "" {
		resolvedFD, resolved, err := format.ResolveCacheDirFD("")
		if err != nil {
			res.Status = format.PrewarmStatusMissing
			res.Error = fmt.Sprintf("resolving cache directory: %v", err)
			return res
		}
		cacheDir = resolved
		dirFD = resolvedFD
	} else {
		resolvedFD, err := format.OpenAndValidateCacheDirFD(cacheDir, false)
		if err == nil {
			dirFD = resolvedFD
		}
	}
	if dirFD >= 0 {
		defer func() { _ = unix.Close(dirFD) }()
	}

	if entry.SHA256 == "" || !format.ValidateChecksum(entry.SHA256) {
		res.Status = format.PrewarmStatusCorrupted
		res.Error = fmt.Sprintf("invalid checksum format %q", entry.SHA256)
		return res
	}

	cleanDir := filepath.Clean(cacheDir)
	cachedName := filepath.Clean(entry.SHA256)
	cachedBinary := filepath.Join(cleanDir, cachedName)
	res.CachedPath = cachedBinary

	var fd int
	var err error
	if dirFD >= 0 {
		fd, err = OpenAndValidateVariantAtFD(dirFD, cachedName, entry, false)
	} else {
		fd, err = OpenAndValidateFD(cachedBinary, entry.UncompressedSize, entry.SHA256, false)
	}
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
