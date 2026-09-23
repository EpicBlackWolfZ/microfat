//go:build linux

package lifecycle_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/lifecycle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

type dummyFileInfo struct {
	name string
}

func (d dummyFileInfo) Name() string       { return d.name }
func (d dummyFileInfo) Size() int64        { return 100 }
func (d dummyFileInfo) Mode() os.FileMode  { return 0o755 }
func (d dummyFileInfo) ModTime() time.Time { return time.Now() }
func (d dummyFileInfo) IsDir() bool        { return false }
func (d dummyFileInfo) Sys() any           { return nil }

func TestFileStatMetadata_NonUnixFallback(t *testing.T) {
	dev, ino, nlink, uid, gid, ok := lifecycle.FileStatMetadataForTest(dummyFileInfo{name: "test"})
	assert.False(t, ok)
	assert.Equal(t, uint64(0), dev)
	assert.Equal(t, uint64(0), ino)
	assert.Equal(t, uint64(1), nlink)
	assert.Equal(t, os.Geteuid(), uid)
	assert.Equal(t, os.Getegid(), gid)
}

func TestPublishCreateOnly_LinuxFallbackAndErrors(t *testing.T) {
	tmpDir := t.TempDir()
	staged := filepath.Join(tmpDir, "staged")
	dest := filepath.Join(tmpDir, "dest")

	require.NoError(t, os.WriteFile(staged, []byte("content"), 0o600))

	// 1. renameat2 returns ENOSYS -> falls back to rename when dest does not exist
	restore := lifecycle.SetRenameat2FuncForTest(func(int, string, int, string, uint) error {
		return unix.ENOSYS
	})
	defer restore()

	err := lifecycle.PublishCreateOnlyForTest(staged, dest)
	require.NoError(t, err)
	require.FileExists(t, dest)

	// 2. renameat2 returns ENOSYS -> fails with ErrDestinationExists when dest exists
	staged2 := filepath.Join(tmpDir, "staged2")
	require.NoError(t, os.WriteFile(staged2, []byte("content2"), 0o600))

	err = lifecycle.PublishCreateOnlyForTest(staged2, dest)
	require.Error(t, err)
	require.ErrorIs(t, err, lifecycle.ErrDestinationExists)

	// 3. renameat2 returns generic error (e.g. EPERM)
	restore()
	restore2 := lifecycle.SetRenameat2FuncForTest(func(int, string, int, string, uint) error {
		return unix.EPERM
	})
	defer restore2()

	// 4. renameat2 returns ENOSYS -> fallback rename fails
	restore2()
	restoreENOSYS := lifecycle.SetRenameat2FuncForTest(func(int, string, int, string, uint) error {
		return unix.ENOSYS
	})
	defer restoreENOSYS()

	restoreRenameErr := lifecycle.SetRenameFuncForTest(func(string, string) error {
		return errors.New("simulated fallback rename error")
	})
	defer restoreRenameErr()

	err = lifecycle.PublishCreateOnlyForTest(staged2, filepath.Join(tmpDir, "dest4"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fallback rename to")
}

func TestReadXattrs_LinuxEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "target")
	require.NoError(t, os.WriteFile(target, []byte("target"), 0o755))

	// 1. listxattr returns ENOTSUP -> returns nil, nil
	restore := lifecycle.SetListxattrFuncForTest(func(string, []byte) (int, error) {
		return 0, unix.ENOTSUP
	})
	defer restore()

	xattrs, err := lifecycle.ReadXattrsForTest(target)
	require.NoError(t, err)
	assert.Nil(t, xattrs)

	// 2. listxattr returns EIO -> returns error
	restore()
	restore2 := lifecycle.SetListxattrFuncForTest(func(string, []byte) (int, error) {
		return 0, unix.EIO
	})
	defer restore2()

	_, err = lifecycle.ReadXattrsForTest(target)
	require.Error(t, err)

	// 3. listxattr returns attributes, but second listxattr fails with error
	restore2()
	calls := 0
	restore3 := lifecycle.SetListxattrFuncForTest(func(_ string, dest []byte) (int, error) {
		calls++
		if calls == 1 {
			return 10, nil
		}
		return 0, unix.EIO
	})
	defer restore3()

	_, err = lifecycle.ReadXattrsForTest(target)
	require.Error(t, err)

	// 4. listxattr succeeds, second listxattr returns ENOTSUP -> returns nil, nil
	restore3()
	restoreNotSup := lifecycle.SetListxattrFuncForTest(func(_ string, dest []byte) (int, error) {
		if dest == nil {
			return 10, nil
		}
		return 0, unix.ENOTSUP
	})
	defer restoreNotSup()

	xattrs, err = lifecycle.ReadXattrsForTest(target)
	require.NoError(t, err)
	assert.Nil(t, xattrs)

	// 5. listxattr succeeds, getxattr returns ENODATA -> skips attribute
	restoreNotSup()
	restore4 := lifecycle.SetListxattrFuncForTest(func(_ string, dest []byte) (int, error) {
		attrName := []byte("user.test\x00")
		if dest == nil {
			return len(attrName), nil
		}
		copy(dest, attrName)
		return len(attrName), nil
	})
	defer restore4()

	restoreGetxattr := lifecycle.SetGetxattrFuncForTest(func(string, string, []byte) (int, error) {
		return 0, unix.ENODATA
	})
	defer restoreGetxattr()

	xattrs, err = lifecycle.ReadXattrsForTest(target)
	require.NoError(t, err)
	assert.Empty(t, xattrs)

	// 6. listxattr succeeds, getxattr returns EIO -> returns error
	restoreGetxattr()
	restoreGetxattr2 := lifecycle.SetGetxattrFuncForTest(func(string, string, []byte) (int, error) {
		return 0, unix.EIO
	})
	defer restoreGetxattr2()

	_, err = lifecycle.ReadXattrsForTest(target)
	require.Error(t, err)

	// 7. listxattr succeeds, second getxattr returns EIO -> returns error
	restoreGetxattr2()
	getCalls := 0
	restoreGetxattr3 := lifecycle.SetGetxattrFuncForTest(func(_ string, _ string, dest []byte) (int, error) {
		getCalls++
		if getCalls == 1 {
			return 5, nil
		}
		return 0, unix.EIO
	})
	defer restoreGetxattr3()

	_, err = lifecycle.ReadXattrsForTest(target)
	require.Error(t, err)

	// 8. listxattr succeeds, second getxattr returns ENOTSUP -> continues and returns empty
	restoreGetxattr3()
	restoreGetxattrNotSup := lifecycle.SetGetxattrFuncForTest(func(_ string, _ string, dest []byte) (int, error) {
		if dest == nil {
			return 5, nil
		}
		return 0, unix.ENOTSUP
	})
	defer restoreGetxattrNotSup()

	xattrs, err = lifecycle.ReadXattrsForTest(target)
	require.NoError(t, err)
	assert.Empty(t, xattrs)
}

func TestAcquireAdvisoryLock_LinuxErrors(t *testing.T) {
	// 1. Non-existent file
	_, err := lifecycle.AcquireAdvisoryLockForTest("/non/existent/microfat/path")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "opening")

	// 2. flock returns non-EAGAIN error
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "lock_test")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o600))

	restore := lifecycle.SetFlockFuncForTest(func(int, int) error {
		return unix.EBADF
	})
	defer restore()

	_, err = lifecycle.AcquireAdvisoryLockForTest(target)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "locking")
}

func TestSyncDirectory_Error(t *testing.T) {
	err := lifecycle.SyncDirectoryForTest("/non/existent/microfat/dir")
	require.Error(t, err)
}
