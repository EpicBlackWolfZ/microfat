//go:build linux

package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const observationExecMode = 0o700

func observationFixture() (*format.VariantEntry, []byte) {
	payload := []byte("read-only observed payload")
	digest := sha256.Sum256(payload)
	return &format.VariantEntry{
		Level:            "v1",
		SHA256:           hex.EncodeToString(digest[:]),
		UncompressedSize: int64(len(payload)),
	}, payload
}

func TestObserveVariantEntryStates(t *testing.T) {
	t.Parallel()
	entry, payload := observationFixture()
	cases := []struct {
		name    string
		status  ObservationStatus
		prepare func(*testing.T, string)
	}{
		{name: "missing", status: ObservationMissing},
		{name: "valid", status: ObservationValid, prepare: observationFile(payload, observationExecMode)},
		{name: "size mismatch", status: ObservationCorruptSafe, prepare: observationFile([]byte("short"), observationExecMode)},
		{name: "checksum mismatch", status: ObservationCorruptSafe, prepare: observationFile(make([]byte, len(payload)), observationExecMode)},
		{name: "not executable", status: ObservationCorruptSafe, prepare: observationFile(payload, cacheTestFileMode)},
		{name: "group writable", status: ObservationUnsafe, prepare: observationFile(payload, 0o720)},
		{name: "world writable", status: ObservationUnsafe, prepare: observationFile(payload, 0o702)},
		{name: "setuid", status: ObservationUnsafe, prepare: observationFile(payload, os.ModeSetuid|observationExecMode)},
		{name: "nonregular directory", status: ObservationUnsafe, prepare: func(t *testing.T, path string) {
			require.NoError(t, os.Mkdir(path, observationExecMode))
		}},
		{name: "symlink", status: ObservationUnsafe, prepare: func(t *testing.T, path string) {
			require.NoError(t, os.Symlink("/bin/true", path))
		}},
		{name: "fifo", status: ObservationUnsafe, prepare: func(t *testing.T, path string) {
			require.NoError(t, unix.Mkfifo(path, cacheTestFileMode))
		}},
		{name: "socket", status: ObservationUnsafe, prepare: createCacheTestSocket},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, entry.SHA256)
			if tc.prepare != nil {
				tc.prepare(t, path)
			}
			before := snapshotObservedCache(t, dir)
			res := ObserveVariant(entry, dir)
			require.Equal(t, tc.status, res.Status)
			require.Equal(t, path, res.CachedPath)
			if tc.status == ObservationValid {
				require.Empty(t, res.Error)
			} else {
				require.NotEmpty(t, res.Error)
			}
			require.Equal(t, before, snapshotObservedCache(t, dir), "observation must preserve entries, modes and bytes")
		})
	}
}

func observationFile(payload []byte, mode os.FileMode) func(*testing.T, string) {
	return func(t *testing.T, path string) {
		t.Helper()
		require.NoError(t, os.WriteFile(path, payload, observationExecMode))
		require.NoError(t, os.Chmod(path, mode))
	}
}

type observedCacheSnapshot struct {
	Mode  os.FileMode
	Files map[string]observedCacheFile
}

type observedCacheFile struct {
	Mode os.FileMode
	Data string
}

func snapshotObservedCache(t *testing.T, dir string) observedCacheSnapshot {
	t.Helper()
	info, err := os.Lstat(dir)
	require.NoError(t, err)
	res := observedCacheSnapshot{Mode: info.Mode(), Files: make(map[string]observedCacheFile)}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		info, err = os.Lstat(path)
		require.NoError(t, err)
		file := observedCacheFile{Mode: info.Mode()}
		if info.Mode().IsRegular() {
			data, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			file.Data = string(data)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			file.Data, err = os.Readlink(path)
			require.NoError(t, err)
		}
		res.Files[entry.Name()] = file
	}
	return res
}

func TestObserveVariantDirectoryStates(t *testing.T) {
	t.Parallel()
	entry, payload := observationFixture()
	t.Run("missing ancestors", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		dir := filepath.Join(root, "absent", "cache")
		res := ObserveVariant(entry, dir)
		require.Equal(t, ObservationMissing, res.Status)
		require.Equal(t, filepath.Join(dir, entry.SHA256), res.CachedPath)
		require.NoDirExists(t, filepath.Dir(dir))
	})
	t.Run("insecure permissions", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		observationFile(payload, observationExecMode)(t, filepath.Join(dir, entry.SHA256))
		const insecureMode = 0o777
		require.NoError(t, os.Chmod(dir, insecureMode))
		before := snapshotObservedCache(t, dir)
		res := ObserveVariant(entry, dir)
		require.Equal(t, ObservationUnsafe, res.Status)
		require.Equal(t, before, snapshotObservedCache(t, dir))
	})
	t.Run("symlink with trailing separator", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		observationFile(payload, observationExecMode)(t, filepath.Join(dir, entry.SHA256))
		before := snapshotObservedCache(t, dir)
		link := filepath.Join(t.TempDir(), "link")
		require.NoError(t, os.Symlink(dir, link))
		res := ObserveVariant(entry, link+string(os.PathSeparator))
		require.Equal(t, ObservationUnsafe, res.Status)
		require.Equal(t, filepath.Join(link, entry.SHA256), res.CachedPath)
		target, err := os.Readlink(link)
		require.NoError(t, err)
		require.Equal(t, dir, target)
		require.Equal(t, before, snapshotObservedCache(t, dir))
	})
	t.Run("non-directory", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "file")
		observationFile(payload, observationExecMode)(t, path)
		res := ObserveVariant(entry, path)
		require.Equal(t, ObservationUnsafe, res.Status)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, payload, data)
	})
}

func TestObserveVariantDiscovery(t *testing.T) {
	entry, payload := observationFixture()
	t.Run("environment missing", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")
		t.Setenv(format.EnvCacheDir, dir)
		res := ObserveVariant(entry, "")
		require.Equal(t, ObservationMissing, res.Status)
		require.Equal(t, filepath.Join(dir, entry.SHA256), res.CachedPath)
		require.NoDirExists(t, dir)
	})
	t.Run("automatic unavailable", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv(format.EnvCacheDir, "")
		t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "xdg"))
		t.Setenv("TMPDIR", filepath.Join(root, "tmp"))
		res := ObserveVariant(entry, "")
		require.Equal(t, ObservationUnavailable, res.Status)
		require.Empty(t, res.CachedPath)
		require.NotEmpty(t, res.Error)
		children, err := os.ReadDir(root)
		require.NoError(t, err)
		require.Empty(t, children, "discovery must not create any candidate")
	})
	t.Run("automatic skips insecure without repair", func(t *testing.T) {
		root := t.TempDir()
		primary := filepath.Join(root, "xdg", "microfat")
		fallback := filepath.Join(root, fmt.Sprintf(".microfat-%d", os.Geteuid()))
		require.NoError(t, os.MkdirAll(primary, observationExecMode))
		require.NoError(t, os.Mkdir(fallback, observationExecMode))
		const insecureMode = 0o777
		require.NoError(t, os.Chmod(primary, insecureMode))
		observationFile(payload, observationExecMode)(t, filepath.Join(fallback, entry.SHA256))
		t.Setenv(format.EnvCacheDir, "")
		t.Setenv("XDG_CACHE_HOME", filepath.Dir(primary))
		t.Setenv("TMPDIR", root)
		beforePrimary := snapshotObservedCache(t, primary)
		beforeFallback := snapshotObservedCache(t, fallback)
		res := ObserveVariant(entry, "")
		require.Equal(t, ObservationValid, res.Status)
		require.Equal(t, filepath.Join(fallback, entry.SHA256), res.CachedPath)
		require.Equal(t, beforePrimary, snapshotObservedCache(t, primary))
		require.Equal(t, beforeFallback, snapshotObservedCache(t, fallback))
	})
}

func TestObserveVariantDescriptorReplacement(t *testing.T) {
	t.Parallel()
	entry, payload := observationFixture()
	for _, originalValid := range []bool{false, true} {
		t.Run(fmt.Sprintf("opened valid=%t", originalValid), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, entry.SHA256)
			other := make([]byte, len(payload))
			original, replacement := other, payload
			observedStatus, laterStatus := ObservationCorruptSafe, ObservationValid
			if originalValid {
				original, replacement = payload, other
				observedStatus, laterStatus = laterStatus, observedStatus
			}
			observationFile(original, observationExecMode)(t, path)
			replacementPath := filepath.Join(dir, "replacement")
			observationFile(replacement, observationExecMode)(t, replacementPath)
			res := observeVariantWithOpener(entry, dir, func(dirFD int, name string) (int, error) {
				fd, err := OpenFileAtFunc(dirFD, name)
				require.NoError(t, err)
				require.NoError(t, os.Rename(replacementPath, path))
				return fd, nil
			})
			require.Equal(t, observedStatus, res.Status, "hash and size checks must inspect the opened inode")
			require.Equal(t, laterStatus, ObserveVariant(entry, dir).Status, "later observations must revalidate stale results")
			current, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, replacement, current, "observation must not remove a replacement")
		})
	}
}

func TestObserveVariantUnavailableAndInvalid(t *testing.T) {
	t.Parallel()
	entry, _ := observationFixture()
	dir := t.TempDir()
	res := observeVariantWithOpener(entry, dir, func(int, string) (int, error) {
		return -1, unix.EACCES
	})
	// Uninspectable metadata is unsafe under the canonical opener's fail-closed contract.
	require.Equal(t, ObservationUnsafe, res.Status)
	observationFile([]byte("payload"), observationExecMode)(t, filepath.Join(dir, entry.SHA256))
	res = observeVariantWithOpener(entry, dir, func(int, string) (int, error) { return -1, unix.EACCES })
	require.Equal(t, ObservationUnavailable, res.Status)
	require.NotEmpty(t, res.Error)
	for _, invalid := range []*format.VariantEntry{
		nil,
		{},
		{SHA256: "../../escape"},
		{SHA256: entry.SHA256, UncompressedSize: -1},
	} {
		res = ObserveVariant(invalid, dir)
		require.Equal(t, ObservationUnavailable, res.Status)
		require.Empty(t, res.CachedPath)
		require.NotEmpty(t, res.Error)
	}
}

func TestClassifyObservationError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err    error
		status ObservationStatus
	}{
		{err: unix.ENOENT, status: ObservationMissing},
		{err: unix.ELOOP, status: ObservationUnsafe},
		{err: ErrNonRegularFile, status: ObservationUnsafe},
		{err: ErrUnsafeFile, status: ObservationUnsafe},
		{err: ErrNotExecutable, status: ObservationCorruptSafe},
		{err: ErrSizeMismatch, status: ObservationCorruptSafe},
		{err: ErrChecksumMismatch, status: ObservationCorruptSafe},
		{err: format.ErrInsecureCacheDir, status: ObservationUnsafe},
		{err: errors.Join(format.ErrInsecureCacheDir, unix.EACCES), status: ObservationUnavailable},
		{err: errors.Join(format.ErrInsecureCacheDir, unix.EMFILE), status: ObservationUnavailable},
		{err: ErrUnsupportedPlatform, status: ObservationUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.status, classifyObservationError(tc.err))
		})
	}
}
