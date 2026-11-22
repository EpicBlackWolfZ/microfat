// Package inputfile opens caller-selected regular inputs without waiting for FIFO peers.
package inputfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	// ErrNotRegular indicates a target failed regular-file or non-empty validation.
	ErrNotRegular = errors.New("input must be a nonempty regular file")
	// ErrSymlink indicates a symlink was encountered where symlinks are forbidden.
	ErrSymlink = errors.New("input must not be a symlink")
)

// Open follows regular-file symlinks, but validates the opened descriptor rather
// than a prior pathname stat. Callers enforce their own format-specific size/read
// bounds. The descriptor is close-on-exec and must be closed by the caller.
func Open(path string) (*os.File, error) {
	return openRegular(filepath.Clean(path), openNonblock)
}

// OpenRegularNoFollow opens an existing regular file without following symlinks
// or blocking on FIFOs. Zero-byte regular files are allowed, and the opened
// descriptor is verified against the pre-open Lstat via os.SameFile.
func OpenRegularNoFollow(path string) (*os.File, os.FileInfo, error) {
	return openRegularNoFollow(filepath.Clean(path), openNoFollowNonblock)
}

func openRegular(path string, open func(string) (*os.File, error)) (*os.File, error) {
	f, _, err := openValidated(path, false, nil, open)
	return f, err
}

func openRegularNoFollow(path string, open func(string) (*os.File, error)) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("%s: %w", path, ErrSymlink)
	}
	if !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s: %w", path, ErrNotRegular)
	}
	return openValidated(path, true, before, open)
}

func openValidated(
	path string,
	allowEmpty bool,
	expected os.FileInfo,
	open func(string) (*os.File, error),
) (*os.File, os.FileInfo, error) {
	f, err := open(path)
	if err != nil {
		return nil, nil, err
	}
	stat, err := f.Stat()
	if err == nil && !matchesRegularExpectation(stat, allowEmpty, expected) {
		err = ErrNotRegular
	}
	if err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, stat, nil
}

func matchesRegularExpectation(stat os.FileInfo, allowEmpty bool, expected os.FileInfo) bool {
	if !stat.Mode().IsRegular() {
		return false
	}
	if !allowEmpty && stat.Size() <= 0 {
		return false
	}
	if expected != nil && !os.SameFile(expected, stat) {
		return false
	}
	return true
}
