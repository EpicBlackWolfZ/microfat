//go:build unix

package installrelease

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestVerifyDigest_FIFORejectedWithoutBlocking(t *testing.T) {
	t.Parallel()
	const limit = 1024
	fifo := filepath.Join(t.TempDir(), "fifo")
	require.NoError(t, unix.Mkfifo(fifo, 0o600))
	sum := sha256.Sum256([]byte("empty"))
	err := VerifyDigest(fifo, hex.EncodeToString(sum[:]), limit)
	require.ErrorContains(t, err, "bounded regular")
}
