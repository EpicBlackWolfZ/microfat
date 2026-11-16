package installrelease

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureARM64 = "arm64"

func publishedFixture(t *testing.T, version, arch string) map[string]any {
	t.Helper()
	return map[string]any{"tag_name": "v" + version, "draft": false, "prerelease": false,
		"assets": []map[string]string{{"name": "microfat_" + version + "_linux_" + arch + ".tar.gz"},
			{"name": "checksums.txt"}, {"name": "checksums.txt.sig"}}}
}

func TestPublishedSelection(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"latest", "pinned", fixtureARM64, "draft", "prerelease", "missing-flag", "wrong-tag",
		"noncanonical", "unsupported", "missing-asset", "duplicate", "malformed", "oversize", "http", "transport", "local", "arch", "version"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			arch, requested := "amd64", ""
			if scenario == fixtureARM64 {
				arch = fixtureARM64
			}
			fixture := publishedFixture(t, "0.3.0", arch)
			address := apiURL
			if scenario == "pinned" || scenario == "wrong-tag" {
				requested = "v0.3.0"
				address = "https://api.github.com/repos/" + Repository + "/releases/tags/v0.3.0"
			}
			switch scenario {
			case "draft", "prerelease":
				fixture[scenario] = true
			case "missing-flag":
				delete(fixture, "draft")
			case "wrong-tag":
				fixture["tag_name"] = "v0.3.1"
			case "noncanonical":
				fixture["tag_name"] = "0.3.0"
			case "unsupported":
				fixture["tag_name"] = "v0.2.2"
			case "missing-asset":
				fixture["assets"] = nil
			case "duplicate":
				fixture["assets"] = append(fixture["assets"].([]map[string]string), map[string]string{"name": "checksums.txt"})
			case "arch":
				arch = "riscv64"
			case "version":
				requested = "0.3.0-rc1"
			}
			data, err := json.Marshal(fixture)
			require.NoError(t, err)
			if scenario == "malformed" {
				data = []byte("{")
			}
			if scenario == "oversize" {
				data = []byte(strings.Repeat("x", int(maxMetadataBytes)+1))
			}
			client := testClient(t, map[string][]byte{address: data})
			if scenario == "http" {
				client = testClient(t, nil)
			}
			if scenario == "local" {
				client, err = NewLocalClient(t.TempDir())
				require.NoError(t, err)
			}
			if scenario == "transport" {
				client.http.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
			}
			version, err := client.Published(t.Context(), requested, arch)
			if scenario == "latest" || scenario == "pinned" || scenario == fixtureARM64 {
				require.NoError(t, err)
				assert.Equal(t, "0.3.0", version)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestPinnedVerifierAndBootstrapContract(t *testing.T) {
	t.Parallel()
	for _, arch := range []string{"amd64", fixtureARM64} {
		pin, err := cosignPin(arch)
		require.NoError(t, err)
		data, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install.sh"))
		require.NoError(t, err)
		assert.Contains(t, string(data), "readonly cosign_"+arch+"='"+CosignVersion()+"@"+pin+"'")
		data, err = os.ReadFile(filepath.Join("..", "..", "scripts", "qualify-installer.sh"))
		require.NoError(t, err)
		assert.Contains(t, string(data), "/.config/tool-versions.env")
		assert.Contains(t, string(data), "${COSIGN_LINUX_"+strings.ToUpper(arch)+"}")
	}
	_, err := cosignPin("other")
	require.Error(t, err)
	path := filepath.Join(t.TempDir(), "cosign")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	// fixture hashes are an explicit independent override, never a default trust pin.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	override := Cosign{Path: path, SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	client := testClient(t, nil)
	actual, err := client.PrepareVerifier(t.Context(), "amd64", t.TempDir(), override)
	require.NoError(t, err)
	assert.Equal(t, override, actual)
	for _, invalid := range []Cosign{
		{Path: "relative", SHA256: override.SHA256},
		{Path: path, SHA256: strings.Repeat("0", 64)}, {SHA256: override.SHA256}} {
		_, err = client.PrepareVerifier(t.Context(), "amd64", t.TempDir(), invalid)
		require.Error(t, err)
	}
	for _, scenario := range []string{"arch", "download", "digest", "existing"} {
		t.Run(scenario, func(t *testing.T) {
			stage, arch := t.TempDir(), "amd64"
			address := "https://github.com/sigstore/cosign/releases/download/" + CosignVersion() + "/cosign-linux-amd64"
			client := testClient(t, map[string][]byte{address: []byte("wrong bytes")})
			if scenario == "arch" {
				arch = "other"
			}
			if scenario == "download" {
				client = testClient(t, nil)
			}
			if scenario == "existing" {
				require.NoError(t, os.WriteFile(filepath.Join(stage, "cosign"), nil, 0o600))
			}
			_, err := client.PrepareVerifier(t.Context(), arch, stage, Cosign{})
			require.Error(t, err)
		})
	}
}

func TestPublishedCancellationAndBounds(t *testing.T) {
	t.Parallel()
	client := NewClient()
	client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) { return nil, request.Context().Err() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := client.Published(ctx, "", "amd64")
	require.ErrorIs(t, err, context.Canceled)
	client.http.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, ContentLength: -1,
			Body: io.NopCloser(strings.NewReader(strings.Repeat("x", int(maxMetadataBytes)+1)))}, nil
	})
	_, err = client.Published(t.Context(), "", "amd64")
	require.ErrorContains(t, err, "size limit")
}

func TestDefaultPinnedVerifier(t *testing.T) {
	if os.Getenv("MICROFAT_SIGNATURE_TESTS") != "required" {
		t.Skip("real pinned verifier acquisition is required in signature qualification")
	}
	stage, err := Staging(t.TempDir())
	require.NoError(t, err)
	defer os.RemoveAll(stage)
	verifier, err := NewClient().PrepareVerifier(t.Context(), runtime.GOARCH, stage, Cosign{})
	require.NoError(t, err)
	expected, err := cosignPin(runtime.GOARCH)
	require.NoError(t, err)
	assert.Equal(t, expected, verifier.SHA256)
	require.NoError(t, verifier.Verify(t.Context(), "0.2.4",
		filepath.Join("..", "..", "tests", "e2e", "testdata", "release-trust", "checksums.txt"),
		filepath.Join("..", "..", "tests", "e2e", "testdata", "release-trust", "checksums.txt.sig")))
}

func TestPublishedRateLimitWebFallback(t *testing.T) {
	t.Run("unversioned_403_uses_web_latest_and_head_assets", func(t *testing.T) {
		t.Setenv("GH_TOKEN", "secret-token")
		t.Setenv("GITHUB_TOKEN", "")
		client := NewClient()
		const wantVersion = "0.4.0"
		const cdnURL = "https://release-assets.githubusercontent.com/asset-blob"
		seenAssets := make(map[string]int)
		client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Host != "api.github.com" {
				assert.Empty(t, request.Header.Get("Authorization"))
			}
			switch {
			case request.Method == http.MethodGet && request.URL.String() == apiURL:
				assert.Equal(t, "Bearer secret-token", request.Header.Get("Authorization"))
				return &http.Response{StatusCode: http.StatusForbidden, Body: http.NoBody}, nil
			case request.Method == http.MethodHead && request.URL.String() == latestReleaseURL:
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     http.Header{redirectLocationHeader: []string{"/EpicBlackWolfZ/microfat/releases/tag/v" + wantVersion}},
					Body:       http.NoBody,
				}, nil
			case request.Method == http.MethodHead && strings.HasPrefix(request.URL.String(), downloadURL+wantVersion+"/"):
				name := strings.TrimPrefix(request.URL.String(), downloadURL+wantVersion+"/")
				seenAssets[name]++
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     http.Header{redirectLocationHeader: []string{cdnURL + "/" + name}},
					Body:       http.NoBody,
				}, nil
			case request.Method == http.MethodHead && strings.HasPrefix(request.URL.String(), cdnURL+"/"):
				return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
			default:
				return nil, fmt.Errorf("unexpected %s %s", request.Method, request.URL.String())
			}
		})
		version, err := client.Published(t.Context(), "", "amd64")
		require.NoError(t, err)
		assert.Equal(t, wantVersion, version)
		assert.Equal(t, map[string]int{
			"microfat_0.4.0_linux_amd64.tar.gz": 1,
			"checksums.txt":                     1,
			"checksums.txt.sig":                 1,
		}, seenAssets)
	})

	t.Run("versioned_429_checks_assets_directly", func(t *testing.T) {
		t.Parallel()
		client := NewClient()
		seenAssets := make(map[string]int)
		client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
			switch {
			case request.Method == http.MethodGet && request.URL.String() == apiTagURL+"0.2.5":
				return &http.Response{StatusCode: http.StatusTooManyRequests, Body: http.NoBody}, nil
			case request.Method == http.MethodHead && strings.HasPrefix(request.URL.String(), downloadURL+"0.2.5/"):
				name := strings.TrimPrefix(request.URL.String(), downloadURL+"0.2.5/")
				seenAssets[name]++
				return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
			default:
				return nil, fmt.Errorf("unexpected %s %s", request.Method, request.URL.String())
			}
		})
		version, err := client.Published(t.Context(), "v0.2.5", fixtureARM64)
		require.NoError(t, err)
		assert.Equal(t, "0.2.5", version)
		assert.Equal(t, map[string]int{
			"microfat_0.2.5_linux_arm64.tar.gz": 1,
			"checksums.txt":                     1,
			"checksums.txt.sig":                 1,
		}, seenAssets)
	})

	t.Run("fallback_failures", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name    string
			handler transportFunc
			wantErr string
		}{
			{
				name: "latest_redirect_fails",
				handler: func(request *http.Request) (*http.Response, error) {
					if request.Method == http.MethodGet {
						return &http.Response{StatusCode: http.StatusForbidden, Body: http.NoBody}, nil
					}
					return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody}, nil
				},
				wantErr: "HTTP 503",
			},
			{
				name: "missing_asset_head_404",
				handler: func(request *http.Request) (*http.Response, error) {
					if request.Method == http.MethodGet {
						return &http.Response{StatusCode: http.StatusForbidden, Body: http.NoBody}, nil
					}
					if strings.HasSuffix(request.URL.Path, "checksums.txt.sig") {
						return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
					}
					return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
				},
				wantErr: "HTTP 404",
			},
			{
				name: "asset_head_transport_error",
				handler: func(request *http.Request) (*http.Response, error) {
					if request.Method == http.MethodGet {
						return &http.Response{StatusCode: http.StatusForbidden, Body: http.NoBody}, nil
					}
					return nil, errors.New("head reset")
				},
				wantErr: "head reset",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				client := NewClient()
				client.http.Transport = tc.handler
				requested := "0.2.5"
				if tc.name == "latest_redirect_fails" {
					requested = ""
				}
				_, err := client.Published(t.Context(), requested, "amd64")
				require.ErrorContains(t, err, tc.wantErr)
			})
		}
	})
}
