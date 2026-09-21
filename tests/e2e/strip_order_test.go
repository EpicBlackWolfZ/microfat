package e2e_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func runStripCommand(t *testing.T, path string, args ...string) ([]byte, error) {
	t.Helper()
	const timeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	require.NoError(t, ctx.Err(), "command timed out: %s", output)
	return output, err
}

func TestStripBeforePacking(t *testing.T) {
	t.Parallel()
	strip, err := exec.LookPath("strip")
	if err != nil {
		t.Skip("GNU strip is not installed")
	}
	version, err := runStripCommand(t, strip, "--version")
	require.NoError(t, err, string(version))
	if !bytes.Contains(version, []byte("GNU strip")) {
		t.Skip("this regression specifically covers GNU strip")
	}
	for _, profile := range []string{launcherFullProfile, launcherMinimalProfile} {
		t.Run(profile, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			stub := filepath.Join(dir, "stub")
			if profile == launcherMinimalProfile {
				require.NoError(t, compileBinaryWithFlags(stubPackagePath, stub, []string{"GOAMD64=v1"}, "-tags=minimal"))
			} else {
				data, err := os.ReadFile(stubPath)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(stub, data, defaultFilePerm))
			}
			payload := filepath.Join(dir, "payload")
			data, err := os.ReadFile(goldenVariantBins[currentHostLevel])
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(payload, data, defaultFilePerm))
			output, err := runStripCommand(t, strip, stub, payload)
			require.NoError(t, err, string(output))
			fat := filepath.Join(dir, "fat")
			require.NoError(t, packBinary(cliPath, stub, "safe-strip-order", fat, map[string]string{currentHostLevel: payload}))
			output, err = runStripCommand(t, cliPath, inputVerifyCommand, fat)
			require.NoError(t, err, string(output))
			output, err = runStripCommand(t, fat)
			require.NoError(t, err, string(output))
			require.Contains(t, string(output), "golden:variant=")
			// Rewrite a fresh copy, avoiding transient executable references from
			// the preceding kernel exec lifetime on the validated original.
			packed, err := os.ReadFile(fat)
			require.NoError(t, err)
			stripped := fat + ".stripped"
			require.NoError(t, os.WriteFile(stripped, packed, defaultFilePerm))
			output, err = runStripCommand(t, strip, stripped)
			require.NoError(t, err, string(output))
			output, err = runStripCommand(t, stripped)
			require.Error(t, err, string(output))
			require.Contains(t, string(output), "missing magic trailer")
			require.Contains(t, string(output), "post-pack ELF rewriting")
			require.Contains(t, string(output), "obtain or rebuild a complete artifact")
		})
	}
}
