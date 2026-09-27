//go:build linux

package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestInstallRootCreationRace(t *testing.T) {
	t.Parallel()
	const writable = "writable-ancestor"
	for _, scenario := range []string{writable, fixtureSymlink, "file", fixtureFIFO, "failure", "safe", "vanished", "unreadable"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			ordinaryUser(t)
			base := t.TempDir()
			outside := t.TempDir()
			contended := filepath.Join(base, "contended")
			injected := false
			root, _, err := openInstallRoot(filepath.Join(contended, "leaf"), func(parent *os.Root, name string, mode os.FileMode) error {
				if name == "contended" {
					injected = true
					switch scenario {
					case writable, "safe":
						require.NoError(t, parent.Mkdir(name, privateMode))
						if scenario == writable {
							require.NoError(t, os.Chmod(contended, 0o777))
						}
					case fixtureSymlink:
						require.NoError(t, parent.Symlink(outside, name))
					case "file":
						require.NoError(t, os.WriteFile(contended, []byte("preserve"), metadataMode))
					case fixtureFIFO:
						require.NoError(t, unix.Mkfifo(contended, privateMode))
					case "failure":
						return unix.ENOSPC
					case "vanished":
						// Successful creation followed by removal before validation.
						require.NoError(t, parent.Mkdir(name, mode))
						return parent.Remove(name)
					case "unreadable":
						require.NoError(t, parent.Mkdir(name, mode))
						changeMode(t, contended, 0o100)
						return nil
					}
				}
				return parent.Mkdir(name, mode)
			})
			require.True(t, injected)
			if scenario == "safe" {
				require.NoError(t, err)
				require.NoError(t, root.Close())
				info, err := os.Stat(contended)
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(privateMode), info.Mode().Perm(), "a raced existing directory keeps its private mode")
				return
			}
			require.Error(t, err)
			require.Nil(t, root)
			if scenario == "failure" {
				require.ErrorIs(t, err, unix.ENOSPC)
			}
			assert.NoDirExists(t, filepath.Join(outside, "leaf"), "never write through a raced symlink")
			assert.NoDirExists(t, filepath.Join(contended, "leaf"), "never descend into an unvalidated directory")
			if scenario == writable {
				info, err := os.Stat(contended)
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0o777), info.Mode().Perm(), "do not take ownership by chmod")
			}
		})
	}
}

func TestApplyRejectsAncestorChangedBeforeActivation(t *testing.T) {
	t.Parallel()
	paths := pathsFor(t)
	first := applyFixture(t, paths, "0.3.0")
	snapshot, err := Inspect(paths)
	require.NoError(t, err)
	generation, source := fixture(t, "0.3.1")
	ancestor := filepath.Dir(paths.Store)
	result, err := apply(t.Context(), snapshot, generation, source, ApplyOptions{}, func(at string) error {
		if at == "generation-published" {
			return os.Chmod(ancestor, 0o777)
		}
		return nil
	})
	require.ErrorIs(t, err, ErrOwnership)
	assert.False(t, result.Activated)
	assert.Equal(t, first.Generation, readActive(t, paths))
}

func TestInstallWithRestrictiveUmask(t *testing.T) {
	if os.Getenv("MICROFAT_TEST_PRIVATE_UMASK") != "1" {
		t.Parallel()
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestInstallWithRestrictiveUmask$")
		cmd.Env = append(os.Environ(), "MICROFAT_TEST_PRIVATE_UMASK=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return
	}
	// Umask is process-wide, so exercise it only in this isolated test process.
	old := unix.Umask(0o077)
	defer unix.Umask(old)
	base := t.TempDir()
	paths := Paths{Bin: filepath.Join(base, "new-bin-parent", "bin"), Store: filepath.Join(base, "new-store-parent", "store")}
	result := applyFixture(t, paths, "0.3.0")
	for _, name := range []string{filepath.Dir(paths.Bin), paths.Bin, filepath.Dir(paths.Store), paths.Store,
		filepath.Join(paths.Store, generationDir), filepath.Join(paths.Store, generationDir, result.Generation.ID)} {
		info, err := os.Stat(name)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(directoryMode), info.Mode().Perm(), "%s must be traversable", name)
	}
	// Preserve pre-existing private directories, including on a subsequent install.
	for _, name := range []string{base, paths.Bin, paths.Store, filepath.Join(paths.Store, generationDir)} {
		require.NoError(t, os.Chmod(name, privateMode))
	}
	applyFixture(t, paths, "0.3.1")
	for _, name := range []string{base, paths.Bin, paths.Store, filepath.Join(paths.Store, generationDir)} {
		info, err := os.Stat(name)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(privateMode), info.Mode().Perm(), "%s must retain its privacy", name)
	}
}

func TestInstallRootCreationPermissionFailure(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("requires ordinary user permission enforcement")
	}
	base := t.TempDir()
	require.NoError(t, os.Chmod(base, 0o500))
	t.Cleanup(func() { _ = os.Chmod(base, privateMode) })
	root, _, err := openInstallRoot(filepath.Join(base, "absent", "leaf"), (*os.Root).Mkdir)
	require.ErrorIs(t, err, os.ErrPermission)
	require.Nil(t, root)
	require.NoDirExists(t, filepath.Join(base, "absent"))
}

func TestInstallRootMustBeOwnedByInstaller(t *testing.T) {
	t.Parallel()
	ordinaryUser(t)
	// /tmp is allowed as a root-owned sticky ancestor, never as the owned store.
	info, err := os.Lstat("/tmp")
	require.NoError(t, err)
	if fileUID(info) != 0 || info.Mode()&os.ModeSticky == 0 {
		t.Skip("requires a root-owned sticky /tmp")
	}
	root, _, err := openInstallRoot("/tmp", (*os.Root).Mkdir)
	require.ErrorIs(t, err, ErrOwnership)
	require.Nil(t, root)
}
