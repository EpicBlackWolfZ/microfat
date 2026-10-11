//go:build linux

package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/require"
)

func TestCacheRequiresOwnerExecutePermission(t *testing.T) {
	t.Parallel()
	for _, mode := range []os.FileMode{0o600, 0o610, 0o601, 0o700, 0o755} {
		t.Run(mode.String(), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			require.NoError(t, os.Chmod(dir, 0o700))
			payload := []byte("verified executable payload")
			hash := sha256.Sum256(payload)
			entry := &format.VariantEntry{SHA256: hex.EncodeToString(hash[:]), UncompressedSize: int64(len(payload))}
			path := filepath.Join(dir, entry.SHA256)
			require.NoError(t, os.WriteFile(path, payload, mode))
			before, err := os.Stat(path)
			require.NoError(t, err)
			directory, err := os.Open(dir)
			require.NoError(t, err)
			defer func() { require.NoError(t, directory.Close()) }()
			for _, remove := range []bool{false, true} {
				for _, relative := range []bool{false, true} {
					var fd int
					if relative {
						fd, err = OpenAndValidateVariantAtFD(int(directory.Fd()), entry.SHA256, entry, remove)
					} else {
						fd, err = OpenAndValidateVariantFD(path, entry, remove)
					}
					if mode&0o100 == 0 {
						require.ErrorIs(t, err, ErrNotExecutable)
						require.Equal(t, -1, fd)
					} else {
						require.NoError(t, err)
						require.NoError(t, CloseFD(fd))
					}
				}
			}
			result := VerifyVariant(entry, dir)
			require.Equal(t, mode&0o100 != 0, result.Valid)
			after, err := os.Stat(path)
			require.NoError(t, err)
			require.True(t, os.SameFile(before, after))
			require.Equal(t, before.Mode(), after.Mode())
			require.Equal(t, before.ModTime(), after.ModTime())
		})
	}
}
