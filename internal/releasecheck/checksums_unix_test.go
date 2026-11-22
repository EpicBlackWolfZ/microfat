//go:build unix

package releasecheck_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestValidateChecksums_FIFORejectedWithoutBlocking(t *testing.T) {
	t.Parallel()

	t.Run("checksums.txt is a FIFO", func(t *testing.T) {
		t.Parallel()
		distDir := t.TempDir()
		contract, err := releasecheck.NewReleaseContract("0.2.3")
		require.NoError(t, err)

		require.NoError(t, unix.Mkfifo(filepath.Join(distDir, "checksums.txt"), 0o600))
		_, err = releasecheck.ValidateChecksums(distDir, contract)
		require.ErrorContains(t, err, "not a regular file")
	})

	t.Run("disk artifact is a FIFO", func(t *testing.T) {
		t.Parallel()
		distDir, contract, digests := createValidDistFixture(t, "0.2.3")

		var targetName string
		for n := range digests {
			targetName = n
			break
		}
		targetPath := filepath.Join(distDir, targetName)
		require.NoError(t, os.Remove(targetPath))
		require.NoError(t, unix.Mkfifo(targetPath, 0o600))

		_, err := releasecheck.ValidateChecksums(distDir, contract)
		require.ErrorContains(t, err, "symlinks forbidden")
	})
}
