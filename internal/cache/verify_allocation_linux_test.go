//go:build linux && !race

package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Race instrumentation makes syscall buffers escape, so this checks the production build.
func TestVerifyCachedFDDoesNotAllocate(t *testing.T) {
	// AllocsPerRun requires serial execution.

	const chunks = 3
	const runs = 10
	payload := bytes.Repeat([]byte("v"), chunks*verifyBufferSize)
	path := filepath.Join(t.TempDir(), "payload")
	require.NoError(t, os.WriteFile(path, payload, 0o600))
	file, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	fd := int(file.Fd())
	verified := true
	allocations := testing.AllocsPerRun(runs, func() {
		verified = verifyCachedFD(fd, digest) && verified
	})
	require.True(t, verified)
	require.Zero(t, allocations, "descriptor verification must keep scratch and digest buffers on the stack")
}
