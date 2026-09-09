//go:build linux

package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestCacheSpecialFileDeadline(t *testing.T) {
	const childEnv = "MICROFAT_TEST_FIFO_CHILD"
	if os.Getenv(childEnv) == "1" {
		path := filepath.Join(t.TempDir(), "fifo")
		require.NoError(t, unix.Mkfifo(path, 0o600))
		fd, err := OpenAndValidateFD(path, 0, "", true)
		require.ErrorIs(t, err, ErrNonRegularFile)
		require.Equal(t, -1, fd)
		directory, err := os.Open(filepath.Dir(path))
		require.NoError(t, err)
		defer directory.Close()
		fd, err = OpenAndValidateAtFD(int(directory.Fd()), "fifo", 0, "", true)
		require.ErrorIs(t, err, ErrNonRegularFile)
		require.Equal(t, -1, fd)
		_, err = os.Lstat(path)
		require.NoError(t, err, "special entry must remain")
		return
	}
	const deadline = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCacheSpecialFileDeadline$")
	cmd.Env = append(os.Environ(), childEnv+"=1")
	output, err := cmd.CombinedOutput()
	require.NoError(t, ctx.Err(), "FIFO open blocked")
	require.NoError(t, err, string(output))
}

func TestCacheMetadataAndMutableInode(t *testing.T) {
	t.Parallel()
	const safeMode = 0o700
	for _, mode := range []uint32{safeMode, 0o720, 0o702, 0o4700, 0o2700, 0o1700} {
		stat := unix.Stat_t{Uid: uint32(os.Geteuid()), Mode: mode}
		err := validateCacheMetadata(stat, os.Geteuid())
		if mode == safeMode {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, ErrUnsafeFile)
		}
	}
	require.ErrorIs(t, validateCacheMetadata(unix.Stat_t{Uid: 1, Mode: safeMode}, 0), ErrUnsafeFile)
	path := filepath.Join(t.TempDir(), "entry")
	original := []byte("original")
	require.NoError(t, os.WriteFile(path, original, safeMode))
	sum := sha256.Sum256(original)
	digest := hex.EncodeToString(sum[:])
	fd, err := OpenAndValidateFD(path, int64(len(original)), digest, false)
	require.NoError(t, err)
	defer CloseFD(fd)
	// A pre-existing writable descriptor can modify the same validated inode.
	writer, err := os.OpenFile(path, os.O_WRONLY, 0)
	require.NoError(t, err)
	defer writer.Close()
	_, err = writer.WriteAt([]byte("modified"), 0)
	require.NoError(t, err)
	require.False(t, verifyCachedFD(fd, digest))
	for _, mode := range []os.FileMode{0o720, 0o702, os.ModeSetuid | safeMode} {
		require.NoError(t, os.Chmod(path, mode))
		rejected, openErr := OpenAndValidateFD(path, int64(len(original)), digest, true)
		require.ErrorIs(t, openErr, ErrUnsafeFile)
		require.Equal(t, -1, rejected)
		_, err = os.Stat(path)
		require.NoError(t, err)
	}
}
