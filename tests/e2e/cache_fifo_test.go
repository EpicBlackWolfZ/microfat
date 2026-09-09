//go:build linux

package e2e_test

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

func TestCacheFIFOCommandsTerminate(t *testing.T) {
	t.Parallel()
	_, index := readTrailerAndIndex(t, goldenFatBin)
	entry, ok := index.FindVariant(currentHostLevel)
	require.True(t, ok)
	for _, command := range []string{"execute", "prewarm", "verify"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			require.NoError(t, os.Chmod(dir, privateDirPerm))
			fifo := filepath.Join(dir, entry.SHA256)
			require.NoError(t, unix.Mkfifo(fifo, uint32(privateFilePerm)))
			const timeout = 5 * time.Second
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			var cmd *exec.Cmd
			switch command {
			case "execute":
				cmd = exec.CommandContext(ctx, goldenFatBin)
			case "prewarm":
				cmd = exec.CommandContext(ctx, cliPath, "prewarm", goldenFatBin, "--cache-dir", dir)
			default:
				cmd = exec.CommandContext(ctx, cliPath, "prewarm", goldenFatBin, "--cache-dir", dir, "--verify")
			}
			cmd.Env = append(os.Environ(), "MICROFAT_CACHE_DIR="+dir, envExecCache)
			output, err := cmd.CombinedOutput()
			require.NoError(t, ctx.Err(), "FIFO blocked %s", command)
			require.Error(t, err, string(output))
			stat, err := os.Lstat(fifo)
			require.NoError(t, err)
			require.NotZero(t, stat.Mode()&os.ModeNamedPipe, "FIFO was replaced")
		})
	}
}
