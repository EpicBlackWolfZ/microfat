package e2e_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func cacheTreeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		var data []byte
		if entry.Type().IsRegular() {
			data, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		result[path] = fmt.Sprintf("%s:%x", info.Mode(), sha256.Sum256(data))
		return nil
	})
	require.NoError(t, err)
	return result
}

func runCacheCheck(t *testing.T, env []string, path string, args ...string) ([]byte, error) {
	t.Helper()
	const timeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), env...)
	output, err := cmd.CombinedOutput()
	require.NoError(t, ctx.Err(), string(output))
	return output, err
}

func TestCacheVerificationPreservesFilesystem(t *testing.T) {
	t.Parallel()
	for _, route := range []string{"cli-explicit", "cli-env", "cli-xdg", "stub-env", "stub-xdg"} {
		for _, state := range []string{"absent", "insecure", "empty", "valid", "corrupt"} {
			t.Run(route+"/"+state, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				cacheDir := filepath.Join(root, "microfat")
				env := []string{"MICROFAT_CACHE_DIR=", "XDG_CACHE_HOME=" + root, "TMPDIR=" + root}
				path, args := cliPath, []string{"prewarm", "--verify", "--json", goldenFatBin}
				switch route {
				case "cli-explicit":
					args = append(args, "--cache-dir", cacheDir)
				case "cli-env":
					env = append(env, "MICROFAT_CACHE_DIR="+cacheDir)
				case "stub-env":
					env = append(env, "MICROFAT_CACHE_DIR="+cacheDir)
					path, args = goldenFatBin, []string{"--microfat:prewarm=verify,json"}
				case "stub-xdg":
					path, args = goldenFatBin, []string{"--microfat:prewarm=verify,json"}
				}
				prepareReadonlyCache(t, state, cacheDir, env)
				before := cacheTreeSnapshot(t, root)
				output, err := runCacheCheck(t, env, path, args...)
				if state == "valid" {
					require.NoError(t, err, string(output))
					require.Contains(t, string(output), `"valid": true`)
				} else {
					require.Error(t, err, string(output))
				}
				require.Equal(t, before, cacheTreeSnapshot(t, root), "verification changed filesystem state: %s", output)
			})
		}
	}
}

func prepareReadonlyCache(t *testing.T, state, dir string, env []string) {
	t.Helper()
	if state == "absent" {
		return
	}
	require.NoError(t, os.Mkdir(dir, privateDirPerm))
	if state == "insecure" {
		const insecureMode = 0o777
		require.NoError(t, os.Chmod(dir, insecureMode))
	}
	if state != "valid" && state != "corrupt" {
		return
	}
	output, err := runCacheCheck(t, env, cliPath, "prewarm", "--cache-dir", dir, goldenFatBin)
	require.NoError(t, err, string(output))
	if state == "corrupt" {
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.NotEmpty(t, entries)
		for _, entry := range entries {
			if entry.Type().IsRegular() {
				require.NoError(t, os.WriteFile(filepath.Join(dir, entry.Name()), []byte("corrupt"), privateFilePerm))
			}
		}
	}
}
