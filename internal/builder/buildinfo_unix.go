//go:build unix

package builder

import (
	"os"
	"syscall"
)

func fileDevIno(fi os.FileInfo) (dev uint64, ino uint64, ok bool) {
	stat, valid := fi.Sys().(*syscall.Stat_t)
	if !valid {
		return 0, 0, false
	}
	return stat.Dev, stat.Ino, true
}
