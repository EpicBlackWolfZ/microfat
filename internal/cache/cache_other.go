//go:build !unix && !linux && !darwin && !freebsd && !openbsd && !netbsd

package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

// OpenFileFunc returns ErrUnsupportedPlatform on non-Unix platforms where file descriptors are not supported.
var OpenFileFunc = func(path string) (int, error) {
	return -1, fmt.Errorf("%w: cannot open file descriptor on this OS", ErrUnsupportedPlatform)
}

// OpenFileAtFunc returns ErrUnsupportedPlatform on non-Unix platforms where file descriptors are not supported.
var OpenFileAtFunc = func(dirFD int, name string) (int, error) {
	return -1, fmt.Errorf("%w: cannot open file descriptor on this OS", ErrUnsupportedPlatform)
}

// CloseFD is a no-op on non-Unix systems.
func CloseFD(fd int) error {
	return nil
}

func closeFD(fd int) error {
	return CloseFD(fd)
}

// IsSymlinkErr reports whether err represents a symlink traversal rejection.
func IsSymlinkErr(err error) bool {
	return false
}

func isNotExistErr(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

// OpenAndValidateVariantFD returns ErrUnsupportedPlatform on non-Unix operating systems.
func OpenAndValidateVariantFD(path string, entry *format.VariantEntry, removeOnCorrupt bool) (int, error) {
	return -1, fmt.Errorf("%w: descriptor-bound execution is Unix-specific", ErrUnsupportedPlatform)
}

// OpenAndValidateVariantAtFD returns ErrUnsupportedPlatform on non-Unix operating systems.
func OpenAndValidateVariantAtFD(dirFD int, name string, entry *format.VariantEntry, removeOnCorrupt bool) (int, error) {
	return -1, fmt.Errorf("%w: descriptor-bound execution is Unix-specific", ErrUnsupportedPlatform)
}

// OpenAndValidateVariantAtFDWithOpener returns ErrUnsupportedPlatform on non-Unix operating systems.
func OpenAndValidateVariantAtFDWithOpener(
	dirFD int,
	name string,
	entry *format.VariantEntry,
	removeOnCorrupt bool,
	opener func(int, string) (int, error),
) (int, error) {
	return -1, fmt.Errorf("%w: descriptor-bound execution is Unix-specific", ErrUnsupportedPlatform)
}

// OpenAndValidateAtFD returns ErrUnsupportedPlatform on non-Unix operating systems.
func OpenAndValidateAtFD(dirFD int, name string, expectedSize int64, expectedSHA256 string, removeOnCorrupt bool) (int, error) {
	return -1, fmt.Errorf("%w: descriptor-bound execution is Unix-specific", ErrUnsupportedPlatform)
}

// OpenAndValidateAtFDWithOpener returns ErrUnsupportedPlatform on non-Unix operating systems.
func OpenAndValidateAtFDWithOpener(
	dirFD int,
	name string,
	expectedSize int64,
	expectedSHA256 string,
	removeOnCorrupt bool,
	opener func(int, string) (int, error),
) (int, error) {
	return -1, fmt.Errorf("%w: descriptor-bound execution is Unix-specific", ErrUnsupportedPlatform)
}

// OpenAndValidateVariantFDWithOpener returns ErrUnsupportedPlatform on non-Unix operating systems.
func OpenAndValidateVariantFDWithOpener(
	path string,
	entry *format.VariantEntry,
	removeOnCorrupt bool,
	opener func(string) (int, error),
) (int, error) {
	return -1, fmt.Errorf("%w: descriptor-bound execution is Unix-specific", ErrUnsupportedPlatform)
}

// OpenAndValidateFD returns ErrUnsupportedPlatform on non-Unix operating systems.
func OpenAndValidateFD(path string, expectedSize int64, expectedSHA256 string, removeOnCorrupt bool) (int, error) {
	return -1, fmt.Errorf("%w: descriptor-bound execution is Unix-specific", ErrUnsupportedPlatform)
}

// OpenAndValidateFDWithOpener returns ErrUnsupportedPlatform on non-Unix operating systems.
func OpenAndValidateFDWithOpener(
	path string,
	expectedSize int64,
	expectedSHA256 string,
	removeOnCorrupt bool,
	opener func(string) (int, error),
) (int, error) {
	return -1, fmt.Errorf("%w: descriptor-bound execution is Unix-specific", ErrUnsupportedPlatform)
}

// VerifyBinary validates that a cached binary exists, is a regular file, matches expectedSize,
// and strictly matches expectedSHA256 using standard portable file operations.
func VerifyBinary(path string, expectedSize int64, expectedSHA256 string) bool {
	stat, err := os.Stat(path)
	if err != nil || !stat.Mode().IsRegular() || stat.Size() != expectedSize {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return false
	}
	return hex.EncodeToString(hasher.Sum(nil)) == expectedSHA256
}

// VerifyVariant inspects the cache directory for an existing variant binary using portable file operations.
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
	} else {
		if _, err := format.OpenAndValidateCacheDirFD(cacheDir, false); err != nil {
			if isNotExistErr(err) {
				res.Status = format.PrewarmStatusMissing
			} else {
				res.Status = format.PrewarmStatusCorrupted
			}
			res.Error = fmt.Sprintf("validating cache directory %s: %v", cacheDir, err)
			return res
		}
	}

	if entry.SHA256 == "" || !format.ValidateChecksum(entry.SHA256) {
		res.Status = format.PrewarmStatusCorrupted
		res.Error = fmt.Sprintf("invalid checksum format %q", entry.SHA256)
		return res
	}

	cleanDir := filepath.Clean(cacheDir)
	cachedBinary := filepath.Join(cleanDir, filepath.Clean(entry.SHA256))
	res.CachedPath = cachedBinary

	stat, err := os.Stat(cachedBinary)
	if err != nil {
		res.Status = format.PrewarmStatusMissing
		res.Error = fmt.Sprintf("cached binary not found: %v", err)
		return res
	}

	res.AlreadyCached = true
	if !stat.Mode().IsRegular() {
		res.Status = format.PrewarmStatusCorrupted
		res.Error = fmt.Sprintf("cached binary %s is not a regular file", cachedBinary)
		return res
	}

	if stat.Size() != entry.UncompressedSize {
		res.Status = format.PrewarmStatusCorrupted
		res.Error = fmt.Sprintf("size mismatch: expected %d bytes, got %d bytes", entry.UncompressedSize, stat.Size())
		return res
	}

	f, err := os.Open(cachedBinary)
	if err != nil {
		res.Status = format.PrewarmStatusCorrupted
		res.Error = fmt.Sprintf("opening cached file for hashing: %v", err)
		return res
	}
	defer func() { _ = f.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		res.Status = format.PrewarmStatusCorrupted
		res.Error = fmt.Sprintf("hashing cached binary: %v", err)
		return res
	}

	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if entry.SHA256 != "" && actualHash != entry.SHA256 {
		res.Status = format.PrewarmStatusCorrupted
		res.Error = fmt.Sprintf("checksum mismatch: expected %s, got %s", entry.SHA256, actualHash)
		return res
	}

	res.Valid = true
	res.Status = format.PrewarmStatusValid
	return res
}

// MaterializeVariantAtFD provides a portable implementation of variant cache materialization for non-Unix platforms.
// On Unix platforms this operation is strictly descriptor-bound via openat/renameat. On platforms without openat/renameat
// semantics (e.g. Windows), it uses the platform's safe temporary file and atomic rename primitives, providing
// best-effort equivalent integrity guarantees while keeping the API signature unified across architectures.
func MaterializeVariantAtFD(
	dirFD int,
	dirPath string,
	entry *format.VariantEntry,
	writePayload func(w io.Writer) error,
) (string, error) {
	if dirPath == "" {
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
	tmpFile, err := os.CreateTemp(cleanDir, ".exec-*.tmp")
	if err != nil {
		return "", fmt.Errorf("%w: cannot create temp file in %s: %w", format.ErrCacheWrite, cleanDir, err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		if tmpFile != nil {
			_ = tmpFile.Close()
		}
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
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

	cachedName := filepath.Clean(entry.SHA256)
	cachedBinary := filepath.Join(cleanDir, cachedName)
	if err := os.Rename(tmpPath, cachedBinary); err != nil {
		return "", fmt.Errorf("%w: renaming temp cache file %s to %s: %w", format.ErrCacheWrite, tmpPath, cachedBinary, err)
	}
	tmpPath = ""

	if !VerifyBinary(cachedBinary, entry.UncompressedSize, entry.SHA256) {
		_ = os.Remove(cachedBinary)
		return "", fmt.Errorf("%w: failed to verify cached binary %s post-extraction", format.ErrCacheWrite, cachedBinary)
	}

	return cachedBinary, nil
}
