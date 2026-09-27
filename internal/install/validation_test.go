//go:build linux

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestPathsAndGenerationValidation(t *testing.T) {
	t.Parallel()
	for _, paths := range []Paths{{}, {Bin: "relative", Store: "/store"}, {Bin: "/", Store: "/store"},
		{Bin: "/bin/../bin", Store: "/store"}, {Bin: "/same", Store: "/same"}, {Bin: "/a", Store: "/a/b"}, {Bin: "/a/b", Store: "/a"}} {
		t.Run(paths.Bin+":"+paths.Store, func(t *testing.T) {
			t.Parallel()
			require.Error(t, paths.Validate())
			_, err := Inspect(paths)
			require.Error(t, err)
		})
	}
	for _, name := range []string{"schema", "id", "version", "arch", "digest", "count", "file-size", "file-hash"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			generation, source := fixture(t, "0.3.0")
			switch name {
			case "schema":
				generation.Schema++
			case "id":
				generation.ID = "../../escape"
			case "version":
				generation.Version = "0.3.0-rc.1"
			case "arch":
				generation.Arch = "other"
			case "digest":
				generation.ArchiveSHA256 = "unknown"
			case "count":
				delete(generation.Files, "microfat")
			case "file-size":
				file := generation.Files["microfat"]
				file.Size = MaxFileBytes + 1
				generation.Files["microfat"] = file
			case "file-hash":
				file := generation.Files["microfat"]
				file.SHA256 = "unknown"
				generation.Files["microfat"] = file
			}
			_, err := Apply(t.Context(), Snapshot{}, generation, source, ApplyOptions{})
			require.Error(t, err)
		})
	}
}

func TestRejectsUnsafeRootsAndOwnership(t *testing.T) {
	t.Parallel()
	for _, name := range []string{fixtureSymlink, "regular", "writable", "store-symlink", "unclaimed", "owner-schema", "owner-id",
		"owner-kind", fixtureOwnerUID, "owner-bin", "owner-store", "owner-null", "owner-unknown", "owner-duplicate", "owner-trailing",
		"owner-oversize", "owner-hardlink", "owner-fifo", "owner-writable"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			paths := pathsFor(t)
			if strings.HasPrefix(name, "owner-") {
				applyFixture(t, paths, "0.3.0")
			}
			switch name {
			case fixtureSymlink:
				require.NoError(t, os.Symlink(t.TempDir(), filepath.Dir(paths.Bin)+"/link"))
				paths.Bin = filepath.Dir(paths.Bin) + "/link/bin"
			case "regular":
				require.NoError(t, os.WriteFile(paths.Bin, []byte("file"), metadataMode))
			case "writable":
				require.NoError(t, os.Mkdir(paths.Store, directoryMode))
				require.NoError(t, os.Chmod(paths.Store, 0o777))
			case "store-symlink":
				require.NoError(t, os.Symlink(t.TempDir(), paths.Store))
			case "unclaimed":
				require.NoError(t, os.Mkdir(paths.Store, directoryMode))
				require.NoError(t, os.WriteFile(filepath.Join(paths.Store, "other"), nil, metadataMode))
			default:
				path := filepath.Join(paths.Store, ownerFile)
				snapshot, err := Inspect(paths)
				require.NoError(t, err)
				owner := snapshot.Owner()
				switch name {
				case "owner-schema":
					owner.Schema++
				case "owner-id":
					owner.ID = "bad"
				case "owner-kind":
					owner.Kind = "homebrew"
				case fixtureOwnerUID:
					owner.UID++
				case "owner-bin":
					owner.Bin = "/unrelated"
				case "owner-store":
					owner.Store = "/unrelated"
				case "owner-null":
					require.NoError(t, os.WriteFile(path, []byte("null"), metadataMode))
				case "owner-unknown":
					require.NoError(t, os.WriteFile(path, []byte(`{"unknown":1}`), metadataMode))
				case "owner-duplicate":
					require.NoError(t, os.WriteFile(path, []byte(`{"schema":1,"schema":1}`), metadataMode))
				case "owner-trailing":
					require.NoError(t, os.WriteFile(path, []byte(`{} {}`), metadataMode))
				case "owner-oversize":
					require.NoError(t, os.WriteFile(path, []byte(strings.Repeat(" ", metadataLimit+1)), metadataMode))
				case "owner-hardlink":
					require.NoError(t, os.Link(path, path+"-alias"))
				case "owner-fifo":
					require.NoError(t, os.Remove(path))
					require.NoError(t, unix.Mkfifo(path, metadataMode))
				case "owner-writable":
					require.NoError(t, os.Chmod(path, 0o666))
				}
				if name == "owner-schema" || name == "owner-id" || name == "owner-kind" ||
					name == fixtureOwnerUID || name == "owner-bin" || name == "owner-store" {
					rewriteJSON(t, path, owner)
				}
			}
			_, err := Inspect(paths)
			require.Error(t, err)
		})
	}
}

func TestSourceAndStagingFailuresLeaveOldGeneration(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"missing-source", "missing-file", fixtureFIFO, fixtureSymlink, "hardlink", "size", "hash",
		"generation-conflict",
		"generation-directory-file", "generation-directory-link"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			paths := pathsFor(t)
			old := applyFixture(t, paths, "0.3.0")
			snapshot, err := Inspect(paths)
			require.NoError(t, err)
			generation, source := fixture(t, "0.3.1")
			file := filepath.Join(source, "microfat")
			switch name {
			case "missing-source":
				source = filepath.Join(source, "missing")
			case "missing-file":
				require.NoError(t, os.Remove(file))
			case fixtureFIFO:
				require.NoError(t, os.Remove(file))
				require.NoError(t, unix.Mkfifo(file, metadataMode))
			case fixtureSymlink:
				require.NoError(t, os.Remove(file))
				require.NoError(t, os.Symlink("microfat-stub", file))
			case "hardlink":
				require.NoError(t, os.Link(file, file+"-alias"))
			case "size":
				require.NoError(t, os.WriteFile(file, []byte("bad"), directoryMode))
			case "hash":
				record := generation.Files["microfat"]
				record.SHA256 = strings.Repeat("0", 64)
				generation.Files["microfat"] = record
			case "generation-conflict":
				generation.ID = old.Generation.ID
			case "generation-directory-file", "generation-directory-link":
				dir := filepath.Join(paths.Store, generationDir)
				require.NoError(t, os.Rename(dir, dir+"-original"))
				if name == "generation-directory-file" {
					require.NoError(t, os.WriteFile(dir, []byte("unrelated"), metadataMode))
				} else {
					require.NoError(t, os.Symlink(generationDir+"-original", dir))
				}
			}
			result, err := Apply(t.Context(), snapshot, generation, source, ApplyOptions{})
			require.Error(t, err)
			assert.False(t, result.Activated)
			target, err := os.Readlink(filepath.Join(paths.Store, currentLink))
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(generationDir, old.Generation.ID), target)
		})
	}
}

func TestRecoveryOfUnclaimedStoreAndUninstallLimits(t *testing.T) {
	t.Parallel()
	paths := pathsFor(t)
	require.NoError(t, os.Mkdir(paths.Store, directoryMode))
	require.NoError(t, os.WriteFile(filepath.Join(paths.Store, ".owner-interrupted"), []byte("partial"), metadataMode))
	snapshot, err := Inspect(paths)
	require.NoError(t, err)
	assert.Nil(t, snapshot.Owner())
	_, err = Uninstall(t.Context(), snapshot)
	require.ErrorIs(t, err, ErrOwnership)
	result := applyFixture(t, paths, "0.3.0")
	info, err := os.Stat(filepath.Join(paths.Store, generationDir, result.Generation.ID))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(directoryMode), info.Mode().Perm(), "system-owned generations must be traversable")
	snapshot, err = Inspect(paths)
	require.NoError(t, err)
	_, err = Uninstall(t.Context(), snapshot)
	require.NoError(t, err)
	removed, err := Uninstall(t.Context(), snapshot)
	require.NoError(t, err)
	assert.Empty(t, removed.Removed)
	require.NoError(t, os.Remove(filepath.Join(paths.Store, ownerFile)))
	_, err = Uninstall(t.Context(), snapshot)
	require.ErrorIs(t, err, ErrChanged)
}
