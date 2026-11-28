//go:build linux

package format

import (
	"fmt"

	"golang.org/x/sys/unix"
)

var fstatfsDirFunc = unix.Fstatfs

// checkCacheDirExecMount verifies on Linux that the pinned directory descriptor
// does not reside on a filesystem mounted with ST_NOEXEC.
func checkCacheDirExecMount(fd int, dir string) error {
	var statfs unix.Statfs_t
	if err := fstatfsDirFunc(fd, &statfs); err != nil {
		return nil
	}
	if statfs.Flags&unix.ST_NOEXEC != 0 {
		return fmt.Errorf("%w: %s", ErrCacheDirNoExec, dir)
	}
	return nil
}
