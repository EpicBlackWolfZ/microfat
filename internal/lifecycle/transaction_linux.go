//go:build linux

package lifecycle

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

var (
	readXattrsFunc        = readXattrs
	setFdXattrFunc        = setFdXattr
	chownFunc             = func(f *os.File, uid, gid int) error { return f.Chown(uid, gid) }
	chmodFunc             = func(f *os.File, mode os.FileMode) error { return f.Chmod(mode) }
	publishCreateOnlyFunc = publishCreateOnly
	acquireLockFunc       = acquireAdvisoryLock
	syncDirFunc           = syncDirectory
	renameat2Func         = unix.Renameat2
	flockFunc             = unix.Flock
	listxattrFunc         = unix.Listxattr
	getxattrFunc          = unix.Getxattr
	fsetxattrFunc         = unix.Fsetxattr
	fileStatMetadataFunc  = fileStatMetadata
)

func fileStatMetadata(fi os.FileInfo) (dev uint64, ino uint64, nlink uint64, uid int, gid int, ok bool) {
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, 1, os.Geteuid(), os.Getegid(), false
	}
	//nolint:unconvert // stat.Nlink is uint32 on linux/arm64 and uint64 on linux/amd64
	return stat.Dev, stat.Ino, uint64(stat.Nlink), int(stat.Uid), int(stat.Gid), true
}

func readXattrs(path string) (map[string][]byte, error) {
	sz, err := listxattrFunc(path, nil)
	if err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOSYS) {
			return nil, nil
		}
		return nil, err
	}
	if sz <= 0 {
		return nil, nil
	}

	buf := make([]byte, sz)
	sz, err = listxattrFunc(path, buf)
	if err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOSYS) {
			return nil, nil
		}
		return nil, err
	}

	rawAttrs := bytes.Split(buf[:sz], []byte{0})
	xattrs := make(map[string][]byte)
	for _, raw := range rawAttrs {
		if len(raw) == 0 {
			continue
		}
		attr := string(raw)
		valSz, err := getxattrFunc(path, attr, nil)
		if err != nil {
			if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
				continue
			}
			return nil, err
		}
		val := make([]byte, valSz)
		_, err = getxattrFunc(path, attr, val)
		if err != nil {
			if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
				continue
			}
			return nil, err
		}
		xattrs[attr] = val
	}
	return xattrs, nil
}

func setFdXattr(fd int, attr string, val []byte) error {
	return fsetxattrFunc(fd, attr, val, 0)
}

func publishCreateOnly(stagedPath, destPath string) error {
	err := renameat2Func(unix.AT_FDCWD, stagedPath, unix.AT_FDCWD, destPath, unix.RENAME_NOREPLACE)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("%w: %s", ErrDestinationExists, destPath)
	}
	// Fallback for filesystems that do not implement RENAME_NOREPLACE
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		if _, statErr := os.Lstat(destPath); statErr == nil {
			return fmt.Errorf("%w: %s", ErrDestinationExists, destPath)
		}
		if renameErr := renameFunc(stagedPath, destPath); renameErr != nil {
			return fmt.Errorf("fallback rename to %s: %w", destPath, renameErr)
		}
		return nil
	}
	return fmt.Errorf("publishing to %s: %w", destPath, err)
}

func acquireAdvisoryLock(path string) (func(), error) {
	// #nosec G304 -- path is validated deployment/target path
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s for advisory lock: %w", path, err)
	}

	err = flockFunc(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("%w: target %s is currently locked", ErrConcurrentTransformation, path)
		}
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}

	unlock := func() {
		_ = flockFunc(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}
	return unlock, nil
}

func syncDirectory(dir string) error {
	// #nosec G304 -- dir is parent directory of validated path
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
