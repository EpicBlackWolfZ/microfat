//go:build linux

package e2e_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Construct the documented owned-layout fixture without calling installer or
// discovery internals. Runtime behavior is exercised only by external CLIs.
func writeManagedGeneration(t *testing.T, store, id, version, cli, full, minimal string, suffix []byte) string {
	t.Helper()
	dir := filepath.Join(store, "generations", id)
	require.NoError(t, os.MkdirAll(dir, defaultFilePerm))
	files := map[string]any{}
	for name, source := range map[string]string{"microfat": cli, "microfat-stub": full, "microfat-stub-minimal": minimal} {
		data, err := os.ReadFile(source)
		require.NoError(t, err)
		if name != "microfat" {
			data = append(data, suffix...)
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, defaultFilePerm))
		files[name] = map[string]any{"size": len(data), "sha256": fmt.Sprintf("%x", sha256.Sum256(data))}
	}
	metadata, err := json.Marshal(map[string]any{"schema": 1, "id": id, "version": version, "arch": currentHostArch,
		"archive_sha256": strings.Repeat("a", 64), "files": files})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "generation.json"), metadata, privateFilePerm))
	return dir
}

func managedLayoutFixture(t *testing.T, cli, minimal string) (bin, store, old string) {
	t.Helper()
	root := t.TempDir()
	bin, store = filepath.Join(root, "bin space"), filepath.Join(root, "store space")
	require.NoError(t, os.Mkdir(bin, defaultFilePerm))
	oldID, newID := strings.Repeat("1", 32), strings.Repeat("2", 32)
	old = writeManagedGeneration(t, store, oldID, "0.3.0", cli, stubPath, minimal, nil)
	writeManagedGeneration(t, store, newID, "0.3.1", cli, stubPath, minimal, []byte("new generation companion"))
	owner, err := json.Marshal(map[string]any{"schema": 1, "kind": "microfat-installer", "id": strings.Repeat("3", 32),
		"uid": os.Geteuid(), "bin": bin, "store": store})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(store, "owner.json"), owner, privateFilePerm))
	require.NoError(t, os.Symlink(filepath.Join("generations", oldID), filepath.Join(store, "current")))
	for _, name := range []string{"microfat", "microfat-stub", "microfat-stub-minimal"} {
		require.NoError(t, os.Symlink(filepath.Join(store, "current", name), filepath.Join(bin, name)))
	}
	return bin, store, old
}

func TestManagedGenerationPausedUpgrade(t *testing.T) {
	minimal := filepath.Join(t.TempDir(), "minimal")
	require.NoError(t, compileBinaryWithFlags(stubPackagePath, minimal, []string{envBaselineAMD64, "GOARM64=v8.0"}, "-tags=minimal"))
	level := "v1"
	if currentHostArch == archARM64 {
		level = manifestARM64Base
	}
	launchers := map[string]string{execModeNative: cliPath}
	for profile, stub := range map[string]string{launcherFullProfile: stubPath, launcherMinimalProfile: minimal} {
		fat := filepath.Join(t.TempDir(), "microfat")
		require.NoError(t, packBinary(cliPath, stub, "managed-cli", fat, map[string]string{level: cliPath}))
		launchers[profile] = fat
	}
	for launcher, cli := range launchers {
		for _, mode := range []string{execModeMemfd, execModeCache} {
			for _, profile := range []string{launcherFullProfile, launcherMinimalProfile} {
				for _, removeOld := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/remove=%t", launcher, mode, profile, removeOld), func(t *testing.T) {
						bin, store, old := managedLayoutFixture(t, cli, minimal)
						out := filepath.Join(t.TempDir(), "packed")
						args := []string{"pack", "--name", "delayed-generation", "--stub-profile", profile,
							"-v", level + "=" + goldenVariantBins[level], "-o", out}
						output, err := runPausedImage(t, filepath.Join(bin, "microfat"), args,
							[]string{"MICROFAT_EXEC_MODE=" + mode, "PATH=" + bin}, func() {
								require.NoError(t, os.Symlink(filepath.Join("generations", strings.Repeat("2", 32)), filepath.Join(store, "next")))
								require.NoError(t, os.Rename(filepath.Join(store, "next"), filepath.Join(store, "current")))
								if removeOld {
									require.NoError(t, os.RemoveAll(old))
								}
							})
						if removeOld {
							require.Error(t, err, output)
							require.Contains(t, output, "cannot bind companion discovery")
							require.NoFileExists(t, out)
							return
						}
						require.NoError(t, err, output)
						stub := stubPath
						if profile == launcherMinimalProfile {
							stub = minimal
						}
						expected, err := os.ReadFile(stub)
						require.NoError(t, err)
						packed, err := os.ReadFile(out)
						require.NoError(t, err)
						require.Greater(t, len(packed), len(expected))
						require.Equal(t, expected, packed[:len(expected)])
						require.NotContains(t, string(packed[len(expected):]), "new generation companion")
					})
				}
			}
		}
	}
}
