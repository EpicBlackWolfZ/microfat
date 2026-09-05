//go:build !unix && !linux && !darwin && !freebsd && !openbsd && !netbsd

package format

import (
	"fmt"
	"os"
)

var (
	lstatFunc   = os.Lstat
	geteuidFunc = func() int { return 0 }
	chmodFunc   = os.Chmod
)

// validateCacheDirSecurity checks that an existing directory satisfies microfat security invariants on non-Unix platforms.
func validateCacheDirSecurity(dir string) error {
	fi, err := lstatFunc(dir)
	if err != nil {
		return fmt.Errorf("%w: unable to stat cache directory %s: %w", ErrInsecureCacheDir, dir, err)
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: cache directory root cannot be a symlink: %s", ErrInsecureCacheDir, dir)
	}

	if !fi.IsDir() {
		return fmt.Errorf("%w: cache path is not a directory: %s", ErrInsecureCacheDir, dir)
	}

	return nil
}

// isDirOwnedByCurrentUID reports whether the file info is owned by the current process effective UID on non-Unix platforms.
func isDirOwnedByCurrentUID(fi os.FileInfo) bool {
	return true
}
