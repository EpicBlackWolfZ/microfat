//go:build unix

package inputfile

import (
	"os"

	"golang.org/x/sys/unix"
)

func openNonblock(path string) (*os.File, error) {
	// O_NONBLOCK prevents a substituted FIFO from waiting for a writer. Regular
	// files are unaffected. os.OpenFile also sets close-on-exec atomically.
	return os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0) // #nosec G304 -- caller-selected input, descriptor validated
}
