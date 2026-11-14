package cache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

// ObservationStatus describes a read-only snapshot of a selected cache entry.
type ObservationStatus string

const (
	// ObservationValid means the opened descriptor passed the canonical metadata, size and SHA-256 checks.
	ObservationValid ObservationStatus = "valid"
	// ObservationMissing means an explicit directory or entry was absent when observed.
	ObservationMissing ObservationStatus = "missing"
	// ObservationCorruptSafe means a safe regular entry failed executable, size or content validation.
	ObservationCorruptSafe ObservationStatus = "corrupt_safe"
	// ObservationUnsafe means the directory or entry violates cache security requirements.
	ObservationUnsafe ObservationStatus = "unsafe"
	// ObservationUnavailable means a directory or entry could not be inspected or securely discovered.
	ObservationUnavailable ObservationStatus = "unavailable"
)

// VariantObservation describes existing cache state without authorizing a future cache hit.
// CachedPath is empty when automatic discovery cannot identify an existing secure directory.
type VariantObservation struct {
	Status     ObservationStatus
	CachedPath string
	Error      string
}

// ObserveVariant reads and hashes an existing entry using descriptor-bound validation.
// It never creates directories, repairs permissions, removes entries or materializes payloads.
// Automatic discovery follows the existing read-only fallback cascade, which can differ
// from a later mutating resolution. A later cache hit must validate its own descriptor.
func ObserveVariant(entry *format.VariantEntry, cacheDir string) VariantObservation {
	return observeVariantWithOpener(entry, cacheDir, nil)
}

func observeVariantWithOpener(
	entry *format.VariantEntry,
	cacheDir string,
	opener func(int, string) (int, error),
) VariantObservation {
	res := VariantObservation{Status: ObservationUnavailable}
	if entry == nil {
		res.Error = "cannot observe a nil variant entry"
		return res
	}
	if entry.SHA256 == "" || !format.ValidateChecksum(entry.SHA256) || entry.UncompressedSize < 0 {
		res.Error = "variant must declare a valid SHA-256 identity and nonnegative raw size"
		return res
	}

	// Keep the caller's explicit destination visible even when it does not exist.
	requestedDir := cacheDir
	if requestedDir == "" {
		requestedDir = os.Getenv(format.EnvCacheDir)
	}
	if requestedDir != "" {
		res.CachedPath = filepath.Join(filepath.Clean(requestedDir), entry.SHA256)
	}

	dirFD, resolvedDir, err := format.ResolveExistingCacheDirFD(cacheDir)
	if err != nil {
		res.Status = classifyObservationError(err)
		res.Error = fmt.Sprintf("observing cache directory: %v", err)
		return res
	}
	defer func() { _ = CloseFD(dirFD) }()
	res.CachedPath = filepath.Join(resolvedDir, entry.SHA256)

	fd, err := OpenAndValidateVariantAtFDWithOpener(dirFD, entry.SHA256, entry, false, opener)
	if err != nil {
		res.Status = classifyObservationError(err)
		res.Error = err.Error()
		return res
	}
	_ = CloseFD(fd)
	res.Status = ObservationValid
	return res
}

func classifyObservationError(err error) ObservationStatus {
	switch {
	case errors.Is(err, ErrNonRegularFile), errors.Is(err, ErrUnsafeFile), IsSymlinkErr(err):
		return ObservationUnsafe
	case isNotExistErr(err):
		return ObservationMissing
	case errors.Is(err, ErrNotExecutable), errors.Is(err, ErrSizeMismatch), errors.Is(err, ErrChecksumMismatch):
		return ObservationCorruptSafe
	case errors.Is(err, format.ErrInsecureCacheDir):
		// Directory metadata refusals have no underlying errno. Operational failures
		// such as EACCES or EMFILE are unavailable observations, not metadata proof.
		var errno syscall.Errno
		if !errors.As(err, &errno) {
			return ObservationUnsafe
		}
		return ObservationUnavailable
	default:
		return ObservationUnavailable
	}
}
