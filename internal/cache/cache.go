// Package cache provides canonical, descriptor-bound cache file verification and lifecycle management.
// It establishes a single unified security primitive for all cache-backed execution, prewarming, and verification paths.
package cache

import (
	"errors"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

// Common cache validation errors.
var (
	// ErrChecksumMismatch indicates that the cache file SHA-256 payload digest failed verification.
	ErrChecksumMismatch = format.ErrPayloadCorrupted

	// ErrUnsupportedPlatform indicates that descriptor-bound cache operations are not supported on non-Unix platforms.
	ErrUnsupportedPlatform = errors.New("descriptor-bound cache operations are not supported on non-Unix platforms")
)

const (
	verifyBufferSize   = 64 * 1024
	verifyHexDigestLen = 64
)
