//go:build unix || linux || darwin || freebsd || openbsd || netbsd

package format

import (
	"fmt"
	"os"
	"syscall"
)

const (
	// InsecureCacheWriteBits identifies group and world write permission bits that are forbidden on cache directories.
	InsecureCacheWriteBits = 0o022

	// PermissionMask extracts standard Unix permission bits (0777).
	PermissionMask = 0o777
)

var (
	lstatFunc   = os.Lstat
	geteuidFunc = os.Geteuid
	chmodFunc   = os.Chmod
)

// validateCacheDirSecurity checks that an existing directory satisfies microfat security invariants:
// 1. Must be a regular directory and NOT a symlink.
// 2. Must be owned by the effective UID of the current process.
// 3. Must not grant group or world write access (mode & 0o022 == 0).
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

	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%w: unable to retrieve file system attributes for %s", ErrInsecureCacheDir, dir)
	}

	euid := geteuidFunc()
	if euid < 0 || stat.Uid != uint32(euid) { // #nosec G115 -- non-negative UID conversion
		return fmt.Errorf("%w: cache directory %s owned by foreign UID %d, expected %d",
			ErrInsecureCacheDir, dir, stat.Uid, euid)
	}

	if stat.Mode&InsecureCacheWriteBits != 0 {
		return fmt.Errorf("%w: cache directory %s has insecure write permissions: %04o (group/world write not allowed)",
			ErrInsecureCacheDir, dir, stat.Mode&PermissionMask)
	}

	return nil
}

// isDirOwnedByCurrentUID reports whether the file info is owned by the current process effective UID.
func isDirOwnedByCurrentUID(fi os.FileInfo) bool {
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	euid := geteuidFunc()
	if euid < 0 {
		return false
	}
	return stat.Uid == uint32(euid) // #nosec G115 -- non-negative UID conversion
}
