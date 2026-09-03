//go:build !unix && !linux && !darwin && !freebsd && !openbsd && !netbsd

package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

// OpenFileFunc defines the default file opener for non-Unix targets.
var OpenFileFunc = func(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return -1, err
	}
	return int(f.Fd()), nil
}

func closeFD(fd int) error {
	return nil
}

func isNotExistErr(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}

// OpenAndValidateVariantFD opens and validates path against the variant entry dimensions and checksum.
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

// OpenAndValidateFD opens path, asserts that it is a regular file matching expectedSize, and verifies expectedSHA256.
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
	stat, err := os.Stat(path)
	if err != nil {
		return -1, err
	}
	if !stat.Mode().IsRegular() {
		return -1, fmt.Errorf("%w: %s", ErrNonRegularFile, path)
	}
	if stat.Size() != expectedSize {
		if removeOnCorrupt {
			_ = os.Remove(path)
		}
		return -1, fmt.Errorf("%w: %s (expected %d bytes, got %d bytes)", ErrSizeMismatch, path, expectedSize, stat.Size())
	}
	f, err := os.Open(path)
	if err != nil {
		return -1, err
	}
	defer func() { _ = f.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return -1, err
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if actual != expectedSHA256 {
		if removeOnCorrupt {
			_ = os.Remove(path)
		}
		return -1, fmt.Errorf("%w: %s", ErrChecksumMismatch, path)
	}
	return int(f.Fd()), nil
}
