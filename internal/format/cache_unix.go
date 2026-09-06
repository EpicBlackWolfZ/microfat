//go:build unix || linux || darwin || freebsd || openbsd || netbsd

package format

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	// InsecureCacheWriteBits identifies group and world write permission bits that are forbidden on cache directories.
	InsecureCacheWriteBits = 0o022

	// PermissionMask extracts standard Unix permission bits (0777).
	PermissionMask = 0o777
)

var (
	openDirFunc = func(path string) (int, error) {
		return unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	}
	fstatDirFunc  = unix.Fstat
	fchmodDirFunc = unix.Fchmod
	closeDirFunc  = unix.Close
	geteuidFunc   = os.Geteuid
)

// OpenAndValidateCacheDirFD opens dir with O_DIRECTORY|O_NOFOLLOW|O_CLOEXEC and asserts:
//  1. Target is a regular directory and NOT a symlink.
//  2. Target is owned by the current process effective UID (os.Geteuid()).
//  3. Target does not grant group or world write access (mode & 0o022 == 0).
//
// Targeting the effective UID (os.Geteuid()) is explicitly intended to support setuid execution
// semantics, ensuring that cache directory validation and fallback paths align with process file creation
// privileges and effective runtime ownership.
//
// If allowRemediate is true and the directory is owned by the current effective UID but has group/world
// write bits, it remediates the descriptor directly via fchmod to 0700, completely eliminating pathname TOCTOU.
// On success, returns the open pinned directory descriptor (fd >= 0). The caller is responsible for closing it.
func OpenAndValidateCacheDirFD(dir string, allowRemediate bool) (int, error) {
	fd, err := openDirFunc(dir)
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, syscall.ELOOP) {
			return -1, fmt.Errorf("%w: cache directory root cannot be a symlink: %s", ErrInsecureCacheDir, dir)
		}
		if errors.Is(err, unix.ENOTDIR) || errors.Is(err, syscall.ENOTDIR) {
			if fi, lerr := os.Lstat(dir); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
				return -1, fmt.Errorf("%w: cache directory root cannot be a symlink: %s", ErrInsecureCacheDir, dir)
			}
			return -1, fmt.Errorf("%w: cache path is not a directory: %s", ErrInsecureCacheDir, dir)
		}
		return -1, fmt.Errorf("%w: unable to open cache directory %s: %w", ErrInsecureCacheDir, dir, err)
	}

	var stat unix.Stat_t
	if fstatErr := fstatDirFunc(fd, &stat); fstatErr != nil {
		_ = closeDirFunc(fd)
		return -1, fmt.Errorf("%w: unable to fstat cache directory descriptor for %s: %w",
			ErrInsecureCacheDir, dir, fstatErr)
	}

	if (stat.Mode & unix.S_IFMT) != unix.S_IFDIR {
		_ = closeDirFunc(fd)
		return -1, fmt.Errorf("%w: cache path %s is not a directory", ErrInsecureCacheDir, dir)
	}

	euid := geteuidFunc()
	if euid < 0 || stat.Uid != uint32(euid) { // #nosec G115 -- non-negative UID conversion
		_ = closeDirFunc(fd)
		return -1, fmt.Errorf("%w: cache directory %s owned by foreign UID %d, expected %d",
			ErrInsecureCacheDir, dir, stat.Uid, euid)
	}

	if stat.Mode&InsecureCacheWriteBits != 0 {
		if !allowRemediate {
			_ = closeDirFunc(fd)
			return -1, fmt.Errorf("%w: cache directory %s has insecure write permissions: %04o (group/world write not allowed)",
				ErrInsecureCacheDir, dir, stat.Mode&PermissionMask)
		}

		if chmodErr := fchmodDirFunc(fd, PrivateCacheDirMode); chmodErr != nil {
			_ = closeDirFunc(fd)
			return -1, fmt.Errorf("%w: failed to tighten cache directory %s permissions: %w",
				ErrInsecureCacheDir, dir, chmodErr)
		}

		if fstatErr := fstatDirFunc(fd, &stat); fstatErr != nil {
			_ = closeDirFunc(fd)
			return -1, fmt.Errorf("%w: unable to re-fstat cache directory %s after chmod: %w",
				ErrInsecureCacheDir, dir, fstatErr)
		}

		if stat.Mode&InsecureCacheWriteBits != 0 {
			_ = closeDirFunc(fd)
			return -1, fmt.Errorf("%w: cache directory %s still insecure after chmod: %04o",
				ErrInsecureCacheDir, dir, stat.Mode&PermissionMask)
		}
	}

	return fd, nil
}

func validateCacheDirSecurity(dir string) error {
	fd, err := OpenAndValidateCacheDirFD(dir, false)
	if err != nil {
		return err
	}
	_ = closeDirFunc(fd)
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
