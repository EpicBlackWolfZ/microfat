//go:build !minimal

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateDeploymentPath(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"same", "symlink", "replacement", "missing", "closed", "nil", "empty"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "deployment")
			require.NoError(t, os.WriteFile(path, []byte("original"), 0o700))
			image, err := os.Open(path)
			require.NoError(t, err)
			t.Cleanup(func() { _ = image.Close() })
			switch state {
			case "symlink":
				require.NoError(t, os.Symlink(path, path+".link"))
				path += ".link"
			case "replacement":
				require.NoError(t, os.WriteFile(path+".next", []byte("newer"), 0o700))
				require.NoError(t, os.Rename(path+".next", path))
			case "missing":
				require.NoError(t, os.Remove(path))
			case "closed":
				require.NoError(t, image.Close())
			case "nil":
				require.ErrorIs(t, validateDeploymentPath(path, nil), errDeploymentChanged)
				return
			case "empty":
				path = ""
			}
			err = validateDeploymentPath(path, image)
			if state == "same" || state == "symlink" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, errDeploymentChanged)
			}
		})
	}
}
