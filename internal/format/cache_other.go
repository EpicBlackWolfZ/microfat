//go:build !unix && !linux && !darwin && !freebsd && !openbsd && !netbsd

package format

import (
	"fmt"
	"os"
)

var (
	lstatFunc    = os.Lstat
	geteuidFunc  = func() int { return 0 }
	chmodFunc    = os.Chmod
	closeDirFunc = func(int) error { return nil }
)

// OpenAndValidateCacheDirFD validates the cache directory on non-Unix platforms.
// Since descriptor-based pinning is Unix-specific, it returns -1 as the descriptor on success.
func OpenAndValidateCacheDirFD(dir string, allowRemediate bool) (int, error) {
	fi, err := lstatFunc(dir)
	if err != nil {
		return -1, fmt.Errorf("%w: unable to stat cache directory %s: %w", ErrInsecureCacheDir, dir, err)
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		return -1, fmt.Errorf("%w: cache directory root cannot be a symlink: %s", ErrInsecureCacheDir, dir)
	}

	if !fi.IsDir() {
		return -1, fmt.Errorf("%w: cache path is not a directory: %s", ErrInsecureCacheDir, dir)
	}

	if allowRemediate {
		_ = chmodFunc(dir, PrivateCacheDirMode)
	}

	return -1, nil
}

// validateCacheDirSecurity checks that an existing directory satisfies microfat security invariants on non-Unix platforms.
func validateCacheDirSecurity(dir string) error {
	_, err := OpenAndValidateCacheDirFD(dir, false)
	return err
}

// isDirOwnedByCurrentUID reports whether the file info is owned by the current process effective UID on non-Unix platforms.
func isDirOwnedByCurrentUID(fi os.FileInfo) bool {
	return true
}

