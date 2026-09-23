//go:build !unix

package builder

import (
	"os"
)

func fileDevIno(fi os.FileInfo) (dev uint64, ino uint64, ok bool) {
	return 0, 0, false
}
