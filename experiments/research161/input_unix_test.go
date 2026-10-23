//go:build unix

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestFIFOInputDoesNotWaitForWriter(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fifo")
	require.NoError(t, unix.Mkfifo(path, privateFile))
	done := make(chan error, 1)
	go func() {
		_, err := readBounded(path)
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, inputfile.ErrNotRegular)
	case <-time.After(time.Second):
		// Unblock a regressed ordinary read-only open before failing the test.
		writer, err := os.OpenFile(path, os.O_WRONLY|unix.O_NONBLOCK, 0)
		if err == nil {
			_ = writer.Close()
		}
		t.Fatal("FIFO input waited for a writer")
	}
}
