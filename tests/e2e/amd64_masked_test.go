//go:build linux && amd64

package e2e_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func runMaskedCPU(t *testing.T, qemu, fat string, env []string) ([]byte, error) {
	t.Helper()
	const timeout = 15 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, qemu, "-cpu", "max,lahf-lm=off", fat)
	cmd.Env = append(os.Environ(), env...)
	output, err := cmd.CombinedOutput()
	require.NoError(t, ctx.Err(), "emulated dispatch timed out: %s", output)
	return output, err
}

// This is emulator evidence when qemu-user is installed; it does not claim to
// represent native hardware with LAHF/SAHF disabled.
func TestMaskedLAHFSAHFDispatch(t *testing.T) {
	t.Parallel()
	qemu, err := exec.LookPath("qemu-x86_64")
	if err != nil {
		t.Skip("qemu-user not installed; CPUID fixture contract runs in internal/microarch")
	}
	for _, profile := range []string{"full", "minimal"} {
		t.Run(profile, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			stub := stubPath
			if profile == "minimal" {
				stub = filepath.Join(dir, "minimal")
				require.NoError(t, compileBinaryWithFlags(stubPackagePath, stub, []string{"GOAMD64=v1"}, "-tags=minimal"))
			}
			fat := filepath.Join(dir, "fat")
			require.NoError(t, packBinary(cliPath, stub, "masked-cpuid", fat, goldenVariantBins))
			for _, mode := range []string{"memfd", "cache"} {
				output, err := runMaskedCPU(t, qemu, fat, []string{"MICROFAT_EXEC_MODE=" + mode, "MICROFAT_FORCE_LEVEL="})
				require.NoError(t, err, string(output))
				require.Contains(t, string(output), "golden:variant=v1")
				for _, force := range []string{"v2", "v3", "v4"} {
					cache := filepath.Join(dir, "unused-"+mode+force)
					output, err = runMaskedCPU(t, qemu, fat, []string{
						"MICROFAT_EXEC_MODE=" + mode, "MICROFAT_FORCE_LEVEL=" + force, "MICROFAT_CACHE_DIR=" + cache,
					})
					require.Error(t, err, string(output))
					require.Contains(t, string(output), "forced variant is incompatible")
					require.NoDirExists(t, cache, "forced rejection must precede extraction")
				}
			}
		})
	}
}
