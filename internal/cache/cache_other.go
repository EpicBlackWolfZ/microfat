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

func closeFD(fd int) error {
	return nil
}

func isNotExistErr(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

// OpenAndValidateVariantFD returns ErrUnsupportedPlatform on non-Unix operating systems.
func OpenAndValidateVariantFD(path string, entry *format.VariantEntry, removeOnCorrupt bool) (int, error) {
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
