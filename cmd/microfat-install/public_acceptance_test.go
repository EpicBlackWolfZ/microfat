package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const publicAcceptanceARM64 = "arm64"

// Public acceptance must reject mismatched identities and unsafe output paths
// before it builds an image or creates a container. Real public bootstrap
// acceptance is separately performed on native hosted runners after publication.
func TestPublicAcceptanceRejectsBeforeDocker(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 || runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != publicAcceptanceARM64) {
		t.Skip("public installer qualification requires an ordinary native Linux user")
	}
	_, err := exec.LookPath("jq")
	require.NoError(t, err, "jq is required for the public qualification trust boundary")
	for _, scenario := range []struct {
		name, message string
	}{
		{"prerelease", "exact stable tag/source required"},
		{"short-source", "exact stable tag/source required"},
		{"foreign-architecture", "native Linux architecture mismatch"},
		{"source-mismatch", "authentication evidence does not match"},
		{"missing-product", "authentication evidence does not match"},
		{"invalid-helper-digest", "authentication evidence does not match"},
		{"symlink-evidence", "regular absolute authentication inputs required"},
		{"symlink-bootstrap", "regular absolute authentication inputs required"},
		{"existing-output", "output must be a new absolute directory"},
		{"unclean-output", "output must be a clean absolute path"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			require.NoError(t, os.Mkdir(bin, 0o755))
			marker := filepath.Join(root, "docker-invoked")
			require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte(
				"#!/bin/bash\nprintf invoked > \"${DOCKER_MARKER}\"\nexit 99\n"), 0o755))
			tag, source, arch := "v0.3.0", strings.Repeat("a", 40), runtime.GOARCH
			helper := "microfat-install_0.3.0_linux_" + arch
			digest := strings.Repeat("b", 64)
			products := map[string]string{"microfat": digest, "microfat-stub": digest, "microfat-stub-minimal": digest}
			assets := map[string]string{helper: digest}
			evidence := map[string]any{"tag": tag, "source": source, "release_id": 123,
				"checksums_sha256": digest, "assets": assets, "products": map[string]any{arch: products}}
			verification := filepath.Join(root, "verification.json")
			bootstrap := filepath.Join(root, "install.sh")
			output := filepath.Join(root, "output")
			switch scenario.name {
			case "prerelease":
				tag = "v0.3.0-rc.1"
			case "short-source":
				source = source[:7]
			case "foreign-architecture":
				arch = publicAcceptanceARM64
				if runtime.GOARCH == publicAcceptanceARM64 {
					arch = "amd64"
				}
			case "source-mismatch":
				evidence["source"] = strings.Repeat("c", 40)
			case "missing-product":
				delete(products, "microfat-stub-minimal")
			case "invalid-helper-digest":
				assets[helper] = "invalid"
			case "existing-output":
				require.NoError(t, os.Mkdir(output, 0o700))
			case "unclean-output":
				output = root + "/./output"
			}
			data, err := json.Marshal(evidence)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(verification, data, 0o600))
			require.NoError(t, os.WriteFile(bootstrap, []byte("#!/bin/bash\nexit 99\n"), 0o600))
			if scenario.name == "symlink-evidence" {
				link := filepath.Join(root, "evidence-link")
				require.NoError(t, os.Symlink(verification, link))
				verification = link
			}
			if scenario.name == "symlink-bootstrap" {
				link := filepath.Join(root, "bootstrap-link")
				require.NoError(t, os.Symlink(bootstrap, link))
				bootstrap = link
			}
			cmd := exec.CommandContext(t.Context(), "/bin/bash", "../../scripts/qualify-public-installer.sh",
				tag, source, arch, verification, bootstrap, output)
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "DOCKER_MARKER="+marker)
			result, err := cmd.CombinedOutput()
			require.Error(t, err, "%s", result)
			assert.Contains(t, string(result), scenario.message)
			_, err = os.Stat(marker)
			require.ErrorIs(t, err, os.ErrNotExist, "invalid qualification input must not invoke Docker")
			if scenario.name != "existing-output" && scenario.name != "unclean-output" {
				_, err = os.Stat(output)
				require.ErrorIs(t, err, os.ErrNotExist, "invalid qualification input must not create output")
			}
		})
	}
}
