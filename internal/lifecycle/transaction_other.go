//go:build !linux

package lifecycle

import (
	"errors"
	"fmt"
	"os"
)

var (
	readXattrsFunc        = readXattrs
	readFdXattrsFunc      = readFdXattrs
	setFdXattrFunc        = setFdXattr
	removeFdXattrFunc     = removeFdXattr
	chownFunc             = func(f *os.File, uid, gid int) error { return f.Chown(uid, gid) }
	chmodFunc             = func(f *os.File, mode os.FileMode) error { return f.Chmod(mode) }
	linkFunc              = os.Link
	publishCreateOnlyFunc = publishCreateOnly
	acquireLockFunc       = acquireAdvisoryLock
	syncDirFunc           = syncDirectory
	fileStatMetadataFunc  = fileStatMetadata
)

func fileStatMetadata(_ os.FileInfo) (dev uint64, ino uint64, nlink uint64, uid int, gid int, ok bool) {
	return 0, 0, 1, os.Geteuid(), os.Getegid(), false
}

func readXattrs(_ string) (map[string][]byte, error) {
	return nil, nil
}

func readFdXattrs(_ int) (map[string][]byte, error) {
	return nil, nil
}

func setFdXattr(_ int, _ string, _ []byte) error {
	return nil
}

func removeFdXattr(_ int, _ string) error {
	return nil
}

func publishCreateOnly(stagedPath, destPath string) error {
	if _, err := os.Lstat(destPath); err == nil {
		return fmt.Errorf("%w: %s", ErrDestinationExists, destPath)
	}
	return os.Rename(stagedPath, destPath)
}

func acquireAdvisoryLock(_ string) (func(), error) {
	return func() {}, nil
}

func syncDirectory(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
