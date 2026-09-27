package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureOwnerUID = "owner-uid"

func TestDiscoveryRetainsPhysicalGeneration(t *testing.T) {
	t.Parallel()
	paths := pathsFor(t)
	applyFixture(t, paths, "0.3.0")
	old, err := filepath.EvalSymlinks(filepath.Join(paths.Bin, "microfat"))
	require.NoError(t, err)
	applyFixture(t, paths, "0.3.1")
	managed, err := ValidateDiscovery(old, old)
	require.NoError(t, err)
	assert.True(t, managed)
	current, err := filepath.EvalSymlinks(filepath.Join(paths.Bin, "microfat"))
	require.NoError(t, err)
	assert.NotEqual(t, old, current)
	for _, hint := range []string{filepath.Join(paths.Bin, "microfat"), filepath.Join(paths.Store, "current", "microfat")} {
		managed, err = ValidateDiscovery(hint, current)
		require.ErrorIs(t, err, ErrDiscovery)
		assert.True(t, managed)
	}
	managed, err = ValidateDiscovery("/manual/bin/microfat", "/manual/bin/microfat")
	require.NoError(t, err)
	assert.False(t, managed)
	assert.False(t, IsGenerationPath("/manual/generations/project/microfat"), "ordinary directories are not managed layouts")
}

func TestDiscoveryRejectsDamagedGeneration(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"missing", "owner", fixtureOwnerUID, "owner-roots", "owner-mode", "owner-link", "generation-mode",
		"generation-link", "metadata", "file", "file-mode", "sibling", "layout", "identity", "generations-mode"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			paths := pathsFor(t)
			applyFixture(t, paths, "0.3.0")
			physical, err := filepath.EvalSymlinks(filepath.Join(paths.Bin, "microfat"))
			require.NoError(t, err)
			dir := filepath.Dir(physical)
			switch scenario {
			case "missing":
				require.NoError(t, os.RemoveAll(dir))
			case "owner":
				require.NoError(t, os.Remove(filepath.Join(paths.Store, ownerFile)))
			case fixtureOwnerUID, "owner-roots", "identity":
				snapshot, err := Inspect(paths)
				require.NoError(t, err)
				owner := snapshot.Owner()
				if scenario == fixtureOwnerUID {
					owner.UID++
				}
				if scenario == "owner-roots" {
					owner.Bin = "relative"
				}
				if scenario == "identity" {
					owner.Kind = "another-installer"
				}
				rewriteJSON(t, filepath.Join(paths.Store, ownerFile), owner)
			case "owner-mode":
				require.NoError(t, os.Chmod(filepath.Join(paths.Store, ownerFile), 0o666))
			case "owner-link":
				name := filepath.Join(paths.Store, ownerFile)
				require.NoError(t, os.Rename(name, name+"-old"))
				require.NoError(t, os.Symlink(name+"-old", name))
			case "generation-mode":
				require.NoError(t, os.Chmod(dir, 0o777))
			case "generations-mode":
				require.NoError(t, os.Chmod(filepath.Dir(dir), 0o777))
			case "generation-link":
				require.NoError(t, os.Rename(dir, dir+"-old"))
				require.NoError(t, os.Symlink(dir+"-old", dir))
			case "metadata":
				require.NoError(t, os.WriteFile(filepath.Join(dir, generationFile), []byte("{}"), 0o644))
			case "file":
				require.NoError(t, os.WriteFile(physical, []byte("tampered"), 0o755))
			case "file-mode":
				require.NoError(t, os.Chmod(physical, 0o777))
			case "sibling":
				require.NoError(t, os.Remove(filepath.Join(dir, "microfat-stub-minimal")))
			case "layout":
				physical = filepath.Join(paths.Bin, "unexpected")
				require.NoError(t, os.WriteFile(filepath.Join(paths.Bin, generationFile), []byte("{}"), 0o600))
			}
			managed, err := ValidateDiscovery(physical, physical)
			require.ErrorIs(t, err, ErrDiscovery)
			assert.True(t, managed)
		})
	}
}
