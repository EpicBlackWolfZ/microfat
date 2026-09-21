package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Test actual cryptographic verification separately from mocked installer flow.
func TestPublishedReleaseSignatureContract(t *testing.T) {
	cosign, err := exec.LookPath("cosign")
	if err != nil {
		if os.Getenv("MICROFAT_SIGNATURE_TESTS") == "required" {
			t.Fatal("Cosign required for release signature contract tests")
		}
		t.Skip("independently installed Cosign unavailable; mandatory in release CI")
	}
	const identity = "https://github.com/EpicBlackWolfZ/microfat/.github/workflows/release.yml@refs/tags/v0.2.4"
	const issuer = "https://token.actions.githubusercontent.com"
	const source = "9607f3a0bb5954f37e8775351d76cee3398574a8"
	fixture := filepath.Join("testdata", "release-trust")
	checksum := filepath.Join(fixture, "checksums.txt")
	bundle := filepath.Join(fixture, "checksums.txt.sig")
	root := t.TempDir()
	modifiedChecksum := filepath.Join(root, "modified-checksums.txt")
	data, err := os.ReadFile(checksum)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(modifiedChecksum, append(data, 'x'), 0o600))
	modifiedBundle := filepath.Join(root, "modified-bundle.json")
	require.NoError(t, os.WriteFile(modifiedBundle, []byte(`{"messageSignature":{"signature":"AAAA"}}`), 0o600))
	wrongKey := createUnrelatedPublicKey(t, root)
	for _, tc := range []struct {
		name, identity, issuer, source, checksum, bundle, key string
		valid                                                 bool
	}{
		{"valid", identity, issuer, source, checksum, bundle, "", true},
		{"wrong workflow", strings.Replace(identity, "release.yml", "release-sbom-repair.yml", 1), issuer, source, checksum, bundle, "", false},
		{"wrong version", strings.Replace(identity, "v0.2.4", "v0.2.3", 1), issuer, source, checksum, bundle, "", false},
		{"wrong issuer", identity, "https://untrusted.invalid", source, checksum, bundle, "", false},
		{"wrong source", identity, issuer, strings.Repeat("0", 40), checksum, bundle, "", false},
		{"wrong key", identity, issuer, source, checksum, bundle, wrongKey, false},
		{"modified checksum", identity, issuer, source, modifiedChecksum, bundle, "", false},
		{"missing checksum", identity, issuer, source, filepath.Join(root, "missing"), bundle, "", false},
		{"modified signature", identity, issuer, source, checksum, modifiedBundle, "", false},
		{"missing signature", identity, issuer, source, checksum, filepath.Join(root, "missing"), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const timeout = 45 * time.Second
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			args := []string{"verify-blob", "--bundle", tc.bundle, "--certificate-identity", tc.identity,
				"--certificate-oidc-issuer", tc.issuer, "--certificate-github-workflow-sha", tc.source}
			if tc.key != "" {
				args = append(args, "--key", tc.key)
			}
			cmd := exec.CommandContext(ctx, cosign, append(args, tc.checksum)...)
			out, err := cmd.CombinedOutput()
			require.NoError(t, ctx.Err(), "verification timed out: %s", out)
			if tc.valid {
				require.NoError(t, err, string(out))
			} else {
				require.Error(t, err, string(out))
			}
		})
	}
}

func createUnrelatedPublicKey(t *testing.T, root string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	path := filepath.Join(root, "unrelated.pub")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600))
	return path
}
