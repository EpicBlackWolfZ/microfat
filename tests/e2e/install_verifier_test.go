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

func TestInstallerVerifierPin(t *testing.T) {
	const version, arch = "0.2.4", "amd64"
	archiveName := "microfat_" + version + "_linux_" + arch + ".tar.gz"
	archive := createValidReleaseTarGz(t, map[string]string{
		"microfat": "cli", "microfat-stub": "stub", "microfat-stub-minimal": "minimal",
	})
	checksums := fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), archiveName)
	server := setupInstallTestServer(t, archiveName, archive, checksums, "signature-fixture")
	defer server.Close()
	for _, name := range []string{"trusted", "mismatch", "missing digest", "missing path", "relative path", "symlink", "malformed digest"} {
		t.Run(name, func(t *testing.T) {
			binDir, installLog := setupInstallMocks(t, true)
			path := filepath.Join(binDir, "cosign")
			verifyLog := filepath.Join(t.TempDir(), "verify.log")
			contents := []byte(fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\n", verifyLog))
			require.NoError(t, os.WriteFile(path, contents, 0o755))
			digest := fmt.Sprintf("%x", sha256.Sum256(contents))
			switch name {
			case "mismatch":
				digest = strings.Repeat("0", 64)
			case "missing digest":
				digest = ""
			case "missing path":
				path = ""
			case "relative path":
				path = "cosign"
			case "symlink":
				link := filepath.Join(binDir, "linked-cosign")
				require.NoError(t, os.Symlink(path, link))
				path = link
			case "malformed digest":
				digest = "invalid"
			}
			t.Setenv("MICROFAT_COSIGN", path)
			t.Setenv("MICROFAT_COSIGN_SHA256", digest)
			code, _, stderr := runInstallReleaseScript(t, binDir, filepath.Join(binDir, "mock-install"),
				server.URL, version, arch, t.TempDir())
			if name != "trusted" {
				require.NotZero(t, code, stderr)
				require.Empty(t, readInstallCalls(installLog))
				require.NoFileExists(t, verifyLog, "untrusted verifier must not execute")
				return
			}
			require.Zero(t, code, stderr)
			require.Len(t, readInstallCalls(installLog), 3)
			arguments, err := os.ReadFile(verifyLog)
			require.NoError(t, err)
			lines := strings.Split(strings.TrimSpace(string(arguments)), "\n")
			require.Len(t, lines, 8)
			require.Equal(t, "verify-blob", lines[0])
			require.Equal(t, "--bundle", lines[1])
			require.Equal(t, "--certificate-identity", lines[3])
			require.Equal(t, "https://github.com/EpicBlackWolfZ/microfat/.github/workflows/release.yml@refs/tags/v"+version, lines[4])
			require.Equal(t, "--certificate-oidc-issuer", lines[5])
			require.Equal(t, "https://token.actions.githubusercontent.com", lines[6])
			require.Equal(t, "checksums.txt.sig", filepath.Base(lines[2]))
			require.Equal(t, "checksums.txt", filepath.Base(lines[7]))
		})
	}
}
