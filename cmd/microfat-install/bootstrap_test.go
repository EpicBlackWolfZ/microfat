package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mocks exercise the shell trust boundary, not cryptography. The real signed
// published fixtures are verified separately by installrelease's Cosign tests.
func TestBootstrapTrustBoundary(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"success", "arm64", "verifier-tamper", "downloaded-verifier-tamper", "helper-tamper",
		"signature", "missing-checksum", "duplicate-checksum", "invalid-checksum", "download-failure", "unsupported",
		"relative-stage", "writable-stage", "symlink-stage", "writable-verifier", "hardlink-verifier", "noexec-staging"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			bin := filepath.Join(root, "base-tools")
			require.NoError(t, os.Mkdir(bin, 0o755))
			for _, name := range []string{"mktemp", "sha256sum", "chmod", "rm", "stat"} {
				target, err := exec.LookPath(name)
				require.NoError(t, err)
				require.NoError(t, os.Symlink(target, filepath.Join(bin, name)))
			}
			write := func(name, data string) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(data), 0o755))
			}
			write("base-tools/uname", "#!/bin/bash\nif [[ $1 == -s ]]; then echo Linux; else echo \"${TEST_ARCH}\"; fi\n")
			write("base-tools/curl", `#!/bin/bash
set -eu
printf '%s\n' "$@" >> "${TEST_ROOT}/requests"
out=''
url=''
while (( $# > 0 )); do
 case "$1" in
 --output) out=$2; shift 2 ;;
 --max-redirs|--proto|--proto-redir|--connect-timeout|--max-time|--max-filesize) shift 2 ;;
 --*) shift ;;
 *) url=$1; shift ;;
 esac
done
[[ ${TEST_SCENARIO} != download-failure ]] || exit 22
case "${url}" in
 */checksums.txt) source=checksums ;;
 */checksums.txt.sig) source=signature ;;
 */cosign-linux-*) source=verifier ;;
 */microfat-install_0.3.0_linux_*) source=helper ;;
 *) exit 23 ;;
esac
/bin/cp "${TEST_ROOT}/${source}" "${out}"
`)
			verifier := "#!/bin/bash\nprintf '%s\\n' \"$@\" > \"${TEST_ROOT}/verified\"\n" +
				"[[ ${COSIGN_FAKE:-} == '' && ${SIGSTORE_FAKE:-} == '' ]] || exit 99\n" +
				"[[ ${TEST_SCENARIO} != signature ]]\n"
			helper := "#!/bin/bash\nprintf '%s\\n' \"$@\" > \"${TEST_ROOT}/installed\"\n"
			write("verifier", verifier)
			if scenario == "writable-verifier" {
				require.NoError(t, os.Chmod(filepath.Join(root, "verifier"), 0o777))
			}
			if scenario == "hardlink-verifier" {
				require.NoError(t, os.Link(filepath.Join(root, "verifier"), filepath.Join(root, "verifier-alias")))
			}
			write("helper", helper)
			write("signature", "fixture")
			arch := "amd64"
			uname := "x86_64"
			if scenario == "arm64" {
				arch, uname = "arm64", "aarch64"
			}
			if scenario == "unsupported" {
				uname = "riscv64"
			}
			checksum := fmt.Sprintf("%x  microfat-install_0.3.0_linux_%s\n", sha256.Sum256([]byte(helper)), arch)
			switch scenario {
			case "missing-checksum":
				checksum = strings.ReplaceAll(checksum, "microfat-install", "unrelated")
			case "duplicate-checksum":
				checksum += checksum
			case "invalid-checksum":
				checksum = "invalid\n"
			case "helper-tamper":
				write("helper", helper+"#modified\n")
			}
			write("checksums", checksum)
			pin := fmt.Sprintf("%x", sha256.Sum256([]byte(verifier)))
			if scenario == "verifier-tamper" {
				pin = strings.Repeat("0", 64)
			}
			stage := root
			if scenario == "noexec-staging" {
				stage = noexecStaging(t)
			}
			if scenario == "writable-stage" {
				require.NoError(t, os.Chmod(root, 0o777))
			}
			if scenario == "symlink-stage" {
				stage = filepath.Join(root, "link")
				require.NoError(t, os.Symlink(root, stage))
			}
			if scenario == "relative-stage" {
				stage = "relative"
			}
			args := []string{"../../scripts/install.sh", "--version", "0.2.5", "--staging-dir", stage,
				"--bin-dir", "/custom bin", "--store-dir", "/custom store"}
			if scenario != "downloaded-verifier-tamper" {
				args = append(args, "--cosign", filepath.Join(root, "verifier"), "--cosign-sha256", pin)
			}
			cmd := exec.CommandContext(t.Context(), "/bin/bash", args...)
			cmd.Env = []string{"PATH=" + bin, "TEST_ROOT=" + root, "TEST_ARCH=" + uname, "TEST_SCENARIO=" + scenario,
				"COSIGN_FAKE=do-not-inherit", "SIGSTORE_FAKE=do-not-inherit"}
			output, err := cmd.CombinedOutput()
			if scenario == "success" || scenario == "arm64" {
				require.NoError(t, err, "%s", output)
				installed, err := os.ReadFile(filepath.Join(root, "installed"))
				require.NoError(t, err)
				assert.Contains(t, string(installed), "--version\n0.2.5\n")
				assert.Contains(t, string(installed), "--bin-dir\n/custom bin\n")
				verified, err := os.ReadFile(filepath.Join(root, "verified"))
				require.NoError(t, err)
				assert.Contains(t, string(verified), "release.yml@refs/tags/v0.3.0")
				assert.Contains(t, string(verified), "https://token.actions.githubusercontent.com")
				requests, err := os.ReadFile(filepath.Join(root, "requests"))
				require.NoError(t, err)
				assert.Contains(t, string(requests), "--proto-redir\n=https\n")
				assert.Contains(t, string(requests), "--max-filesize\n1048576\n")
			} else {
				require.Error(t, err, "%s", output)
				if scenario == "noexec-staging" {
					assert.Contains(t, string(output), "staging must permit execution (use --staging-dir)")
				}
				_, err = os.Stat(filepath.Join(root, "installed"))
				require.ErrorIs(t, err, os.ErrNotExist, "untrusted helper must not execute")
			}
			if strings.Contains(scenario, "verifier-tamper") || scenario == "writable-verifier" || scenario == "hardlink-verifier" {
				_, err = os.Stat(filepath.Join(root, "verified"))
				require.ErrorIs(t, err, os.ErrNotExist, "untrusted verifier must not execute")
			}
			staging, err := filepath.Glob(filepath.Join(stage, "microfat-bootstrap.*"))
			require.NoError(t, err)
			assert.Empty(t, staging, "bootstrap cleans its private temporary directory")
		})
	}
}

// Use a real noexec mount when the host provides one; never change mount policy.
func noexecStaging(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/dev/shm", "microfat-noexec-*")
	if err != nil {
		t.Skip("no shared-memory fixture directory available")
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	probe := filepath.Join(dir, "probe")
	require.NoError(t, os.WriteFile(probe, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	err = exec.CommandContext(t.Context(), probe).Run()
	if !errors.Is(err, os.ErrPermission) {
		t.Skip("shared-memory fixture mount does not reject executable files")
	}
	return dir
}

func TestTruncatedBootstrapHasNoEffects(t *testing.T) {
	t.Parallel()
	script, err := os.ReadFile("../../scripts/install.sh")
	require.NoError(t, err)
	for _, offset := range []int{len(script) / 2, strings.LastIndex(string(script), "microfat_bootstrap \"$@\"")} {
		cmd := exec.CommandContext(t.Context(), "/bin/bash", "-s")
		cmd.Stdin = strings.NewReader(string(script[:offset]))
		cmd.Env = []string{"PATH=/nonexistent"}
		output, _ := cmd.CombinedOutput()
		assert.NotContains(t, string(output), "required tool not found", "partial retrieval cannot invoke the function")
	}
}
