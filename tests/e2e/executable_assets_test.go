package e2e_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDocumentedAssetResolution(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	native := filepath.Join(dir, "native")
	require.NoError(t, compileBinary("../../runtimeinit/testdata/assets_app", native, nil))
	fat := filepath.Join(dir, "fat")
	require.NoError(t, packBinary(cliPath, stubPath, "assets", fat, map[string]string{currentHostLevel: native}))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("deployment assets"), privateFilePerm))
	explicit := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(explicit, "config.yaml"), []byte("explicit assets"), privateFilePerm))
	for _, mode := range []string{"native", "memfd", "cache"} {
		for _, symlink := range []bool{false, true} {
			for _, assetDir := range []string{"", explicit} {
				t.Run(mode+"/"+filepath.Base(assetDir)+"/symlink="+strconv.FormatBool(symlink), func(t *testing.T) {
					path := fat
					if mode == "native" {
						path = native
					}
					if symlink {
						link := filepath.Join(t.TempDir(), "invoked-link")
						require.NoError(t, os.Symlink(path, link))
						path = link
					}
					const timeout = 10 * time.Second
					ctx, cancel := context.WithTimeout(context.Background(), timeout)
					defer cancel()
					cmd := exec.CommandContext(ctx, path)
					cmd.Env = append(os.Environ(), "MICROFAT_ORIGINAL_EXE=", "APP_ASSET_DIR="+assetDir, "MICROFAT_EXEC_MODE="+mode)
					output, err := cmd.CombinedOutput()
					require.NoError(t, ctx.Err())
					require.NoError(t, err, string(output))
					want := "deployment assets"
					if assetDir != "" {
						want = "explicit assets"
					}
					require.Equal(t, want, string(output))
				})
			}
		}
	}
}
