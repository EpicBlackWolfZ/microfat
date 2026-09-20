//go:build !unix

package inputfile

import "os"

func openNonblock(path string) (*os.File, error) {
	return os.Open(path) // #nosec G304 -- caller-selected input, descriptor validated
}
