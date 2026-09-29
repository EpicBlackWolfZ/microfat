package homebrew

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/internal/installrelease"
	"github.com/EpicBlackWolfZ/microfat/internal/releaseaudit"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicCancellationAndInvalidInputs(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Discover(ctx)
	require.Error(t, err)
	_, err = Prepare(ctx, "v0.3.0", filepath.Join(t.TempDir(), "unused"))
	require.Error(t, err)
	_, err = authenticate(ctx, t.TempDir(), "0.3.0", strings.Repeat("a", 40))
	require.Error(t, err)
	_, err = validateProducts(t.TempDir(), "0.3.0")
	require.Error(t, err)
	require.Error(t, Check(ctx, filepath.Join(t.TempDir(), "absent")))
	require.Error(t, metadata(t, "v0.2.5").validate("v0.2.5"))
	_, err = requiredAssets(release{}, "")
	require.Error(t, err)
}

func TestPinnedAuthenticationBoundary(t *testing.T) {
	// Ambient verifier variables must not override the repository's fixed policy.
	t.Setenv("COSIGN_REPOSITORY", "untrusted")
	t.Setenv("SIGSTORE_ROOT_FILE", "untrusted")
	for _, failure := range []string{"", "staging", "pin", "signature", "inventory"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			dist := filepath.Join(root, "dist")
			require.NoError(t, os.Mkdir(dist, privateMode))
			contract, err := releasecheck.NewReleaseContract("0.2.4")
			require.NoError(t, err)
			var checksums strings.Builder
			for name := range contract.ExpectedPayloadNames {
				data := []byte("archive fixture")
				if strings.HasSuffix(name, ".spdx.json") {
					data = []byte(`{"spdxVersion":"SPDX-2.3"}`)
				}
				if strings.HasSuffix(name, ".cyclonedx.json") {
					data = []byte(`{"bomFormat":"CycloneDX","specVersion":"1.5"}`)
				}
				require.NoError(t, os.WriteFile(filepath.Join(dist, name), data, fileMode))
				fmt.Fprintf(&checksums, "%s  %s\n", digest(data), name)
			}
			require.NoError(t, os.WriteFile(filepath.Join(dist, "checksums.txt"), []byte(checksums.String()), fileMode))
			if failure == "inventory" {
				require.NoError(t, os.WriteFile(filepath.Join(dist, "checksums.txt"), nil, fileMode))
			}
			staging := func(string) (string, error) {
				if failure == "staging" {
					return "", errors.New("unsafe staging")
				}
				return os.MkdirTemp(root, "verifier-")
			}
			pin := func(context.Context, string, string, installrelease.Cosign) (installrelease.Cosign, error) {
				if failure == "pin" {
					return installrelease.Cosign{}, errors.New("pin mismatch")
				}
				return installrelease.Cosign{Path: "/independently/pinned/cosign"}, nil
			}
			execute := func(_ context.Context, spec process.Spec) (releaseaudit.Result, error) {
				require.Equal(t, "/independently/pinned/cosign", spec.Path)
				require.Contains(t, spec.Args, installrelease.Identity("0.2.4"))
				require.Contains(t, spec.Args, installrelease.Issuer)
				require.Contains(t, spec.Args, "--certificate-github-workflow-sha")
				require.Contains(t, spec.Args, strings.Repeat("a", 40))
				for _, entry := range spec.Env {
					require.False(t, strings.HasPrefix(entry, "COSIGN_") || strings.HasPrefix(entry, "SIGSTORE_"))
				}
				if failure == "signature" {
					return releaseaudit.Result{ExitCode: 1}, nil
				}
				return releaseaudit.Result{}, nil
			}
			hashes, err := authenticateWith(context.Background(), dist, "0.2.4", strings.Repeat("a", 40), staging, pin, execute)
			if failure == "" {
				require.NoError(t, err)
				require.Len(t, hashes, len(contract.ExpectedPayloadNames))
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestArtifactAndSBOMCoupling(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"", "contract", "artifact", "sbom", "cleanup"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			version := "0.3.0"
			if failure == "contract" {
				version = ""
			}
			inspected, validated := 0, 0
			inspect := func(filename string) (*releasecheck.ArchiveFacts, error) {
				inspected++
				if failure == "artifact" {
					return nil, errors.New("corrupt binary")
				}
				identity, err := releasecheck.ParseReleaseArtifactName(filepath.Base(filename))
				require.NoError(t, err)
				facts := &releasecheck.ArchiveFacts{Kind: identity.Kind, TargetArch: identity.Arch,
					Executables: map[string]*releasecheck.ExecutableFacts{"microfat": {SHA256: strings.Repeat("a", 64)}}}
				if failure == "cleanup" {
					facts.StagingDir = filepath.Join(t.TempDir(), "invalid\x00")
				}
				return facts, nil
			}
			validate := func(_, name string, facts *releasecheck.ArchiveFacts) error {
				validated++
				require.NotNil(t, facts)
				require.NotEmpty(t, name)
				if failure == "sbom" {
					return errors.New("SBOM mismatch")
				}
				return nil
			}
			products, err := inspectProducts(t.TempDir(), version, inspect, validate)
			if failure != "" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 4, inspected)
			require.Equal(t, inspected, validated)
			require.Len(t, products, 2)
		})
	}
	require.Error(t, validateSBOMs(t.TempDir(), "asset", nil))
	facts := &releasecheck.ArchiveFacts{ArchiveName: "microfat_0.3.0_linux_amd64.tar.gz", TargetArch: "amd64",
		Executables: map[string]*releasecheck.ExecutableFacts{"microfat": {BuildInfo: &buildinfo.BuildInfo{}}}}
	require.Error(t, validateSBOMs(t.TempDir(), "asset", facts))
}

func TestCaskCheckFailures(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"", "directory", "large", "syntax", "authentication", "absent output", "changed"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			file := filepath.Join(t.TempDir(), "microfat.rb")
			data := fixtureRecipe(t, "0.3.0")
			if failure == "syntax" {
				data = []byte("invalid")
			}
			if failure == "large" {
				data = bytesRepeat('a', int(metadataLimit+1))
			}
			if failure == "directory" {
				require.NoError(t, os.Mkdir(file, privateMode))
			} else {
				require.NoError(t, os.WriteFile(file, data, fileMode))
			}
			prepare := func(_ context.Context, tag, output string) (Evidence, error) {
				require.Equal(t, "v0.3.0", tag)
				if failure == "authentication" {
					return Evidence{}, errors.New("signature rejected")
				}
				require.NoError(t, os.Mkdir(output, privateMode))
				if failure != "absent output" {
					if failure == "changed" {
						data = append(data, '\n')
					}
					require.NoError(t, os.WriteFile(filepath.Join(output, "microfat.rb"), data, fileMode))
				}
				return Evidence{}, nil
			}
			assert.Equal(t, failure != "", check(context.Background(), file, prepare) != nil)
		})
	}
}

func bytesRepeat(value byte, size int) []byte { return []byte(strings.Repeat(string(value), size)) }

type failedOutput struct{}

func (failedOutput) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestFetchAndPreparationTransportFailures(t *testing.T) {
	t.Parallel()
	c := fixtureClient(t, populatedRelease(t))
	require.Error(t, c.fetch(context.Background(), ":invalid", metadataLimit, io.Discard))
	require.Error(t, c.fetch(context.Background(), apiBase, metadataLimit, failedOutput{}))
	for _, point := range []string{"metadata", "invalid metadata", "source", "invalid source", "asset", "hashes", "checksum", "evidence"} {
		t.Run(point, func(t *testing.T) {
			t.Parallel()
			c := fixtureClient(t, populatedRelease(t))
			old := c.http.Transport
			c.http.Transport = transport(func(req *http.Request) (*http.Response, error) {
				path := req.URL.Path
				if (point == "metadata" && strings.Contains(path, "/releases/tags/")) ||
					(point == "source" && strings.Contains(path, "/commits/")) ||
					(point == "asset" && strings.Contains(path, "/download/")) {
					return nil, errors.New("offline")
				}
				if (point == "invalid metadata" && strings.Contains(path, "/releases/tags/")) ||
					(point == "invalid source" && strings.Contains(path, "/commits/")) {
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
				}
				return old.RoundTrip(req)
			})
			output := filepath.Join(t.TempDir(), "candidate")
			auth := func(_ context.Context, dist, _, _ string) (map[string]string, error) {
				if point == "checksum" {
					require.NoError(t, os.Remove(filepath.Join(dist, "checksums.txt")))
				}
				if point == "evidence" {
					require.NoError(t, os.Mkdir(filepath.Join(output, "verification.json"), privateMode))
				}
				if point == "hashes" {
					return nil, nil
				}
				return map[string]string{archiveName("0.3.0", "amd64"): strings.Repeat("a", 64),
					archiveName("0.3.0", "arm64"): strings.Repeat("b", 64)}, nil
			}
			_, err := c.prepare(context.Background(), "v0.3.0", output, auth,
				func(string, string) (map[string]map[string]string, error) { return nil, nil })
			require.Error(t, err)
			require.NoFileExists(t, filepath.Join(output, "microfat.rb"))
		})
	}
}
