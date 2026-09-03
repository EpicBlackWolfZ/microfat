// Package cache provides canonical, descriptor-bound cache file verification and lifecycle management.
// It establishes a single unified security primitive for all cache-backed execution, prewarming, and verification paths.
package cache

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

// Common cache validation errors.
var (
	// ErrNonRegularFile indicates that the cache entry is not a standard regular file (e.g. symlink or directory).
	ErrNonRegularFile = errors.New("cached path is not a regular file")

	// ErrSizeMismatch indicates that the cache file length does not match the expected uncompressed variant size.
	ErrSizeMismatch = errors.New("cache file size mismatch")

	// ErrChecksumMismatch indicates that the cache file SHA-256 payload digest failed verification.
	ErrChecksumMismatch = format.ErrPayloadCorrupted
)

const (
	verifyBufferSize = 32768
)

// VerifyBinary validates that a cached binary exists, is a regular file (not a symlink),
// matches expectedSize, and strictly matches expectedSHA256 over an open descriptor.
// It closes the descriptor prior to returning.
func VerifyBinary(path string, expectedSize int64, expectedSHA256 string) bool {
	fd, err := OpenAndValidateFD(path, expectedSize, expectedSHA256, false)
	if err != nil {
		return false
	}
	_ = closeFD(fd)
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
	_ = closeFD(fd)

	res.AlreadyCached = true
	res.Valid = true
	res.Status = format.PrewarmStatusValid
	return res
}
