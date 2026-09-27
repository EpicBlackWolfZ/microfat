package installrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pinnedScript(t *testing.T, body string) Cosign {
	t.Helper()
	path := filepath.Join(t.TempDir(), "verifier")
	data := []byte("#!/bin/sh\n" + body + "\n")
	require.NoError(t, os.WriteFile(path, data, 0o755))
	hash := sha256.Sum256(data)
	return Cosign{Path: path, SHA256: hex.EncodeToString(hash[:])}
}

func TestVerifierPinBeforeExecution(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"valid", "wrong-hash", "bad-hash", "relative", "missing", fixtureSymlink,
		"not-executable", "writable-file", "writable-parent", fixtureHardlink, "exit", "overflow", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			marker := filepath.Join(t.TempDir(), "ran")
			verifier := pinnedScript(t, fmt.Sprintf("printf 'ran' > %q\nexit 0", marker))
			ctx := t.Context()
			switch scenario {
			case "wrong-hash":
				verifier.SHA256 = strings.Repeat("0", 64)
			case "bad-hash":
				verifier.SHA256 = "invalid"
			case "relative":
				verifier.Path = "verifier"
			case "missing":
				verifier.Path += "-missing"
			case fixtureSymlink:
				link := verifier.Path + "-link"
				require.NoError(t, os.Symlink(verifier.Path, link))
				verifier.Path = link
			case "not-executable":
				require.NoError(t, os.Chmod(verifier.Path, 0o600))
			case "writable-file":
				require.NoError(t, os.Chmod(verifier.Path, 0o777))
			case "writable-parent":
				require.NoError(t, os.Chmod(filepath.Dir(verifier.Path), 0o777))
			case fixtureHardlink:
				require.NoError(t, os.Link(verifier.Path, verifier.Path+"-alias"))
			case "exit":
				verifier = pinnedScript(t, "exit 1")
			case "overflow":
				verifier = pinnedScript(t, "head -c 70000 /dev/zero")
			case "timeout":
				verifier = pinnedScript(t, "exec sleep 10")
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(t.Context(), 50*time.Millisecond)
				defer cancel()
			}
			err := verifier.Verify(ctx, "0.3.0", "checksums", "bundle")
			if scenario == "valid" {
				require.NoError(t, err)
				assert.FileExists(t, marker)
				return
			}
			require.Error(t, err)
			assert.NoFileExists(t, marker)
		})
	}
}

func TestVerifierArgumentsAndEnvironment(t *testing.T) {
	t.Setenv("COSIGN_PUBLIC_KEY", "untrusted")
	t.Setenv("SIGSTORE_ROOT_FILE", "untrusted")
	marker := filepath.Join(t.TempDir(), "args")
	script := "test -z \"${COSIGN_PUBLIC_KEY:-}\" && test -z \"${SIGSTORE_ROOT_FILE:-}\" || exit 1\n" +
		fmt.Sprintf("printf '%%s\\n' \"$@\" > %q", marker)
	verifier := pinnedScript(t, script)
	require.NoError(t, verifier.Verify(t.Context(), "0.3.0", "checksums", "bundle"))
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	assert.Equal(t, []string{"verify-blob", "--bundle", "bundle", "--certificate-identity", Identity("0.3.0"),
		"--certificate-oidc-issuer", Issuer, "checksums", ""}, strings.Split(string(data), "\n"))
	require.Error(t, verifier.Verify(t.Context(), "invalid", "checksums", "bundle"))
}

func TestDigestBounds(t *testing.T) {
	t.Parallel()
	verifier := pinnedScript(t, "exit 0")
	require.ErrorContains(t, VerifyDigest(verifier.Path, verifier.SHA256, 1), "bounded regular")
	require.Error(t, VerifyDigest(filepath.Dir(verifier.Path), verifier.SHA256, maxDownloadBytes))
	buffer := &boundedOutput{}
	n, err := buffer.Write([]byte("ok"))
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, "ok", buffer.String())
}

func TestCosignPublishedSignature(t *testing.T) {
	if os.Getenv("MICROFAT_SIGNATURE_TESTS") != "required" {
		t.Skip("real Cosign fixture is required in signature qualification")
	}
	path, err := exec.LookPath("cosign")
	require.NoError(t, err)
	path, err = filepath.Abs(path)
	require.NoError(t, err)
	path, err = filepath.EvalSymlinks(path)
	require.NoError(t, err)
	// The test trusts the independently installed host verifier, like the existing
	// published-release signature suite. Production bootstrap uses reviewed fixed pins.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	hash := sha256.Sum256(data)
	verifier := Cosign{Path: path, SHA256: hex.EncodeToString(hash[:])}
	fixture := filepath.Join("..", "..", "tests", "e2e", "testdata", "release-trust")
	checksums, bundle := filepath.Join(fixture, "checksums.txt"), filepath.Join(fixture, "checksums.txt.sig")
	require.NoError(t, verifier.Verify(t.Context(), "0.2.4", checksums, bundle))
	require.Error(t, verifier.Verify(t.Context(), "0.2.3", checksums, bundle), "wrong exact tag must fail")
	modified := filepath.Join(t.TempDir(), "checksums.txt")
	data, err = os.ReadFile(checksums)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(modified, append(data, 'x'), fileMode))
	require.Error(t, verifier.Verify(t.Context(), "0.2.4", modified, bundle))
}
