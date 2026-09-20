//go:build unix

package inputfile

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestFIFOReplacement(t *testing.T) {
	const helperEnv = "MICROFAT_TEST_FIFO_REPLACEMENT"
	if os.Getenv(helperEnv) == "1" {
		dir := t.TempDir()
		path := filepath.Join(dir, "input")
		require.NoError(t, os.WriteFile(path, []byte("regular"), 0o600))
		_, err := openRegular(path, func(path string) (*os.File, error) {
			// Substitute after the caller selected a regular input but before the
			// actual open. A pathname stat followed by a blocking open hangs here.
			require.NoError(t, os.Remove(path))
			require.NoError(t, unix.Mkfifo(path, 0o600))
			return openNonblock(path)
		})
		require.ErrorIs(t, err, ErrNotRegular)
		return
	}
	t.Parallel()
	const timeout = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFIFOReplacement$")
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	output, err := cmd.CombinedOutput()
	require.NoError(t, ctx.Err(), "FIFO open blocked")
	require.NoError(t, err, string(output))
}
