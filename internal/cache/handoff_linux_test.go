//go:build linux

package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const handoffMode = 0o700

type handoffFixture struct {
	dir       string
	directory *os.File
	entry     *format.VariantEntry
	payload   []byte
}

func newHandoffFixture(t *testing.T) handoffFixture {
	t.Helper()
	dir := t.TempDir()
	directory, err := os.Open(dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, directory.Close()) })
	payload := []byte("verified descriptor handoff payload")
	sum := sha256.Sum256(payload)
	return handoffFixture{dir: dir, directory: directory, payload: payload, entry: &format.VariantEntry{
		SHA256: hex.EncodeToString(sum[:]), UncompressedSize: int64(len(payload)),
	}}
}

func (f handoffFixture) writePayload(w io.Writer) error {
	_, err := w.Write(f.payload)
	return err
}

func readHandoffFD(t *testing.T, fd int, payload []byte) {
	t.Helper()
	got := make([]byte, len(payload))
	n, err := unix.Pread(fd, got, 0)
	require.NoError(t, err)
	require.Equal(t, len(payload), n)
	require.Equal(t, payload, got)
}

func requireHandoffFDClosed(t *testing.T, fd int) {
	t.Helper()
	var stat unix.Stat_t
	require.ErrorIs(t, unix.Fstat(fd, &stat), unix.EBADF)
}

func TestMaterializeHandoffCanonicalDescriptor(t *testing.T) {
	t.Parallel()
	for _, injected := range []bool{false, true} {
		t.Run(map[bool]string{false: "canonical", true: "explicit opener"}[injected], func(t *testing.T) {
			t.Parallel()
			f := newHandoffFixture(t)
			openedFD, opens := -1, 0
			var opener func(int, string) (int, error)
			if injected {
				opener = func(dirFD int, name string) (int, error) {
					opens++
					var err error
					openedFD, err = OpenFileAtFunc(dirFD, name)
					return openedFD, err
				}
			}
			path, fd, err := MaterializeAndOpenVariantAtFDWithOpener(int(f.directory.Fd()), f.dir, f.entry, f.writePayload, opener)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, CloseFD(fd)) })
			require.Equal(t, filepath.Join(f.dir, f.entry.SHA256), path)
			if injected {
				require.Equal(t, 1, opens, "exactly one full post-publication validation open")
				require.Equal(t, openedFD, fd, "transfer the canonically validated descriptor itself")
			}
			flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
			require.NoError(t, err)
			require.Equal(t, unix.O_RDONLY, flags&unix.O_ACCMODE)
			require.NotZero(t, flags&unix.O_NONBLOCK)
			require.NotZero(t, flags&unix.O_NOFOLLOW)
			descriptorFlags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
			require.NoError(t, err)
			require.NotZero(t, descriptorFlags&unix.FD_CLOEXEC)
			readHandoffFD(t, fd, f.payload)
			require.ErrorIs(t, unix.Ftruncate(fd, 0), unix.EINVAL, "execution descriptor must be read-only")
			require.NoError(t, os.Rename(path, path+".original"))
			require.NoError(t, os.WriteFile(path, []byte("pathname replacement"), handoffMode))
			readHandoffFD(t, fd, f.payload)
		})
	}
}

func TestMaterializeHandoffPathWrapperClosesDescriptor(t *testing.T) {
	f := newHandoffFixture(t)
	originalOpener := OpenFileAtFunc
	t.Cleanup(func() { OpenFileAtFunc = originalOpener })
	openedFD, opens := -1, 0
	OpenFileAtFunc = func(dirFD int, name string) (int, error) {
		opens++
		var err error
		openedFD, err = originalOpener(dirFD, name)
		return openedFD, err
	}
	path, err := MaterializeVariantAtFD(int(f.directory.Fd()), f.dir, f.entry, f.writePayload)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(f.dir, f.entry.SHA256), path)
	require.Equal(t, 1, opens)
	requireHandoffFDClosed(t, openedFD)
}

func TestMaterializeHandoffInvalidInputOwnership(t *testing.T) {
	t.Parallel()
	type arguments struct {
		dirFD  int
		dir    string
		entry  *format.VariantEntry
		writer func(io.Writer) error
	}
	cases := []struct {
		name   string
		mutate func(*arguments)
	}{
		{"invalid descriptor", func(args *arguments) { args.dirFD = -1 }},
		{"empty path", func(args *arguments) { args.dir = "" }},
		{"nil entry", func(args *arguments) { args.entry = nil }},
		{"empty checksum", func(args *arguments) { args.entry.SHA256 = "" }},
		{"invalid checksum", func(args *arguments) { args.entry.SHA256 = "invalid" }},
		{"zero size", func(args *arguments) { args.entry.UncompressedSize = 0 }},
		{"oversized", func(args *arguments) { args.entry.UncompressedSize = format.MaxPayloadSize + 1 }},
		{"nil writer", func(args *arguments) { args.writer = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newHandoffFixture(t)
			called := false
			args := arguments{int(f.directory.Fd()), f.dir, f.entry, func(io.Writer) error { called = true; return nil }}
			tc.mutate(&args)
			path, fd, err := MaterializeAndOpenVariantAtFD(args.dirFD, args.dir, args.entry, args.writer)
			require.Error(t, err)
			require.Empty(t, path)
			require.Equal(t, -1, fd)
			require.False(t, called, "reject invalid metadata before staging any payload")
			entries, err := os.ReadDir(f.dir)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestMaterializeHandoffRejectsForeignOwnedDescriptor(t *testing.T) {
	f := newHandoffFixture(t)
	const foreignPath = "/bin/true"
	var stat unix.Stat_t
	require.NoError(t, unix.Stat(foreignPath, &stat))
	if uint64(stat.Uid) == uint64(os.Geteuid()) {
		t.Skip("a foreign-owned executable is required for this ownership rejection")
	}
	openedFD := -1
	opener := func(int, string) (int, error) {
		var err error
		openedFD, err = OpenFileFunc(foreignPath)
		return openedFD, err
	}
	path, fd, err := MaterializeAndOpenVariantAtFDWithOpener(int(f.directory.Fd()), f.dir, f.entry, f.writePayload, opener)
	require.ErrorIs(t, err, format.ErrCacheWrite)
	require.ErrorIs(t, err, ErrUnsafeFile)
	require.Empty(t, path)
	require.Equal(t, -1, fd)
	requireHandoffFDClosed(t, openedFD)
	require.NoFileExists(t, filepath.Join(f.dir, f.entry.SHA256))
}

func TestMaterializeHandoffReplacementAfterOpen(t *testing.T) {
	t.Parallel()
	f := newHandoffFixture(t)
	path := filepath.Join(f.dir, f.entry.SHA256)
	opener := func(dirFD int, name string) (int, error) {
		fd, err := OpenFileAtFunc(dirFD, name)
		require.NoError(t, err)
		require.NoError(t, os.Rename(path, path+".original"))
		require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte("x"), len(f.payload)), handoffMode))
		return fd, nil
	}
	_, fd, err := MaterializeAndOpenVariantAtFDWithOpener(int(f.directory.Fd()), f.dir, f.entry, f.writePayload, opener)
	require.NoError(t, err, "the opened inode remains the subject of full verification")
	defer CloseFD(fd)
	readHandoffFD(t, fd, f.payload)
}

func TestMaterializeHandoffMutableInodeBoundary(t *testing.T) {
	t.Parallel()
	f := newHandoffFixture(t)
	path, fd, err := MaterializeAndOpenVariantAtFD(int(f.directory.Fd()), f.dir, f.entry, f.writePayload)
	require.NoError(t, err)
	defer CloseFD(fd)
	writer, err := os.OpenFile(path, os.O_WRONLY, 0)
	require.NoError(t, err)
	defer writer.Close()
	changed := bytes.Repeat([]byte("x"), len(f.payload))
	_, err = writer.WriteAt(changed, 0)
	require.NoError(t, err)
	readHandoffFD(t, fd, changed)
	require.False(t, verifyCachedFD(fd, f.entry.SHA256), "a held cache descriptor does not seal a writable inode")
}

func TestMaterializeHandoffPublicationFailures(t *testing.T) {
	for _, stage := range []string{"create", "write", "hash", "chmod", "sync", "close", "rename"} {
		t.Run(stage, func(t *testing.T) {
			f := newHandoffFixture(t)
			path := filepath.Join(f.dir, f.entry.SHA256)
			previous := []byte("retain the prior safe file until publication")
			require.NoError(t, os.WriteFile(path, previous, handoffMode))
			before, err := os.Stat(path)
			require.NoError(t, err)
			failure := errors.New("injected publication failure")
			tmpFD := -1
			ops := materializeFileOps{
				create: func(dirFD int, dirPath string) (int, string, string, error) {
					if stage == "create" {
						return -1, "", "", failure
					}
					fd, name, tempPath, createErr := createTempFileAt(dirFD, dirPath)
					tmpFD = fd
					return fd, name, tempPath, createErr
				},
				chmod: func(file *os.File, mode os.FileMode) error {
					if stage == "chmod" {
						return failure
					}
					return file.Chmod(mode)
				},
				sync: func(file *os.File) error {
					if stage == "sync" {
						return failure
					}
					return file.Sync()
				},
				close: func(file *os.File) error {
					if stage == "close" {
						return failure
					}
					return file.Close()
				},
				rename: func(oldFD int, oldName string, newFD int, newName string) error {
					if stage == "rename" {
						return failure
					}
					return unix.Renameat(oldFD, oldName, newFD, newName)
				},
			}
			writePayload := func(w io.Writer) error {
				if stage == "write" {
					require.NoError(t, f.writePayload(w))
					return failure
				}
				if stage == "hash" {
					_, writeErr := w.Write([]byte("invalid checksum"))
					return writeErr
				}
				return f.writePayload(w)
			}
			gotPath, fd, err := materializeAndOpenVariantAtFD(int(f.directory.Fd()), f.dir, f.entry, writePayload, nil, ops)
			require.Error(t, err)
			require.Empty(t, gotPath)
			require.Equal(t, -1, fd)
			if stage == "hash" {
				require.ErrorIs(t, err, format.ErrPayloadCorrupted)
			} else {
				require.ErrorIs(t, err, failure)
				if stage == "write" {
					require.ErrorIs(t, err, format.ErrCacheExtract)
				} else {
					require.ErrorIs(t, err, format.ErrCacheWrite)
				}
			}
			if tmpFD >= 0 {
				requireHandoffFDClosed(t, tmpFD)
			}
			after, err := os.Stat(path)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after))
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, previous, data)
			entries, err := os.ReadDir(f.dir)
			require.NoError(t, err)
			require.Len(t, entries, 1, "publication failure must remove staging files")
		})
	}
}

func TestMaterializeHandoffPostPublicationRefusal(t *testing.T) {
	states := []string{"open", "fstat", "size", "checksum", "mode", "execute", cacheTestSymlink, cacheTestFIFO, cacheTestSocket, "directory"}
	for _, state := range states {
		t.Run(state, func(t *testing.T) {
			f := newHandoffFixture(t)
			path := filepath.Join(f.dir, f.entry.SHA256)
			openedFD := -1
			opener := func(dirFD int, name string) (int, error) {
				switch state {
				case "open":
					return -1, unix.EACCES
				case "size":
					require.NoError(t, os.WriteFile(path, []byte("short"), handoffMode))
				case "checksum":
					require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte("x"), len(f.payload)), handoffMode))
				case "mode":
					const unsafeMode = 0o720
					require.NoError(t, os.Chmod(path, unsafeMode))
				case "execute":
					require.NoError(t, os.Chmod(path, cacheTestFileMode))
				case cacheTestSymlink, cacheTestFIFO, cacheTestSocket, "directory":
					require.NoError(t, os.Remove(path))
					switch state {
					case cacheTestSymlink:
						require.NoError(t, os.Symlink("/bin/true", path))
					case cacheTestFIFO:
						require.NoError(t, unix.Mkfifo(path, cacheTestFileMode))
					case cacheTestSocket:
						createCacheTestSocket(t, path)
					case "directory":
						require.NoError(t, os.Mkdir(path, handoffMode))
					}
				}
				fd, err := OpenFileAtFunc(dirFD, name)
				openedFD = fd
				if state == "fstat" {
					require.NoError(t, err)
					require.NoError(t, CloseFD(fd))
				}
				return fd, err
			}
			gotPath, fd, err := MaterializeAndOpenVariantAtFDWithOpener(int(f.directory.Fd()), f.dir, f.entry, f.writePayload, opener)
			require.ErrorIs(t, err, format.ErrCacheWrite)
			require.Empty(t, gotPath)
			require.Equal(t, -1, fd)
			if openedFD >= 0 {
				requireHandoffFDClosed(t, openedFD)
			}
			switch state {
			case "open":
				require.ErrorIs(t, err, unix.EACCES)
			case "fstat":
				require.ErrorIs(t, err, unix.EBADF)
			case "size":
				require.ErrorIs(t, err, ErrSizeMismatch)
			case "checksum":
				require.ErrorIs(t, err, ErrChecksumMismatch)
			case "mode":
				require.ErrorIs(t, err, ErrUnsafeFile)
			case "execute":
				require.ErrorIs(t, err, ErrNotExecutable)
			default:
				require.ErrorIs(t, err, ErrNonRegularFile)
			}
			if state == "directory" {
				require.DirExists(t, path, "preserve the existing refusal to unlink a directory target")
			} else {
				require.NoFileExists(t, path, "post-publication verification failure must remove the target")
			}
		})
	}
}

func TestMaterializeHandoffConcurrentPublication(t *testing.T) {
	t.Parallel()
	f := newHandoffFixture(t)
	const publishers = 12
	start := make(chan struct{})
	var group sync.WaitGroup
	for range publishers {
		group.Go(func() {
			<-start
			path, fd, err := MaterializeAndOpenVariantAtFD(int(f.directory.Fd()), f.dir, f.entry, f.writePayload)
			if !assert.NoError(t, err) {
				assert.Equal(t, -1, fd)
				return
			}
			defer CloseFD(fd)
			assert.Equal(t, filepath.Join(f.dir, f.entry.SHA256), path)
			got := make([]byte, len(f.payload))
			n, readErr := unix.Pread(fd, got, 0)
			assert.NoError(t, readErr)
			assert.Equal(t, len(f.payload), n)
			assert.Equal(t, f.payload, got)
		})
	}
	close(start)
	group.Wait()
	entries, err := os.ReadDir(f.dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "concurrent publication must not leave staging files")
}
