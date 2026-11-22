package inputfile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegularInput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "input")
	_, err := Open(path)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = Open(dir)
	require.ErrorIs(t, err, ErrNotRegular)
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	_, err = Open(path)
	require.ErrorIs(t, err, ErrNotRegular)
	require.NoError(t, os.WriteFile(path, []byte("payload"), 0o600))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(path, link))
	f, err := Open(link)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	_, err = openRegular(path, func(path string) (*os.File, error) {
		closed, openErr := os.Open(path)
		require.NoError(t, openErr)
		require.NoError(t, closed.Close())
		return closed, nil
	})
	require.ErrorIs(t, err, os.ErrClosed)
}

func TestOpenRegularNoFollow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	missing := filepath.Join(dir, "missing")
	_, _, err := OpenRegularNoFollow(missing)
	require.ErrorIs(t, err, os.ErrNotExist)

	_, _, err = OpenRegularNoFollow(dir)
	require.ErrorIs(t, err, ErrNotRegular)

	empty := filepath.Join(dir, "empty")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	f, info, err := OpenRegularNoFollow(empty)
	require.NoError(t, err)
	require.Equal(t, int64(0), info.Size())
	require.NoError(t, f.Close())

	path := filepath.Join(dir, "input")
	require.NoError(t, os.WriteFile(path, []byte("payload"), 0o600))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(path, link))
	_, _, err = OpenRegularNoFollow(link)
	require.ErrorIs(t, err, ErrSymlink)

	f, info, err = OpenRegularNoFollow(path)
	require.NoError(t, err)
	require.Equal(t, int64(len("payload")), info.Size())
	require.NoError(t, f.Close())

	replacement := filepath.Join(dir, "replacement")
	require.NoError(t, os.WriteFile(replacement, []byte("other"), 0o600))
	_, _, err = openRegularNoFollow(path, func(string) (*os.File, error) {
		return os.Open(replacement)
	})
	require.ErrorIs(t, err, ErrNotRegular)
}
