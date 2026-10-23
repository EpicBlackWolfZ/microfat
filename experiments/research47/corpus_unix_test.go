//go:build unix

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestBoundedInputRejectsFIFOEmptyAndSparseOversize(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "fifo")
	require.NoError(t, unix.Mkfifo(path, privateFileMode))
	_, err := readBounded(path, maxInput)
	require.Error(t, err)
	path = filepath.Join(root, "empty")
	require.NoError(t, os.WriteFile(path, nil, privateFileMode))
	_, err = readBounded(path, maxInput)
	require.Error(t, err)
	path = filepath.Join(root, "oversize")
	f, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(maxInput+1))
	require.NoError(t, f.Close())
	_, err = readBounded(path, maxInput)
	require.Error(t, err)
}
