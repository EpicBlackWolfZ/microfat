//go:build linux

package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	fixtureFIFO    = "fifo"
	fixtureSymlink = "symlink"
)

func fixture(t *testing.T, version string) (Generation, string) {
	t.Helper()
	dir := t.TempDir()
	files := make(map[string]File)
	for _, name := range Products() {
		data := []byte("#!/bin/sh\nprintf '%s\\n' '" + version + ":" + name + "'\n")
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, directoryMode))
		hash := sha256.Sum256(data)
		files[name] = File{Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:])}
	}
	hash := sha256.Sum256([]byte(version))
	return Generation{Schema: SchemaVersion, ID: NewID(), Version: version, Arch: "amd64",
		ArchiveSHA256: hex.EncodeToString(hash[:]), Files: files}, dir
}

func pathsFor(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	return Paths{Bin: filepath.Join(root, "bin"), Store: filepath.Join(root, "data")}
}

func applyFixture(t *testing.T, paths Paths, version string) Result {
	t.Helper()
	snapshot, err := Inspect(paths)
	require.NoError(t, err)
	generation, source := fixture(t, version)
	result, err := Apply(t.Context(), snapshot, generation, source, ApplyOptions{})
	require.NoError(t, err)
	return result
}

func readActive(t *testing.T, paths Paths) Generation {
	t.Helper()
	root, err := os.OpenRoot(paths.Store)
	require.NoError(t, err)
	defer root.Close()
	generation, err := activeGeneration(root)
	require.NoError(t, err)
	require.NoError(t, verifyGeneration(root, generation))
	return generation
}

func rewriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, metadataMode))
}

func TestInstallationLifecycle(t *testing.T) {
	t.Parallel()
	paths := pathsFor(t)
	first := applyFixture(t, paths, "0.3.0")
	require.True(t, first.Activated)
	snapshot, err := Inspect(paths)
	require.NoError(t, err)
	owner := snapshot.Owner()
	require.Equal(t, OwnerKind, owner.Kind)
	owner.ID = "changed copy"
	require.NotEqual(t, owner.ID, snapshot.Owner().ID)
	for _, name := range Products() {
		out, err := exec.CommandContext(t.Context(), filepath.Join(paths.Bin, name)).Output()
		require.NoError(t, err)
		assert.Equal(t, "0.3.0:"+name+"\n", string(out))
	}
	second := applyFixture(t, paths, "0.3.1")
	assert.NotEqual(t, first.Generation.ID, second.Generation.ID)
	assert.Equal(t, second.Generation, readActive(t, paths))
	old := filepath.Join(paths.Store, generationDir, first.Generation.ID, "microfat")
	out, err := exec.CommandContext(t.Context(), old).Output()
	require.NoError(t, err)
	assert.Equal(t, "0.3.0:microfat\n", string(out))
	reinstalled := applyFixture(t, paths, "0.3.1")
	assert.True(t, reinstalled.Reused)
	assert.False(t, reinstalled.Activated)
	assert.Equal(t, second.Generation.ID, reinstalled.Generation.ID)
	snapshot, err = Inspect(paths)
	require.NoError(t, err)
	removed, err := Uninstall(t.Context(), snapshot)
	require.NoError(t, err)
	assert.ElementsMatch(t, Products(), removed.Removed)
	for _, name := range Products() {
		_, err := os.Lstat(filepath.Join(paths.Bin, name))
		assert.ErrorIs(t, err, os.ErrNotExist)
	}
	assert.FileExists(t, old)
	assert.FileExists(t, filepath.Join(paths.Store, lockFile))
	assert.True(t, applyFixture(t, paths, "0.3.1").Reused)
}

func TestInterruptedInstallRecovery(t *testing.T) {
	t.Parallel()
	points := []string{"staged-microfat", "staged-microfat-stub", "staged-microfat-stub-minimal",
		"generation-published", "before-activation", "activated", "linked-microfat", "linked-microfat-stub", "linked-microfat-stub-minimal"}
	for _, existing := range []bool{false, true} {
		for _, point := range points {
			t.Run(point+"/existing="+map[bool]string{false: "no", true: "yes"}[existing], func(t *testing.T) {
				t.Parallel()
				paths := pathsFor(t)
				if existing {
					applyFixture(t, paths, "0.3.0")
				}
				snapshot, err := Inspect(paths)
				require.NoError(t, err)
				generation, source := fixture(t, "0.3.1")
				injected := errors.New("interrupted at " + point)
				result, err := apply(t.Context(), snapshot, generation, source, ApplyOptions{}, func(at string) error {
					if at == point {
						return injected
					}
					return nil
				})
				require.ErrorIs(t, err, injected)
				activated := point == "activated" || strings.HasPrefix(point, "linked-")
				assert.Equal(t, activated, result.Activated)
				if activated {
					assert.Equal(t, "0.3.1", readActive(t, paths).Version)
				} else if existing {
					assert.Equal(t, "0.3.0", readActive(t, paths).Version)
				}
				for _, name := range Products() {
					entry := filepath.Join(paths.Bin, name)
					if _, err := os.Lstat(entry); err == nil {
						_, err := os.Stat(entry)
						require.NoError(t, err, "published link must never dangle")
					}
				}
				applyFixture(t, paths, "0.3.1")
				assert.Equal(t, "0.3.1", readActive(t, paths).Version)
			})
		}
	}
}

func TestSnapshotAndEntrypointConflicts(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"new-owner", "store-replaced", "bin-replaced", "owner-replaced", "entrypoint-conflict"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			paths := pathsFor(t)
			if scenario != "new-owner" {
				applyFixture(t, paths, "0.3.0")
			}
			snapshot, err := Inspect(paths)
			require.NoError(t, err)
			switch scenario {
			case "new-owner":
				applyFixture(t, paths, "0.3.0")
			case "store-replaced", "bin-replaced":
				name := paths.Store
				if scenario == "bin-replaced" {
					name = paths.Bin
				}
				require.NoError(t, os.Rename(name, name+"-old"))
				require.NoError(t, os.Mkdir(name, directoryMode))
			case "owner-replaced":
				owner := snapshot.Owner()
				owner.ID = NewID()
				rewriteJSON(t, filepath.Join(paths.Store, ownerFile), owner)
			case "entrypoint-conflict":
				name := filepath.Join(paths.Bin, "microfat")
				require.NoError(t, os.Remove(name))
				require.NoError(t, os.WriteFile(name, []byte("unrelated"), directoryMode))
			}
			generation, source := fixture(t, "0.3.1")
			result, err := Apply(t.Context(), snapshot, generation, source, ApplyOptions{})
			require.Error(t, err)
			assert.False(t, result.Activated)
			if scenario == "entrypoint-conflict" {
				assert.ErrorIs(t, err, ErrConflict)
			} else {
				assert.ErrorIs(t, err, ErrChanged)
			}
		})
	}
}

func TestManagedCorruptionNeedsRepair(t *testing.T) {
	t.Parallel()
	for _, corrupt := range []string{"bytes", "mode", "hardlink", fixtureSymlink, fixtureFIFO,
		"missing-file", "missing-metadata", "dangling-current"} {
		t.Run(corrupt, func(t *testing.T) {
			t.Parallel()
			paths := pathsFor(t)
			old := applyFixture(t, paths, "0.3.0")
			file := filepath.Join(paths.Store, generationDir, old.Generation.ID, "microfat")
			switch corrupt {
			case "bytes":
				require.NoError(t, os.WriteFile(file, []byte("changed"), directoryMode))
			case "mode":
				require.NoError(t, os.Chmod(file, metadataMode))
			case "hardlink":
				require.NoError(t, os.Link(file, file+"-alias"))
			case fixtureSymlink:
				require.NoError(t, os.Remove(file))
				require.NoError(t, os.Symlink("microfat-stub", file))
			case fixtureFIFO:
				require.NoError(t, os.Remove(file))
				require.NoError(t, unix.Mkfifo(file, metadataMode))
			case "missing-file":
				require.NoError(t, os.Remove(file))
			case "missing-metadata":
				require.NoError(t, os.Remove(filepath.Join(filepath.Dir(file), generationFile)))
			case "dangling-current":
				require.NoError(t, os.Remove(filepath.Join(paths.Store, currentLink)))
				require.NoError(t, os.Symlink(filepath.Join(generationDir, NewID()), filepath.Join(paths.Store, currentLink)))
			}
			snapshot, err := Inspect(paths)
			require.NoError(t, err)
			generation, source := fixture(t, "0.3.1")
			result, err := Apply(t.Context(), snapshot, generation, source, ApplyOptions{})
			require.ErrorIs(t, err, ErrCorrupt)
			assert.False(t, result.Activated)
			result, err = Apply(t.Context(), snapshot, generation, source, ApplyOptions{Repair: true})
			require.NoError(t, err)
			assert.True(t, result.Activated)
			assert.Equal(t, "0.3.1", readActive(t, paths).Version)
		})
	}
}

func TestRejectsUnmanagedAndChangedLinks(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"regular", "directory", fixtureSymlink, "owned-shaped-symlink", fixtureFIFO} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			paths := pathsFor(t)
			require.NoError(t, os.MkdirAll(paths.Bin, directoryMode))
			file := filepath.Join(paths.Bin, "microfat")
			switch kind {
			case "regular":
				require.NoError(t, os.WriteFile(file, []byte("untouched"), directoryMode))
			case "directory":
				require.NoError(t, os.Mkdir(file, directoryMode))
			case fixtureSymlink:
				require.NoError(t, os.Symlink("somewhere", file))
			case "owned-shaped-symlink":
				require.NoError(t, os.Symlink(filepath.Join(paths.Store, currentLink, "microfat"), file))
			case fixtureFIFO:
				require.NoError(t, unix.Mkfifo(file, metadataMode))
			}
			snapshot, err := Inspect(paths)
			require.NoError(t, err)
			generation, source := fixture(t, "0.3.0")
			_, err = Apply(t.Context(), snapshot, generation, source, ApplyOptions{})
			require.ErrorIs(t, err, ErrConflict)
			_, err = os.Lstat(file)
			require.NoError(t, err)
		})
	}
	t.Run("uninstall preserves changed entries", func(t *testing.T) {
		t.Parallel()
		paths := pathsFor(t)
		applyFixture(t, paths, "0.3.0")
		snapshot, err := Inspect(paths)
		require.NoError(t, err)
		name := filepath.Join(paths.Bin, "microfat")
		require.NoError(t, os.Remove(name))
		require.NoError(t, os.WriteFile(name, []byte("not ours"), directoryMode))
		result, err := Uninstall(t.Context(), snapshot)
		require.ErrorIs(t, err, ErrConflict)
		assert.Equal(t, []string{"microfat"}, result.Preserved)
		assert.Len(t, result.Removed, 2)
		data, err := os.ReadFile(name)
		require.NoError(t, err)
		assert.Equal(t, "not ours", string(data))
	})
}

func TestCancellationAndMidOperationChanges(t *testing.T) {
	t.Parallel()
	t.Run("cancel before writes", func(t *testing.T) {
		t.Parallel()
		paths := pathsFor(t)
		snapshot, err := Inspect(paths)
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		generation, source := fixture(t, "0.3.0")
		_, err = Apply(ctx, snapshot, generation, source, ApplyOptions{})
		require.ErrorIs(t, err, context.Canceled)
		_, err = os.Stat(paths.Store)
		require.ErrorIs(t, err, os.ErrNotExist)
	})
	for _, point := range []string{"cancel", "bin", "store", "entrypoint"} {
		t.Run(point, func(t *testing.T) {
			t.Parallel()
			paths := pathsFor(t)
			applyFixture(t, paths, "0.3.0")
			snapshot, err := Inspect(paths)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			generation, source := fixture(t, "0.3.1")
			result, err := apply(ctx, snapshot, generation, source, ApplyOptions{}, func(at string) error {
				if at != "generation-published" {
					return nil
				}
				switch point {
				case "cancel":
					cancel()
				case "bin", "store":
					name := paths.Bin
					if point == "store" {
						name = paths.Store
					}
					require.NoError(t, os.Rename(name, name+"-old"))
					require.NoError(t, os.Mkdir(name, directoryMode))
				case "entrypoint":
					name := filepath.Join(paths.Bin, "microfat")
					require.NoError(t, os.Remove(name))
					require.NoError(t, os.Symlink("unexpected", name))
				}
				return nil
			})
			require.Error(t, err)
			assert.False(t, result.Activated)
		})
	}
}
