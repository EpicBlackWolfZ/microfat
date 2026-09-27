package installrelease

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalReleaseUsesIdenticalTrustPolicy(t *testing.T) {
	t.Parallel()
	responses := releaseResponses(archiveFixture(t, "amd64", nil))
	for _, scenario := range []string{"success", "signature", "checksum", "archive", "symlink", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			directory, staging := t.TempDir(), t.TempDir()
			for address, data := range responses {
				if address == apiURL {
					continue
				}
				require.NoError(t, os.WriteFile(filepath.Join(directory, filepath.Base(address)), data, 0o600))
			}
			checksum := filepath.Join(directory, "checksums.txt")
			switch scenario {
			case "checksum":
				require.NoError(t, os.WriteFile(checksum, []byte("invalid"), 0o600))
			case "archive":
				require.NoError(t, os.WriteFile(filepath.Join(directory, "microfat_0.3.0_linux_amd64.tar.gz"), []byte("tampered"), 0o600))
			case "symlink":
				require.NoError(t, os.Rename(checksum, checksum+"-old"))
				require.NoError(t, os.Symlink(checksum+"-old", checksum))
			case "missing":
				require.NoError(t, os.Remove(checksum))
			}
			client, err := NewLocalClient(directory)
			require.NoError(t, err)
			_, err = client.Resolve(t.Context(), "")
			require.Error(t, err, "local assets cannot select latest from an unsigned directory")
			version, err := client.Resolve(t.Context(), "v0.3.0")
			require.NoError(t, err)
			verified := false
			verifier := verifierFunc(func(_ context.Context, selected, _, _ string) error {
				assert.Equal(t, "0.3.0", selected)
				if scenario == "signature" {
					return errors.New("untrusted signature")
				}
				verified = true
				return nil
			})
			generation, products, err := client.Acquire(t.Context(), version, "amd64", staging, verifier)
			if scenario == "success" {
				require.NoError(t, err)
				assert.True(t, verified)
				require.NoError(t, generation.Validate())
				assert.FileExists(t, filepath.Join(products, "microfat"))
			} else {
				require.Error(t, err)
				assert.NoDirExists(t, filepath.Join(staging, "products"))
			}
		})
	}
}

func TestLocalAssetBounds(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"", "relative", "/unclean/../path"} {
		_, err := NewLocalClient(path)
		require.Error(t, err)
	}
	path := filepath.Join(t.TempDir(), "asset")
	require.NoError(t, os.WriteFile(path, []byte("bounded"), 0o600))
	require.Error(t, copyLocal(t.Context(), path, 1, io.Discard))
	require.Error(t, copyLocal(t.Context(), filepath.Dir(path), maxMetadataBytes, io.Discard))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, copyLocal(ctx, path, maxMetadataBytes, io.Discard), context.Canceled)
	var output strings.Builder
	require.NoError(t, copyLocal(t.Context(), path, maxMetadataBytes, &output))
	assert.Equal(t, "bounded", output.String())
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	require.Error(t, copyLocal(t.Context(), path, maxMetadataBytes, io.Discard))
}
