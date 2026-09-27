package installrelease

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStagingRequiresProtectedAncestors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writable := filepath.Join(root, "writable")
	require.NoError(t, os.Mkdir(writable, 0o755))
	require.NoError(t, os.Chmod(writable, 0o777))
	_, err := Staging(writable)
	require.Error(t, err)
	child := filepath.Join(writable, "private")
	require.NoError(t, os.Mkdir(child, 0o700))
	_, err = Staging(child)
	require.Error(t, err, "private child does not protect against ancestor rename")
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(root, link))
	_, err = Staging(link)
	require.Error(t, err)
	dir, err := Staging(root)
	require.NoError(t, err)
	require.NoError(t, os.Remove(dir))
}
