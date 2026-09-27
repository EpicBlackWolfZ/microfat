package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// Trust, extraction and transactional regressions now live with the native
// installer. Keep the legacy entrypoint's migration and refusal behavior here;
// TestPublishedReleaseSignatureContract retains actual cryptographic negatives.
func TestLegacyInstallerDelegation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	script, err := os.ReadFile("../../scripts/install-release.sh")
	require.NoError(t, err)
	shim := filepath.Join(root, "install-release.sh")
	require.NoError(t, os.WriteFile(shim, script, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "install.sh"), []byte("#!/bin/bash\nprintf '%s\\n' \"$@\"\n"), 0o755))
	for _, scenario := range []string{"default", "custom", "system-explicit", "verifier", "url-override", "runner-override",
		"version", "arch", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			args := []string{shim, "v0.2.5", runtime.GOARCH}
			env := []string{"PATH=" + os.Getenv("PATH")}
			switch scenario {
			case "custom":
				args = append(args, "/custom bin", "--store-dir", "/custom store")
			case "system-explicit":
				args = append(args, "/usr/local/bin", "--system", "--store-dir", "/var/lib/microfat")
			case "verifier":
				env = append(env, "MICROFAT_COSIGN=/trusted/cosign", "MICROFAT_COSIGN_SHA256=independent-pin")
			case "url-override":
				env = append(env, "MICROFAT_RELEASE_URL=https://untrusted.invalid")
			case "runner-override":
				env = append(env, "INSTALL_RUNNER=custom")
			case "version":
				args[1] = "0.3.0-rc.1"
			case "arch":
				args[2] = "unsupported"
			case "missing":
				args = []string{shim}
			}
			cmd := exec.CommandContext(t.Context(), "bash", args...)
			cmd.Env = env
			out, err := cmd.CombinedOutput()
			switch scenario {
			case "default", "custom", "system-explicit", "verifier":
				require.NoError(t, err, "%s", out)
				require.Contains(t, string(out), "--version\n0.2.5\n")
				if scenario == "default" {
					require.NotContains(t, string(out), "--bin-dir")
				}
				if scenario == "custom" {
					require.Contains(t, string(out), "--bin-dir\n/custom bin\n--store-dir\n/custom store")
				}
				if scenario == "system-explicit" {
					require.Contains(t, string(out), "--system\n--store-dir\n/var/lib/microfat")
				}
				if scenario == "verifier" {
					require.Contains(t, string(out), "--cosign\n/trusted/cosign\n--cosign-sha256\nindependent-pin")
				}
			default:
				require.Error(t, err, "%s", out)
				require.NotContains(t, string(out), "--version\n", "must reject before delegating")
			}
		})
	}
}
