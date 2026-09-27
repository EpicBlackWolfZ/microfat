//go:build linux

package install

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureBeforeActivation = "before-activation"

func installedCLI(t *testing.T, paths Paths) string {
	t.Helper()
	physical, err := filepath.EvalSymlinks(filepath.Join(paths.Bin, "microfat"))
	require.NoError(t, err)
	return physical
}

func TestUpdateObservationAndActivation(t *testing.T) {
	t.Parallel()
	paths := pathsFor(t)
	first := applyFixture(t, paths, "0.3.0")
	physical := installedCLI(t, paths)
	before, err := os.Stat(filepath.Join(paths.Store, lockFile))
	require.NoError(t, err)
	observed, err := ReadInstallation(physical)
	require.NoError(t, err)
	assert.Equal(t, first.Generation, observed.Current)
	assert.Equal(t, observed.Current, observed.Running)
	snapshot, err := InspectUpdate(physical)
	require.NoError(t, err)
	assert.True(t, snapshot.updateOnly)
	generation, source := fixture(t, "0.3.1")
	result, err := Apply(t.Context(), snapshot, generation, source, ApplyOptions{})
	require.NoError(t, err)
	assert.True(t, result.Activated)
	observed, err = ReadInstallation(physical)
	require.NoError(t, err)
	assert.Equal(t, first.Generation.ID, observed.Running.ID)
	assert.Equal(t, result.Generation.ID, observed.Current.ID)
	_, err = InspectUpdate(physical)
	require.ErrorIs(t, err, ErrChanged)
	after, err := os.Stat(filepath.Join(paths.Store, lockFile))
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after))
	assert.FileExists(t, physical)
}

func TestUpdateRequiresUnchangedExistingInstallation(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"store-removed", "bin-removed", "link-removed", "link-modified", "current-changed", "metadata-changed",
		"owner-changed", "product-corrupt", "identical-replacement", fixtureBeforeActivation, "activated"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			paths := pathsFor(t)
			first := applyFixture(t, paths, "0.3.0")
			physical := installedCLI(t, paths)
			snapshot, err := InspectUpdate(physical)
			require.NoError(t, err)
			generation, source := fixture(t, "0.3.1")
			var hook func(string) error
			switch scenario {
			case "store-removed":
				require.NoError(t, os.RemoveAll(paths.Store))
			case "bin-removed":
				require.NoError(t, os.RemoveAll(paths.Bin))
			case "link-removed":
				require.NoError(t, os.Remove(filepath.Join(paths.Bin, "microfat-stub")))
			case "link-modified":
				require.NoError(t, os.Remove(filepath.Join(paths.Bin, "microfat-stub")))
				require.NoError(t, os.WriteFile(filepath.Join(paths.Bin, "microfat-stub"), []byte("unrelated"), 0o755))
			case "current-changed":
				applyFixture(t, paths, "0.3.2")
			case "metadata-changed":
				g := first.Generation
				g.Version = "0.3.2"
				rewriteJSON(t, filepath.Join(filepath.Dir(physical), generationFile), g)
			case "owner-changed":
				owner := snapshot.Owner()
				owner.ID = NewID()
				rewriteJSON(t, filepath.Join(paths.Store, ownerFile), owner)
			case "identical-replacement":
				data, err := os.ReadFile(physical)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(physical+"-replacement", data, 0o755))
				require.NoError(t, os.Rename(physical+"-replacement", physical))
			case "product-corrupt":
				require.NoError(t, os.WriteFile(physical, []byte("corrupt"), 0o755))
			case fixtureBeforeActivation:
				hook = func(phase string) error {
					if phase == scenario {
						require.NoError(t, os.Remove(filepath.Join(paths.Bin, "microfat-stub")))
					}
					return nil
				}
			case "activated":
				hook = func(phase string) error {
					if phase == scenario {
						return errors.New("injected post-activation failure")
					}
					return nil
				}
			}
			result, err := apply(t.Context(), snapshot, generation, source, ApplyOptions{}, hook)
			require.Error(t, err)
			assert.Equal(t, scenario == "activated", result.Activated)
			if scenario == "store-removed" {
				assert.NoDirExists(t, paths.Store)
			}
			if scenario == "bin-removed" {
				assert.NoDirExists(t, paths.Bin)
			}
			if scenario == fixtureBeforeActivation {
				assert.Equal(t, first.Generation.ID, readActive(t, paths).ID)
			}
		})
	}
}

func TestUpdateReadOnlyFailures(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"unmanaged", "missing-link", "wrong-link", "missing-current", "bad-current", "missing-generation",
		"current-corrupt", "stale-identical-payload", "bin-mode", "bin-symlink", "foreign-marker"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			paths := pathsFor(t)
			first := applyFixture(t, paths, "0.3.0")
			physical := installedCLI(t, paths)
			switch scenario {
			case "unmanaged":
				physical = filepath.Join(t.TempDir(), "microfat")
			case "missing-link":
				require.NoError(t, os.Remove(filepath.Join(paths.Bin, "microfat-stub")))
			case "wrong-link":
				require.NoError(t, os.Remove(filepath.Join(paths.Bin, "microfat-stub")))
				require.NoError(t, os.Symlink(physical, filepath.Join(paths.Bin, "microfat-stub")))
			case "missing-current":
				require.NoError(t, os.Remove(filepath.Join(paths.Store, currentLink)))
			case "bad-current":
				require.NoError(t, os.Remove(filepath.Join(paths.Store, currentLink)))
				require.NoError(t, os.Symlink("/outside", filepath.Join(paths.Store, currentLink)))
			case "missing-generation", "current-corrupt":
				second := applyFixture(t, paths, "0.3.1")
				path := filepath.Join(paths.Store, generationDir, second.Generation.ID)
				if scenario == "missing-generation" {
					require.NoError(t, os.RemoveAll(path))
				} else {
					require.NoError(t, os.WriteFile(filepath.Join(path, "microfat"), []byte("bad"), 0o755))
				}
			case "stale-identical-payload":
				snapshot, err := Inspect(paths)
				require.NoError(t, err)
				generation := first.Generation
				generation.ID = NewID()
				generation.Version = "0.3.1"
				_, err = Apply(t.Context(), snapshot, generation, filepath.Dir(physical), ApplyOptions{})
				require.NoError(t, err)
				_, err = InspectUpdate(physical)
				require.ErrorIs(t, err, ErrChanged)
				return
			case "bin-mode":
				require.NoError(t, os.Chmod(paths.Bin, 0o777))
			case "bin-symlink":
				require.NoError(t, os.Rename(paths.Bin, paths.Bin+"-original"))
				require.NoError(t, os.Symlink(paths.Bin+"-original", paths.Bin))
			case "foreign-marker":
				owner := Owner{Schema: 1, Kind: "homebrew"}
				rewriteJSON(t, filepath.Join(paths.Store, ownerFile), owner)
			}
			// Removing the lock proves reads do not create it, including failing reads.
			require.NoError(t, os.Remove(filepath.Join(paths.Store, lockFile)))
			_, err := ReadInstallation(physical)
			require.Error(t, err)
			assert.NoFileExists(t, filepath.Join(paths.Store, lockFile))
		})
	}
}

func TestUpdateReadDoesNotCreateLock(t *testing.T) {
	t.Parallel()
	paths := pathsFor(t)
	applyFixture(t, paths, "0.3.0")
	require.NoError(t, os.Remove(filepath.Join(paths.Store, lockFile)))
	_, err := ReadInstallation(installedCLI(t, paths))
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(paths.Store, lockFile))
}

func TestIndependentUpdateProcesses(t *testing.T) {
	t.Parallel()
	for _, competitor := range []string{"update", "apply", "uninstall"} {
		t.Run(competitor, func(t *testing.T) {
			t.Parallel()
			paths := pathsFor(t)
			first := applyFixture(t, paths, "0.3.0")
			updater := startChild(t, paths, "update", "0.3.1", "")
			other := startChild(t, paths, competitor, "0.3.2", "")
			// Both processes have captured G1. Let the competitor finish first.
			other.proceed(t)
			require.NoError(t, other.command.Wait())
			updater.proceed(t)
			require.NoError(t, updater.command.Wait())
			active := readActive(t, paths)
			if competitor == "uninstall" {
				assert.Equal(t, first.Generation.ID, active.ID)
				assert.NoFileExists(t, filepath.Join(paths.Bin, "microfat"))
			} else {
				assert.Equal(t, "0.3.2", active.Version)
			}
			entries, err := os.ReadDir(filepath.Join(paths.Store, generationDir))
			require.NoError(t, err)
			expected := 2
			if competitor == "uninstall" {
				expected = 1
			}
			assert.Len(t, entries, expected, "a stale updater must not publish another generation")
		})
	}
}

func TestUpdateProcessInterruption(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{fixtureBeforeActivation, fixtureActivated} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			paths := pathsFor(t)
			first := applyFixture(t, paths, "0.3.0")
			process := startChild(t, paths, "update", "0.3.1", phase)
			process.proceed(t)
			line, err := process.output.ReadString('\n')
			require.NoError(t, err)
			require.Equal(t, "stopped\n", line)
			require.NoError(t, process.command.Process.Kill())
			require.Error(t, process.command.Wait())
			active := readActive(t, paths)
			if phase == fixtureBeforeActivation {
				assert.Equal(t, first.Generation.ID, active.ID)
			} else {
				assert.Equal(t, "0.3.1", active.Version)
			}
			for _, name := range Products() {
				assert.FileExists(t, filepath.Join(paths.Bin, name))
			}
			// The stable lock is usable after a killed updater; recovery preserves G1.
			applyFixture(t, paths, "0.3.2")
			assert.FileExists(t, filepath.Join(paths.Store, generationDir, first.Generation.ID, "microfat"))
		})
	}
}

func TestUpdateValidationErrors(t *testing.T) {
	t.Parallel()
	paths := pathsFor(t)
	applyFixture(t, paths, "0.3.0")
	physical := installedCLI(t, paths)
	_, err := InspectUpdate("/unmanaged/microfat")
	require.ErrorIs(t, err, ErrOwnership)
	snapshot, err := InspectUpdate(physical)
	require.NoError(t, err)
	op, err := openOperation(t.Context(), snapshot)
	require.NoError(t, err)
	require.ErrorIs(t, op.checkUpdateSnapshot(Snapshot{}), ErrOwnership)
	owner := snapshot.Owner()
	owner.ID = NewID()
	rewriteJSON(t, filepath.Join(paths.Store, ownerFile), owner)
	require.ErrorIs(t, op.checkUpdateSnapshot(snapshot), ErrChanged)
	op.close()
	require.Error(t, op.checkUpdateSnapshot(snapshot))
	require.NoError(t, os.Remove(filepath.Join(filepath.Dir(physical), "microfat-stub")))
	_, err = updateFileIdentities(snapshot)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, os.RemoveAll(paths.Store))
	_, err = updateFileIdentities(snapshot)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestUpdateReadonlyStorePreservesSelection(t *testing.T) {
	t.Parallel()
	ordinaryUser(t)
	paths := pathsFor(t)
	old := applyFixture(t, paths, "0.3.0")
	snapshot, err := InspectUpdate(installedCLI(t, paths))
	require.NoError(t, err)
	next, source := fixture(t, "0.3.1")
	changeMode(t, paths.Store, 0o500)
	result, err := Apply(t.Context(), snapshot, next, source, ApplyOptions{})
	require.ErrorIs(t, err, os.ErrPermission)
	assert.False(t, result.Activated)
	assert.Equal(t, old.Generation.ID, readActive(t, paths).ID)
}
