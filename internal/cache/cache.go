// Package cache provides canonical, descriptor-bound cache file verification and lifecycle management.
// It establishes a single unified security primitive for all cache-backed execution, prewarming, and verification paths.
package cache

import (
	"errors"

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

	// ErrUnsupportedPlatform indicates that descriptor-bound cache operations are not supported on non-Unix platforms.
	ErrUnsupportedPlatform = errors.New("descriptor-bound cache operations are not supported on non-Unix platforms")
)

const (
	verifyBufferSize = 32768
)
