package e2e

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstallDirectoryElevation(t *testing.T) {
	t.Parallel()
	const version, arch = "0.2.3", "amd64"
	archiveName := "microfat_" + version + "_linux_" + arch + ".tar.gz"
	archive := createValidReleaseTarGz(t, map[string]string{
		"microfat": "cli", "microfat-stub": "stub", "microfat-stub-minimal": "minimal",
	})
	server := setupInstallTestServer(t, archiveName, archive, fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), archiveName), "mock-signature")
	defer server.Close()
	for _, scenario := range []string{"missing system destination", "writable destination", "sudo failure", "custom runner"} {
		t.Run(scenario, func(t *testing.T) {
			binDir, logPath := setupInstallMocks(t, true)
			dest := filepath.Join(t.TempDir(), "new destination")
			if scenario == "writable destination" || scenario == "custom runner" {
				require.NoError(t, os.Mkdir(dest, 0o700))
			}
			writeMock := func(name, body string) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\n"+body), 0o755))
			}
			writeMock("id", "printf '1000\\n'\n")
			writeMock("mkdir", fmt.Sprintf(`if [ "$2" = %q ] && [ ! -d "$2" ] && [ "${MOCK_SUDO:-}" != 1 ]; then
    echo 'plain mkdir denied' >> %q
    exit 1
fi
exec /bin/mkdir "$@"
`, dest, logPath))
			sudo := fmt.Sprintf("echo \"sudo $*\" >> %q\n", logPath)
			if scenario == "sudo failure" {
				sudo += "exit 1\n"
			} else {
				sudo += "export MOCK_SUDO=1\nexec \"$@\"\n"
			}
			writeMock("sudo", sudo)
			writeMock("install", fmt.Sprintf("echo \"install $*\" >> %q\n", logPath))
			runner := ""
			if scenario == "custom runner" {
				runner = filepath.Join(binDir, "mock-install") + " --custom-option"
			}
			code, _, stderr := runInstallReleaseScript(t, binDir, runner, server.URL, version, arch, dest)
			calls := readInstallCalls(logPath)
			if scenario == "sudo failure" {
				require.NotZero(t, code, stderr)
				require.Len(t, calls, 2)
				require.Contains(t, calls[1], "sudo mkdir -p "+dest)
				return
			}
			require.Zero(t, code, stderr)
			if scenario == "missing system destination" {
				require.Len(t, calls, 8)
				require.Equal(t, "plain mkdir denied", calls[0])
				require.Equal(t, "sudo mkdir -p "+dest, calls[1])
				require.Equal(t, 3, strings.Count(strings.Join(calls, "\n"), "sudo install -m 0755"))
			} else {
				require.Len(t, calls, 3)
				require.NotContains(t, strings.Join(calls, "\n"), "sudo")
				if scenario == "custom runner" {
					require.True(t, strings.HasPrefix(calls[0], "--custom-option -m 0755"))
				}
			}
		})
	}
}
