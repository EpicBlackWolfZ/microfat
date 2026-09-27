//go:build linux

package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ordinaryUser(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission enforcement requires an ordinary user")
	}
}

func changeMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chmod(path, info.Mode().Perm()) })
	require.NoError(t, os.Chmod(path, mode))
}

func TestPermissionLossAtPublicationBoundaries(t *testing.T) {
	t.Parallel()
	ordinaryUser(t)
	for _, scenario := range []string{"metadata-write", "generation-publish", "activation-link", "activation-replace",
		"activation-sync", "entrypoints"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			paths := pathsFor(t)
			old := applyFixture(t, paths, "0.3.0")
			if scenario == "entrypoints" {
				require.NoError(t, os.Remove(filepath.Join(paths.Bin, "microfat-stub-minimal")))
			}
			snapshot, err := Inspect(paths)
			require.NoError(t, err)
			next, source := fixture(t, "0.3.1")
			if scenario == "generation-publish" {
				changeMode(t, filepath.Join(paths.Store, generationDir), 0o500)
			}
			hook := func(point string) error {
				if scenario == "metadata-write" && point == "staged-microfat-stub-minimal" {
					stages, err := filepath.Glob(filepath.Join(paths.Store, ".stage-*"))
					require.NoError(t, err)
					require.Len(t, stages, 1)
					changeMode(t, stages[0], 0o500)
				}
				if point == "before-activation" {
					switch scenario {
					case "activation-link":
						changeMode(t, paths.Store, 0o500)
					case "activation-replace":
						require.NoError(t, os.Remove(filepath.Join(paths.Store, currentLink)))
						require.NoError(t, os.Mkdir(filepath.Join(paths.Store, currentLink), directoryMode))
					}
				}
				if point == fixtureActivated {
					switch scenario {
					case "activation-sync":
						changeMode(t, paths.Store, 0o100)
					case "entrypoints":
						changeMode(t, paths.Bin, 0o500)
					}
				}
				return nil
			}
			result, err := apply(t.Context(), snapshot, next, source, ApplyOptions{}, hook)
			require.Error(t, err)
			after := scenario == "activation-sync" || scenario == "entrypoints"
			assert.Equal(t, after, result.Activated)
			require.NoError(t, os.Chmod(paths.Store, directoryMode))
			require.NoError(t, os.Chmod(paths.Bin, directoryMode))
			require.NoError(t, os.Chmod(filepath.Join(paths.Store, generationDir), directoryMode))
			if scenario == "activation-replace" {
				info, err := os.Stat(filepath.Join(paths.Store, currentLink))
				require.NoError(t, err)
				assert.True(t, info.IsDir(), "refuse to replace the conflicting directory")
				require.NoError(t, os.Remove(filepath.Join(paths.Store, currentLink)))
				require.NoError(t, os.Symlink(filepath.Join(generationDir, old.Generation.ID), filepath.Join(paths.Store, currentLink)))
			}
			expected := old.Generation.ID
			if after {
				expected = next.ID
			}
			assert.Equal(t, expected, readActive(t, paths).ID)
		})
	}
}

func TestPermissionFailuresDoNotClaimOrRemoveInstallation(t *testing.T) {
	t.Parallel()
	ordinaryUser(t)
	t.Run("claim", func(t *testing.T) {
		t.Parallel()
		paths := pathsFor(t)
		require.NoError(t, os.Mkdir(paths.Store, directoryMode))
		require.NoError(t, os.WriteFile(filepath.Join(paths.Store, lockFile), nil, metadataMode))
		snapshot, err := Inspect(paths)
		require.NoError(t, err)
		changeMode(t, paths.Store, 0o500)
		generation, source := fixture(t, "0.3.0")
		result, err := Apply(t.Context(), snapshot, generation, source, ApplyOptions{})
		require.ErrorIs(t, err, os.ErrPermission)
		assert.False(t, result.Activated)
		assert.NoFileExists(t, filepath.Join(paths.Store, ownerFile))
		assert.NoFileExists(t, filepath.Join(paths.Bin, "microfat"))
	})
	t.Run("uninstall", func(t *testing.T) {
		t.Parallel()
		paths := pathsFor(t)
		old := applyFixture(t, paths, "0.3.0")
		snapshot, err := Inspect(paths)
		require.NoError(t, err)
		changeMode(t, paths.Bin, 0o500)
		result, err := Uninstall(t.Context(), snapshot)
		require.ErrorIs(t, err, os.ErrPermission)
		assert.Empty(t, result.Removed)
		assert.Equal(t, old.Generation.ID, readActive(t, paths).ID)
		for _, name := range Products() {
			assert.FileExists(t, filepath.Join(paths.Bin, name))
		}
	})
	t.Run("missing-parent-write", func(t *testing.T) {
		t.Parallel()
		paths := pathsFor(t)
		snapshot, err := Inspect(paths)
		require.NoError(t, err)
		changeMode(t, filepath.Dir(paths.Store), 0o500)
		generation, source := fixture(t, "0.3.0")
		_, err = Apply(t.Context(), snapshot, generation, source, ApplyOptions{})
		require.ErrorIs(t, err, os.ErrPermission)
		assert.NoDirExists(t, paths.Store)
	})
}

func TestInterruptedCleanupRefusesUnsafeOrUnremovableEntries(t *testing.T) {
	t.Parallel()
	ordinaryUser(t)
	for _, scenario := range []string{"oversized-root", "writable-stage", "read-only-stage", "untrusted-owner-temp"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			paths := pathsFor(t)
			old := applyFixture(t, paths, "0.3.0")
			stage := filepath.Join(paths.Store, ".stage-"+NewID())
			switch scenario {
			case "oversized-root":
				for i := range 1025 {
					require.NoError(t, os.WriteFile(filepath.Join(paths.Store, fmt.Sprintf("unrelated-%d", i)), nil, metadataMode))
				}
			case "writable-stage", "read-only-stage":
				require.NoError(t, os.Mkdir(stage, directoryMode))
				require.NoError(t, os.WriteFile(filepath.Join(stage, "partial"), nil, metadataMode))
				mode := os.FileMode(0o777)
				if scenario == "read-only-stage" {
					mode = 0o500
				}
				changeMode(t, stage, mode)
			case "untrusted-owner-temp":
				require.NoError(t, os.Symlink("/unrelated", filepath.Join(paths.Store, ".owner-"+NewID())))
			}
			snapshot, err := Inspect(paths)
			require.NoError(t, err)
			next, source := fixture(t, "0.3.1")
			result, err := Apply(t.Context(), snapshot, next, source, ApplyOptions{})
			require.Error(t, err)
			assert.False(t, result.Activated)
			assert.Equal(t, old.Generation.ID, readActive(t, paths).ID)
		})
	}
}

func TestClosedFilesystemHandlesFailClosed(t *testing.T) {
	t.Parallel()
	paths := pathsFor(t)
	old := applyFixture(t, paths, "0.3.0")
	snapshot, err := Inspect(paths)
	require.NoError(t, err)
	op, err := openOperation(t.Context(), snapshot)
	require.NoError(t, err)
	defer op.close()
	require.NoError(t, op.store.Close())
	for name, operation := range map[string]func() error{
		"durability":             func() error { return syncDir(op.store, ".") },
		"publication":            func() error { return publishableDirectory(op.store, ".") },
		"cleanup":                op.cleanupStaging,
		"root-identity":          op.checkRoots,
		"ownership-scan":         func() error { return validateUnclaimed(op.store) },
		"generation-stage":       func() error { return op.stage(t.TempDir(), old.Generation) },
		"interrupted-activation": func() error { return op.removeInterruptedActivation(".current-" + NewID()) },
	} {
		t.Run(name, func(t *testing.T) { require.Error(t, operation()) })
	}
	assert.Equal(t, old.Generation.ID, readActive(t, paths).ID)
}

type metadataInfo struct {
	os.FileInfo
	metadata any
}

func (i metadataInfo) Sys() any { return i.metadata }

func TestOwnershipRequiresReliableMatchingUID(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "metadata")
	require.NoError(t, os.WriteFile(path, nil, metadataMode))
	info, err := os.Stat(path)
	require.NoError(t, err)
	unknown := metadataInfo{FileInfo: info}
	assert.Equal(t, -1, fileUID(unknown))
	require.ErrorIs(t, validateInfo(unknown, false, false), ErrOwnership)
	stat, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok)
	foreign := *stat
	foreign.Uid ^= 1
	require.ErrorIs(t, validateInfo(metadataInfo{FileInfo: info, metadata: &foreign}, false, true), ErrOwnership)
	require.ErrorIs(t, validateReadInfo(info, false, os.Geteuid()^1), ErrOwnership)
}

func TestBadMetadataAndLockObjectsCannotAcquireAuthority(t *testing.T) {
	t.Parallel()
	paths := pathsFor(t)
	generation, source := fixture(t, "0.3.0")
	_, err := Apply(t.Context(), Snapshot{}, generation, source, ApplyOptions{})
	require.Error(t, err)
	snapshot, err := Inspect(paths)
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(paths.Store, directoryMode))
	require.NoError(t, os.WriteFile(filepath.Join(paths.Store, ownerFile), []byte("invalid"), metadataMode))
	_, err = Apply(t.Context(), snapshot, generation, source, ApplyOptions{})
	require.Error(t, err)
	root, err := os.OpenRoot(paths.Store)
	require.NoError(t, err)
	defer root.Close()
	require.NoError(t, os.Remove(filepath.Join(paths.Store, ownerFile)))
	require.NoError(t, os.Remove(filepath.Join(paths.Store, lockFile)))
	require.NoError(t, os.Mkdir(filepath.Join(paths.Store, lockFile), directoryMode))
	_, err = lock(t.Context(), root)
	require.Error(t, err)
	require.NoError(t, os.Remove(filepath.Join(paths.Store, lockFile)))
	require.NoError(t, os.WriteFile(filepath.Join(paths.Store, lockFile), nil, metadataMode))
	require.NoError(t, os.Chmod(filepath.Join(paths.Store, lockFile), 0o666))
	_, err = lock(t.Context(), root)
	require.ErrorIs(t, err, ErrOwnership)
	require.ErrorIs(t, validateUnclaimed(root), ErrOwnership)
	require.NoError(t, os.Chmod(filepath.Join(paths.Store, lockFile), metadataMode))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = lock(ctx, root)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, errors.Is(err, ErrOwnership))
}

func TestInvalidSelectedMetadataIsNeverReusable(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"invalid-id", "identity-mismatch", "directory-link", "escape", "same-size-corruption"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			paths := pathsFor(t)
			old := applyFixture(t, paths, "0.3.0")
			current := filepath.Join(paths.Store, currentLink)
			switch scenario {
			case "invalid-id", "escape":
				require.NoError(t, os.Remove(current))
				target := "generations/invalid"
				if scenario == "escape" {
					target = "../elsewhere"
				}
				require.NoError(t, os.Symlink(target, current))
			case "identity-mismatch":
				record := old.Generation
				record.ID = NewID()
				rewriteJSON(t, filepath.Join(current, generationFile), record)
			case "directory-link":
				directory := filepath.Join(paths.Store, generationDir, old.Generation.ID)
				require.NoError(t, os.Rename(directory, directory+"-saved"))
				require.NoError(t, os.Symlink(directory+"-saved", directory))
			case "same-size-corruption":
				file := filepath.Join(current, "microfat")
				data, err := os.ReadFile(file)
				require.NoError(t, err)
				data[0] ^= 1
				require.NoError(t, os.WriteFile(file, data, directoryMode))
			}
			snapshot, err := Inspect(paths)
			require.NoError(t, err)
			generation, source := fixture(t, "0.3.1")
			result, err := Apply(t.Context(), snapshot, generation, source, ApplyOptions{})
			require.Error(t, err)
			assert.False(t, result.Activated)
		})
	}
}

func TestUnreadableAncestorsAndConflictingMetadata(t *testing.T) {
	t.Parallel()
	ordinaryUser(t)
	directory := t.TempDir()
	changeMode(t, directory, 0)
	child := filepath.Join(directory, "child")
	require.ErrorIs(t, validateAncestors(child), os.ErrPermission)
	_, err := rootInfo(child)
	require.ErrorIs(t, err, os.ErrPermission)
	directory = t.TempDir()
	file := filepath.Join(directory, "file")
	require.NoError(t, os.WriteFile(file, nil, metadataMode))
	_, err = rootInfo(file)
	require.ErrorIs(t, err, ErrOwnership)
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	defer root.Close()
	require.Error(t, writeJSON(root, "unserializable", make(chan struct{})))
	assert.NoFileExists(t, filepath.Join(directory, "unserializable"))
	require.NoError(t, os.Mkdir(filepath.Join(directory, ownerFile), directoryMode))
	op := &operation{store: root, paths: Paths{Store: directory, Bin: t.TempDir()}}
	require.Error(t, op.initializeOwner())
	info, err := os.Stat(filepath.Join(directory, ownerFile))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	require.NoError(t, os.Remove(filepath.Join(directory, ownerFile)))
	require.NoError(t, os.WriteFile(filepath.Join(directory, currentLink), nil, metadataMode))
	_, err = activeGeneration(root)
	require.ErrorIs(t, err, ErrCorrupt)
}
