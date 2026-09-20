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
