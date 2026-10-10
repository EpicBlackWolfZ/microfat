//go:build linux

package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	cacheTestFileMode = 0o600
	cacheTestFIFO     = "fifo"
	cacheTestUnsafe   = "unsafe"
)

func TestCacheSpecialFileDeadline(t *testing.T) {
	const childEnv = "MICROFAT_TEST_FIFO_CHILD"
	if os.Getenv(childEnv) == "1" {
		path := filepath.Join(t.TempDir(), cacheTestFIFO)
		require.NoError(t, unix.Mkfifo(path, 0o600))
		fd, err := OpenAndValidateFD(path, 0, "", true)
		require.ErrorIs(t, err, ErrNonRegularFile)
		require.Equal(t, -1, fd)
		directory, err := os.Open(filepath.Dir(path))
		require.NoError(t, err)
		defer directory.Close()
		fd, err = OpenAndValidateAtFD(int(directory.Fd()), cacheTestFIFO, 0, "", true)
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

func createCacheTestSocket(t *testing.T, path string) {
	t.Helper()
	dirFD, err := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	require.NoError(t, err)
	defer CloseFD(dirFD)
	socketFD, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	require.NoError(t, err)
	defer CloseFD(socketFD)
	// Descriptor-relative naming fits sockaddr_un even for a SHA-256 cache name.
	name := "/proc/self/fd/" + strconv.Itoa(dirFD) + "/" + filepath.Base(path)
	require.NoError(t, unix.Bind(socketFD, &unix.SockaddrUnix{Name: name}))
}

func TestCacheSocketRejectedBeforeDescriptorValidation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "socket")
	createCacheTestSocket(t, path)
	directory, err := os.Open(dir)
	require.NoError(t, err)
	defer directory.Close()
	for _, remove := range []bool{false, true} {
		fd, openErr := OpenAndValidateFD(path, 0, "", remove)
		require.Equal(t, -1, fd)
		require.ErrorIs(t, openErr, ErrNonRegularFile)
		require.ErrorIs(t, openErr, unix.ENXIO, "preserve the socket open failure")
		fd, openErr = OpenAndValidateAtFD(int(directory.Fd()), filepath.Base(path), 0, "", remove)
		require.Equal(t, -1, fd)
		require.ErrorIs(t, openErr, ErrNonRegularFile)
		require.ErrorIs(t, openErr, unix.ENXIO)
		stat, statErr := os.Lstat(path)
		require.NoError(t, statErr)
		require.NotZero(t, stat.Mode()&os.ModeSocket, "socket must not be removed or replaced")
	}
}

func TestFailedCacheOpenMetadataOnlyRejects(t *testing.T) {
	t.Parallel()
	for _, route := range []string{"path", "at"} {
		for _, state := range []string{cacheTestFIFO, cacheTestUnsafe, "safe", "uninspectable", "transient-miss"} {
			for _, failure := range []error{unix.ENXIO, unix.ENODEV, unix.EACCES} {
				t.Run(route+"/"+state+"/"+failure.Error(), func(t *testing.T) {
					t.Parallel()
					dir := t.TempDir()
					path := filepath.Join(dir, "entry")
					if state == cacheTestFIFO || state == "transient-miss" {
						require.NoError(t, unix.Mkfifo(path, uint32(cacheTestFileMode)))
					} else if state != "uninspectable" {
						require.NoError(t, os.WriteFile(path, []byte("payload"), 0o700))
						if state == cacheTestUnsafe {
							const unsafeMode = 0o720
							require.NoError(t, os.Chmod(path, unsafeMode))
						}
					}
					if state == "transient-miss" {
						failure = unix.ENOENT
					}
					var fd int
					var err error
					if route == "path" {
						fd, err = OpenAndValidateFDWithOpener(path, 0, "", true,
							func(string) (int, error) { return -1, failure })
					} else {
						directory, openErr := os.Open(dir)
						require.NoError(t, openErr)
						defer directory.Close()
						fd, err = OpenAndValidateAtFDWithOpener(int(directory.Fd()), filepath.Base(path), 0, "", true,
							func(int, string) (int, error) { return -1, failure })
					}
					require.Equal(t, -1, fd, "metadata inspection can never authorize a descriptor")
					require.ErrorIs(t, err, failure)
					switch state {
					case cacheTestFIFO:
						require.ErrorIs(t, err, ErrNonRegularFile)
					case cacheTestUnsafe, "uninspectable":
						require.ErrorIs(t, err, ErrUnsafeFile)
					case "safe", "transient-miss":
						require.NotErrorIs(t, err, ErrUnsafeFile)
						require.NotErrorIs(t, err, ErrNonRegularFile)
					}
					if state != "uninspectable" {
						_, statErr := os.Lstat(path)
						require.NoError(t, statErr, "failed open must not modify the entry")
					}
				})
			}
		}
	}
}

func TestMaterializeRefusesUnsafeReplacementTargets(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"socket", cacheTestFIFO, "symlink", cacheTestUnsafe} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			payload := []byte("verified payload")
			sum := sha256.Sum256(payload)
			entry := &format.VariantEntry{SHA256: hex.EncodeToString(sum[:]), UncompressedSize: int64(len(payload))}
			path := filepath.Join(dir, entry.SHA256)
			switch state {
			case "socket":
				createCacheTestSocket(t, path)
			case cacheTestFIFO:
				require.NoError(t, unix.Mkfifo(path, uint32(cacheTestFileMode)))
			case "symlink":
				require.NoError(t, os.Symlink("/bin/true", path))
			case cacheTestUnsafe:
				const unsafeMode = 0o720
				require.NoError(t, os.WriteFile(path, payload, unsafeMode))
				require.NoError(t, os.Chmod(path, unsafeMode))
			}
			before, err := os.Lstat(path)
			require.NoError(t, err)
			directory, err := os.Open(dir)
			require.NoError(t, err)
			defer directory.Close()
			called := false
			result, err := MaterializeVariantAtFD(int(directory.Fd()), dir, entry, func(w io.Writer) error {
				called = true
				_, writeErr := w.Write(payload)
				return writeErr
			})
			require.Empty(t, result)
			require.ErrorIs(t, err, format.ErrCacheWrite)
			if state == cacheTestUnsafe {
				require.ErrorIs(t, err, ErrUnsafeFile)
			} else {
				require.ErrorIs(t, err, ErrNonRegularFile)
			}
			require.False(t, called, "unsafe replacement must be rejected before extraction")
			after, err := os.Lstat(path)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after))
			require.Equal(t, before.Mode(), after.Mode())
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Len(t, entries, 1, "refusal must not leave staging files")
		})
	}
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
