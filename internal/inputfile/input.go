// Package inputfile opens caller-selected regular inputs without waiting for FIFO peers.
package inputfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrNotRegular = errors.New("input must be a nonempty regular file")

// Open follows regular-file symlinks, but validates the opened descriptor rather
// than a prior pathname stat. Callers enforce their own format-specific size/read
// bounds. The descriptor is close-on-exec and must be closed by the caller.
func Open(path string) (*os.File, error) {
	return openRegular(filepath.Clean(path), openNonblock)
}

func openRegular(path string, open func(string) (*os.File, error)) (*os.File, error) {
	f, err := open(path)
	if err != nil {
		return nil, err
	}
	stat, err := f.Stat()
	if err == nil && (!stat.Mode().IsRegular() || stat.Size() <= 0) {
		err = ErrNotRegular
	}
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}
