//go:build linux

package e2e_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPrivilegedLauncherRejection(t *testing.T) {
	for _, tool := range []string{"unshare", "setpriv", "setcap"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("privileged fixture unavailable: %s", tool)
		}
	}
	const timeout = 30 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	probe := exec.CommandContext(ctx, "unshare", "--user", "--map-auto", "--map-root-user", "setpriv",
		"--reuid=1", "--regid=1", "--clear-groups", "/usr/bin/true")
	if output, err := probe.CombinedOutput(); err != nil {
		t.Skipf("subordinate UID/GID mappings unavailable: %v: %s", err, output)
	}
	// This directory must remain traversable after dropping to namespace UID 1.
	dir, err := os.MkdirTemp("", "microfat-privilege- fixture-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	require.NoError(t, os.Chmod(dir, defaultFilePerm))
	minimal := filepath.Join(dir, "minimal-stub")
	output, err := exec.CommandContext(ctx, "go", "build", "-tags=minimal", "-o", minimal, stubPackagePath).CombinedOutput()
	require.NoError(t, err, string(output))
	level := "v1"
	if currentHostArch == archARM64 {
		level = "v8.0"
	}
	minimalFat := filepath.Join(dir, "minimal-fat")
	output, err = exec.CommandContext(ctx, cliPath, "pack", "--stub", minimal, "--arch", currentHostArch,
		"-v", level+"="+goldenVariantBins[level], "-o", minimalFat).CombinedOutput()
	require.NoError(t, err, string(output))
	for _, profile := range []struct{ name, path string }{{"full", goldenFatBin}, {"minimal", minimalFat}} {
		for _, mode := range []string{"ordinary-root", "setuid", "setgid", "capability"} {
			t.Run(profile.name+"/"+mode, func(t *testing.T) {
				target := filepath.Join(dir, profile.name+"-"+mode)
				script := `cp "$1" "$2"
chmod 755 "$2"
case "$3" in
 setuid) chmod u+s "$2" ;;
 setgid) chmod g+s "$2" ;;
 capability) setcap cap_net_bind_service=ep "$2" ;;
 ordinary-root) exec "$2" ;;
esac
exec setpriv --reuid=1 --regid=1 --clear-groups "$2" --microfat-info`
				cmd := exec.CommandContext(ctx, "unshare", "--user", "--map-auto", "--map-root-user", "sh", "-eu", "-c",
					script, "fixture", profile.path, target, mode)
				cmd.Env = append(os.Environ(), "MICROFAT_EXEC_MODE=memfd")
				output, err := cmd.CombinedOutput()
				require.NoError(t, ctx.Err(), "privileged fixture timed out")
				if mode == "ordinary-root" {
					require.NoError(t, err, string(output))
					return
				}
				if mode == "capability" && strings.Contains(string(output), "setcap:") {
					t.Skipf("filesystem capability fixture unavailable: %s", output)
				}
				require.Error(t, err, string(output))
				require.Contains(t, string(output), "privilege-bearing launcher execution is unsupported")
			})
		}
	}
}
