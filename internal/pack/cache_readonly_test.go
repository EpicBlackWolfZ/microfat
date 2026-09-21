package pack

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/require"
)

func TestImplicitCacheVerificationDoesNotMutate(t *testing.T) {
	root := t.TempDir()
	stub, payload, fat := filepath.Join(root, "stub"), filepath.Join(root, "payload"), filepath.Join(root, "fat")
	require.NoError(t, os.WriteFile(stub, []byte("stub"), 0o700))
	require.NoError(t, os.WriteFile(payload, []byte("payload"), 0o700))
	idx, err := Pack(Options{
		StubPath: stub, OutputPath: fat, Variants: map[string]string{"v1": payload}, SkipELFValidation: true,
	})
	require.NoError(t, err)
	f, err := os.Open(fat)
	require.NoError(t, err)
	defer f.Close()
	stat, err := f.Stat()
	require.NoError(t, err)
	for _, route := range []string{"env", "xdg"} {
		for _, state := range []string{"absent", "insecure", "valid", "corrupt"} {
			t.Run(route+"/"+state, func(t *testing.T) {
				root := t.TempDir()
				dir := filepath.Join(root, "microfat")
				t.Setenv(format.EnvCacheDir, "")
				t.Setenv("XDG_CACHE_HOME", root)
				t.Setenv("TMPDIR", root)
				if route == "env" {
					t.Setenv(format.EnvCacheDir, dir)
				}
				var before []byte
				var mode os.FileMode
				if state != "absent" {
					_, _, err := PrewarmBinary(f, stat.Size(), nil, dir)
					require.NoError(t, err)
					if state == "insecure" {
						const permissiveMode = 0o777
						require.NoError(t, os.Chmod(dir, permissiveMode))
					}
					if state == "corrupt" {
						require.NoError(t, os.WriteFile(filepath.Join(dir, idx.Variants[0].SHA256), []byte("bad"), 0o700))
					}
					info, err := os.Stat(dir)
					require.NoError(t, err)
					mode = info.Mode()
					before, err = os.ReadFile(filepath.Join(dir, idx.Variants[0].SHA256))
					require.NoError(t, err)
				}
				_, results, verifyErr := VerifyCacheBinary(f, stat.Size(), nil, "")
				result := VerifyCacheVariant(&idx.Variants[0], "")
				require.Equal(t, state == "valid", result.Valid)
				if state == "absent" || state == "insecure" {
					require.Error(t, verifyErr)
				} else {
					require.NoError(t, verifyErr)
					require.Len(t, results, 1)
					require.Equal(t, state == "valid", results[0].Valid)
				}
				if state == "absent" {
					require.NoDirExists(t, dir)
					entries, err := os.ReadDir(root)
					require.NoError(t, err)
					require.Empty(t, entries)
					return
				}
				info, err := os.Stat(dir)
				require.NoError(t, err)
				require.Equal(t, mode, info.Mode())
				after, err := os.ReadFile(filepath.Join(dir, idx.Variants[0].SHA256))
				require.NoError(t, err)
				require.Equal(t, before, after)
				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				require.Len(t, entries, 1)
			})
		}
	}
}
