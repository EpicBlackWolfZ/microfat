//go:build unix || linux || darwin || freebsd || openbsd || netbsd

package cache

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"golang.org/x/sys/unix"
)

const maxTempFileAttempts = 1000

// OpenFileFunc defines the default file descriptor opener enforcing O_NOFOLLOW and O_CLOEXEC.
var OpenFileFunc = func(path string) (int, error) {
	return unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
}

// OpenFileAtFunc defines the directory-relative descriptor opener enforcing O_NOFOLLOW and O_CLOEXEC.
var OpenFileAtFunc = func(dirFD int, name string) (int, error) {
	return unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
}

var (
	unlinkAtFunc     = unix.Unlinkat
	renameAtFunc     = unix.Renameat
	cryptoRandReader = rand.Reader
)

// CloseFD closes an open file descriptor on Unix systems, or returns nil if fd < 0.
func CloseFD(fd int) error {
	if fd < 0 {
		return nil
	}
	return unix.Close(fd)
}

func closeFD(fd int) error {
	return CloseFD(fd)
}

// IsSymlinkErr reports whether err represents a symlink traversal rejection (such as ELOOP).
func IsSymlinkErr(err error) bool {
	return errors.Is(err, unix.ELOOP) || errors.Is(err, syscall.ELOOP)
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

	var dirFD int
	var cleanDir string
	if cacheDir == "" {
		resolvedFD, resolved, err := format.ResolveCacheDirFD("")
		if err != nil {
			res.Status = format.PrewarmStatusMissing
			res.Error = fmt.Sprintf("resolving cache directory: %v", err)
			return res
		}
		cleanDir = resolved
		dirFD = resolvedFD
	} else {
		cleanDir = filepath.Clean(cacheDir)
		resolvedFD, err := format.OpenAndValidateCacheDirFD(cleanDir, false)
		if err != nil {
			if isNotExistErr(err) {
				res.Status = format.PrewarmStatusMissing
			} else {
				res.Status = format.PrewarmStatusCorrupted
			}
			res.Error = fmt.Sprintf("validating cache directory %s: %v", cacheDir, err)
			return res
		}
		dirFD = resolvedFD
	}
	defer func() { _ = CloseFD(dirFD) }()

	if entry.SHA256 == "" || !format.ValidateChecksum(entry.SHA256) {
		res.Status = format.PrewarmStatusCorrupted
		res.Error = fmt.Sprintf("invalid checksum format %q", entry.SHA256)
		return res
	}

	cachedName := filepath.Clean(entry.SHA256)
	cachedBinary := filepath.Join(cleanDir, cachedName)
	res.CachedPath = cachedBinary

	fd, err := OpenAndValidateVariantAtFD(dirFD, cachedName, entry, false)
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

func createTempFileAt(dirFD int, dirPath string) (int, string, string, error) {
	var rnd [8]byte
	for range maxTempFileAttempts {
		if _, err := io.ReadFull(cryptoRandReader, rnd[:]); err != nil {
			return -1, "", "", fmt.Errorf("generating random suffix: %w", err)
		}
		name := fmt.Sprintf(".exec-%s.tmp", hex.EncodeToString(rnd[:]))
		fd, err := unix.Openat(dirFD, name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC, format.PrivateExecMode)
		if err == nil {
			return fd, name, filepath.Join(dirPath, name), nil
		}
		if !errors.Is(err, unix.EEXIST) && !errors.Is(err, syscall.EEXIST) {
			return -1, "", "", err
		}
	}
	return -1, "", "", errors.New("failed to create temporary file in cache directory")
}

// MaterializeVariantAtFD extracts and materializes a variant payload into the cache directory
// referenced by dirFD using descriptor-bound operations:
//  1. Generates a secure temporary file name (.exec-<hex>.tmp) and opens it via Openat with O_CREAT|O_EXCL.
//  2. Streams the payload via writePayload into an io.MultiWriter(f, hasher).
//  3. Verifies the computed SHA-256 matches entry.SHA256.
//  4. Sets permissions to PrivateExecMode (0700), flushes via Sync(), and closes the descriptor.
//  5. Atomically renames the temporary file to entry.SHA256 via Renameat.
//  6. Re-opens and verifies the file descriptor via OpenAndValidateVariantAtFD before returning the validated path.
//  7. On any error, all temporary files are unlinked via Unlinkat.
//
// On Unix platforms (Linux, Darwin, BSD), this operation is strictly descriptor-bound to eliminate TOCTOU races.
func MaterializeVariantAtFD(
	dirFD int,
	dirPath string,
	entry *format.VariantEntry,
	writePayload func(w io.Writer) error,
) (string, error) {
	if dirFD < 0 {
		return "", fmt.Errorf("%w: invalid directory descriptor", format.ErrCacheWrite)
	}
	if strings.TrimSpace(dirPath) == "" {
		return "", fmt.Errorf("%w: invalid empty cache directory path", format.ErrCacheWrite)
	}
	if entry == nil {
		return "", errors.New("nil variant entry")
	}
	if entry.SHA256 == "" || !format.ValidateChecksum(entry.SHA256) {
		return "", fmt.Errorf("%w: invalid variant checksum %q", format.ErrInvalidChecksum, entry.SHA256)
	}
	if entry.UncompressedSize <= 0 || entry.UncompressedSize > format.MaxPayloadSize {
		return "", fmt.Errorf("%w: invalid variant uncompressed size %d", format.ErrPayloadTooLarge, entry.UncompressedSize)
	}
	if writePayload == nil {
		return "", errors.New("nil writePayload function")
	}

	cleanDir := filepath.Clean(dirPath)
	cachedName := filepath.Clean(entry.SHA256)
	cachedBinary := filepath.Join(cleanDir, cachedName)

	tmpFD, tmpName, tmpPath, createErr := createTempFileAt(dirFD, cleanDir)
	if createErr != nil {
		return "", fmt.Errorf("%w: cannot create temp file in %s: %w", format.ErrCacheWrite, cleanDir, createErr)
	}

	tmpFile := os.NewFile(uintptr(tmpFD), tmpPath)
	defer func() {
		if tmpFile != nil {
			_ = tmpFile.Close()
		}
		if tmpName != "" {
			_ = unlinkAtFunc(dirFD, tmpName, 0)
		}
	}()

	hasher := sha256.New()
	mw := io.MultiWriter(tmpFile, hasher)
	if err := writePayload(mw); err != nil {
		return "", fmt.Errorf("%w: writing variant payload: %w", format.ErrCacheExtract, err)
	}

	actualHex := hex.EncodeToString(hasher.Sum(nil))
	if actualHex != entry.SHA256 {
		return "", fmt.Errorf("%w: expected %s, got %s", format.ErrPayloadCorrupted, entry.SHA256, actualHex)
	}

	if err := tmpFile.Chmod(format.PrivateExecMode); err != nil {
		return "", fmt.Errorf("%w: setting permissions on %s: %w", format.ErrCacheWrite, tmpPath, err)
	}
	if err := tmpFile.Sync(); err != nil {
		return "", fmt.Errorf("%w: syncing temp cache file %s: %w", format.ErrCacheWrite, tmpPath, err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", fmt.Errorf("%w: closing temp cache file %s: %w", format.ErrCacheWrite, tmpPath, err)
	}
	tmpFile = nil

	if err := renameAtFunc(dirFD, tmpName, dirFD, cachedName); err != nil {
		return "", fmt.Errorf("%w: renaming temp cache file %s to %s: %w", format.ErrCacheWrite, tmpPath, cachedBinary, err)
	}
	tmpName = ""

	vfd, openErr := OpenAndValidateVariantAtFD(dirFD, cachedName, entry, false)
	if openErr != nil {
		_ = unlinkAtFunc(dirFD, cachedName, 0)
		return "", fmt.Errorf("%w: opening verified cache file %s: %w", format.ErrCacheWrite, cachedBinary, openErr)
	}
	_ = closeFD(vfd)

	return cachedBinary, nil
}
