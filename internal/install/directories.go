package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type mkdirFunc func(*os.Root, string, os.FileMode) error

// openInstallRoot walks from / through verified directory descriptors. In
// particular, a missing ancestor must not authorize MkdirAll to follow a raced
// symlink or to create descendants inside a directory supplied by another UID.
func openInstallRoot(name string, mkdir mkdirFunc) (*os.Root, os.FileInfo, error) {
	root, err := os.OpenRoot(string(filepath.Separator))
	if err != nil {
		return nil, nil, err
	}
	info, err := root.Stat(".")
	if err == nil {
		err = validateInfo(info, true, false)
	}
	if err != nil {
		return nil, nil, errors.Join(err, root.Close())
	}
	for _, component := range strings.Split(strings.TrimPrefix(name, string(filepath.Separator)), string(filepath.Separator)) {
		next, openErr := openInstallDirectory(root, component, mkdir)
		_ = root.Close()
		if openErr != nil {
			return nil, nil, fmt.Errorf("unsafe installation directory %q: %w", name, openErr)
		}
		root = next
	}
	info, err = root.Stat(".")
	if err == nil {
		err = validateInfo(info, true, true)
	}
	if err != nil {
		return nil, nil, errors.Join(err, root.Close())
	}
	return root, info, nil
}

func openInstallDirectory(parent *os.Root, name string, mkdir mkdirFunc) (*os.Root, error) {
	info, err := parent.Lstat(name)
	created := false
	if errors.Is(err, os.ErrNotExist) {
		err = mkdir(parent, name, directoryMode)
		created = err == nil
		if err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		info, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if err := validateInfo(info, true, created); err != nil {
		return nil, err
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err == nil && !os.SameFile(info, opened) {
		err = ErrChanged
	}
	if err == nil && created {
		// Correct umask only for directories this operation created. Existing
		// private ancestors may hold unrelated data and must keep their modes.
		err = publishableDirectory(root, ".")
		if err == nil {
			err = syncDir(parent, ".")
		}
	}
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return root, nil
}
