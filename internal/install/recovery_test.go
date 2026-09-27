package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestRecoverInterruptedActivationLink(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"owned-link", "regular-file", "escaping-link"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			paths := pathsFor(t)
			applyFixture(t, paths, "0.3.0")
			old := readActive(t, paths)
			name := filepath.Join(paths.Store, ".current-"+NewID())
			switch scenario {
			case "owned-link":
				require.NoError(t, os.Symlink(filepath.Join(generationDir, old.ID), name))
			case "regular-file":
				require.NoError(t, os.WriteFile(name, []byte("unrelated"), metadataMode))
			case "escaping-link":
				require.NoError(t, os.Symlink("/unrelated", name))
			}
			snapshot, err := Inspect(paths)
			require.NoError(t, err)
			generation, source := fixture(t, "0.3.1")
			_, err = Apply(t.Context(), snapshot, generation, source, ApplyOptions{})
			if scenario == "owned-link" {
				require.NoError(t, err)
				_, err = os.Lstat(name)
				require.ErrorIs(t, err, os.ErrNotExist)
			} else {
				require.ErrorIs(t, err, ErrOwnership)
				assert.Equal(t, old.ID, readActive(t, paths).ID)
				_, err = os.Lstat(name)
				require.NoError(t, err, "unrecognized entries are preserved")
			}
		})
	}
}

func TestInjectedNoSpaceRetainsSelection(t *testing.T) {
	t.Parallel()
	paths := pathsFor(t)
	applyFixture(t, paths, "0.3.0")
	old := readActive(t, paths)
	snapshot, err := Inspect(paths)
	require.NoError(t, err)
	generation, source := fixture(t, "0.3.1")
	result, err := apply(t.Context(), snapshot, generation, source, ApplyOptions{}, func(stage string) error {
		if stage == "staged-microfat" {
			return unix.ENOSPC
		}
		return nil
	})
	require.ErrorIs(t, err, unix.ENOSPC)
	assert.False(t, result.Activated)
	assert.Equal(t, old.ID, readActive(t, paths).ID)
}
