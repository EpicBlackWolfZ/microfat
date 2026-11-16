package installrelease

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/install"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type verifierFunc func(context.Context, string, string, string) error

func (f verifierFunc) Verify(ctx context.Context, version, checksums, bundle string) error {
	return f(ctx, version, checksums, bundle)
}

func testClient(t *testing.T, responses map[string][]byte) *Client {
	t.Helper()
	client := NewClient()
	client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
		data, ok := responses[request.URL.String()]
		status := http.StatusOK
		if !ok {
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(data)),
			ContentLength: int64(len(data)), Request: request}, nil
	})
	return client
}

func elfFixture(arch string) []byte {
	data := make([]byte, 64)
	copy(data, elf.ELFMAG)
	data[elf.EI_CLASS] = byte(elf.ELFCLASS64)
	data[elf.EI_DATA] = byte(elf.ELFDATA2LSB)
	data[elf.EI_VERSION] = byte(elf.EV_CURRENT)
	binary.LittleEndian.PutUint16(data[16:], uint16(elf.ET_EXEC))
	machine := elf.EM_X86_64
	if arch == fixtureARM64 {
		machine = elf.EM_AARCH64
	}
	binary.LittleEndian.PutUint16(data[18:], uint16(machine))
	binary.LittleEndian.PutUint32(data[20:], uint32(elf.EV_CURRENT))
	binary.LittleEndian.PutUint16(data[52:], 64)
	return data
}

func fatFixture(t *testing.T, arch string) []byte {
	t.Helper()
	data := bytes.NewBuffer(elfFixture(arch))
	payload := []byte("authenticated-fixture-payload")
	hash := sha256.Sum256(payload)
	_, err := data.Write(payload)
	require.NoError(t, err)
	level := "v1"
	if arch == fixtureARM64 {
		level = "v8.0"
	}
	index := &format.Index{Version: 2, TargetOS: "linux", TargetArch: arch, Variants: []format.VariantEntry{{Level: level, Offset: 64,
		CompressedSize: int64(len(payload)), UncompressedSize: int64(len(payload)), Compression: "none", SHA256: hex.EncodeToString(hash[:])}}}
	_, err = format.WriteIndexAndTrailer(data, index, int64(data.Len()))
	require.NoError(t, err)
	return data.Bytes()
}

func archiveFixture(t *testing.T, arch string, change func(*tar.Header, []byte) (*tar.Header, []byte)) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)
	for _, name := range append(install.Products(), "docs/guide.md") {
		data := elfFixture(arch)
		if name == "microfat" {
			data = fatFixture(t, arch)
		}
		header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}
		if change != nil {
			header, data = change(header, data)
			if header == nil {
				continue
			}
		}
		require.NoError(t, writer.WriteHeader(header))
		_, err := writer.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	require.NoError(t, compressed.Close())
	return buffer.Bytes()
}

func releaseResponses(archive []byte) map[string][]byte {
	base := downloadURL + "0.3.0/"
	name := "microfat_0.3.0_linux_amd64.tar.gz"
	return map[string][]byte{base + name: archive, base + "checksums.txt": fmt.Appendf(nil, "%x  %s\n", sha256.Sum256(archive), name),
		base + "checksums.txt.sig": []byte("signature fixture"), apiURL: []byte(`{"tag_name":"v0.3.0","draft":false,"prerelease":false}`)}
}

func TestAcquireAuthenticatesBeforeExtraction(t *testing.T) {
	t.Parallel()
	archive := archiveFixture(t, "amd64", nil)
	responses := releaseResponses(archive)
	for _, scenario := range []string{"success", "signature", "duplicate checksum", "missing checksum",
		"tampered archive", "download", "exists"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			staging := t.TempDir()
			local := make(map[string][]byte)
			for k, v := range responses {
				local[k] = v
			}
			base := downloadURL + "0.3.0/"
			name := "microfat_0.3.0_linux_amd64.tar.gz"
			switch scenario {
			case "duplicate checksum":
				local[base+"checksums.txt"] = bytes.Repeat(local[base+"checksums.txt"], 2)
			case "missing checksum":
				local[base+"checksums.txt"] = []byte(fmt.Sprintf("%x  another.tar.gz\n", sha256.Sum256(archive)))
			case "tampered archive":
				local[base+name] = []byte("tampered")
			case "download":
				delete(local, base+name)
			case "exists":
				require.NoError(t, os.WriteFile(filepath.Join(staging, "checksums.txt"), []byte("unrelated"), fileMode))
			}
			verified := false
			verify := verifierFunc(func(_ context.Context, version, checksums, bundle string) error {
				assert.Equal(t, "0.3.0", version)
				assert.FileExists(t, checksums)
				assert.FileExists(t, bundle)
				assert.NoFileExists(t, filepath.Join(staging, name))
				assert.NoDirExists(t, filepath.Join(staging, "products"))
				if scenario == "signature" {
					return errors.New("wrong signing identity")
				}
				verified = true
				return nil
			})
			generation, products, err := testClient(t, local).Acquire(t.Context(), "0.3.0", "amd64", staging, verify)
			if scenario != "success" {
				require.Error(t, err)
				assert.NoDirExists(t, filepath.Join(staging, "products"))
				return
			}
			require.NoError(t, err)
			require.True(t, verified)
			require.NoError(t, generation.Validate())
			for _, name := range install.Products() {
				assert.FileExists(t, filepath.Join(products, name))
			}
			assert.NoDirExists(t, filepath.Join(products, "docs"), "only the three products are staged")
		})
	}
}

func TestVersionSelection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		input string
		valid bool
	}{{"v0.2.3", true}, {"0.2.5", true}, {"1.0.0", true}, {"0.2.2", false}, {"0.3.0-rc.1", false},
		{"01.2.3", false}, {"latest", false}, {strings.Repeat("9", 100) + ".0.0", false}} {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			_, err := ParseVersion(tc.input)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	for _, data := range []string{`{"tag_name":"v0.3.0","draft":false,"prerelease":false}`,
		`{"tag_name":"v0.3.0","draft":true,"prerelease":false}`,
		`{"tag_name":"v0.3.0","draft":false,"prerelease":true}`, `{"tag_name":"v0.3.0"}`, `invalid`} {
		t.Run(data, func(t *testing.T) {
			t.Parallel()
			version, err := testClient(t, map[string][]byte{apiURL: []byte(data)}).Resolve(t.Context(), "")
			if data == `{"tag_name":"v0.3.0","draft":false,"prerelease":false}` {
				require.NoError(t, err)
				assert.Equal(t, "0.3.0", version)
			} else {
				require.Error(t, err)
			}
		})
	}
	version, err := NewClient().Resolve(t.Context(), "v0.2.4")
	require.NoError(t, err)
	assert.Equal(t, "0.2.4", version)
	_, err = testClient(t, nil).Resolve(t.Context(), "")
	require.Error(t, err)
	assert.Positive(t, CompareVersions("0.10.0", "0.9.0"))
	assert.Negative(t, CompareVersions("0.3.0", "1.0.0"))
	assert.Positive(t, CompareVersions("1.0.0", "1.0"))
	assert.Negative(t, CompareVersions("1.0", "1.0.0"))
}

func TestDownloadBoundaries(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"http://github.com/file", "https://evil.invalid/file",
		"https://github.com:443/file", "https://u:p@github.com/file"} {
		t.Run(address, func(t *testing.T) {
			t.Parallel()
			err := NewClient().get(t.Context(), address, 10, io.Discard)
			require.Error(t, err)
		})
	}
	request := &http.Request{URL: &url.URL{Scheme: "https", Host: "release-assets.githubusercontent.com"}}
	require.NoError(t, checkRedirect(request, nil))
	require.Error(t, checkRedirect(request, make([]*http.Request, maxRedirects)))
	for _, length := range []int64{-1, 100} {
		client := NewClient()
		client.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, ContentLength: length,
				Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 100))), Request: r}, nil
		})
		require.ErrorContains(t, client.get(t.Context(), apiURL, 10, io.Discard), "size limit")
	}
	client := NewClient()
	client.http.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("network unavailable") })
	require.ErrorContains(t, client.get(t.Context(), apiURL, 10, io.Discard), "network unavailable")
	_, _, err := client.Acquire(t.Context(), "bad", "amd64", t.TempDir(), nil)
	require.Error(t, err)
	_, _, err = client.Acquire(t.Context(), "0.3.0", "mips", t.TempDir(), nil)
	require.Error(t, err)
	_, _, err = client.Acquire(t.Context(), "0.3.0", "amd64", t.TempDir(), nil)
	require.Error(t, err)
}

const redirectLocationHeader = "Location"

func TestAPITokenAndRedirectAuthorizationStripping(t *testing.T) {
	t.Run("prefers_GH_TOKEN_and_strips_on_redirect", func(t *testing.T) {
		t.Setenv("GH_TOKEN", "ghp_primary")
		t.Setenv("GITHUB_TOKEN", "ghp_secondary")
		client := NewClient()
		var apiAuth, apiAccept, assetAuth string
		client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
			switch request.URL.String() {
			case apiURL:
				apiAuth = request.Header.Get("Authorization")
				apiAccept = request.Header.Get("Accept")
				return &http.Response{
					StatusCode: http.StatusFound,
					Header:     http.Header{redirectLocationHeader: []string{downloadURL + "0.4.0/checksums.txt"}},
					Body:       http.NoBody,
				}, nil
			case downloadURL + "0.4.0/checksums.txt":
				assetAuth = request.Header.Get("Authorization")
				payload := `{"tag_name":"v0.4.0","draft":false,"prerelease":false}`
				return &http.Response{
					StatusCode:    http.StatusOK,
					ContentLength: int64(len(payload)),
					Body:          io.NopCloser(strings.NewReader(payload)),
				}, nil
			default:
				return nil, fmt.Errorf("unexpected URL %s", request.URL.String())
			}
		})
		version, err := client.Resolve(t.Context(), "")
		require.NoError(t, err)
		assert.Equal(t, "0.4.0", version)
		assert.Equal(t, "Bearer ghp_primary", apiAuth)
		assert.Equal(t, "application/vnd.github+json", apiAccept)
		assert.Empty(t, assetAuth)
	})

	t.Run("uses_GITHUB_TOKEN_and_ignores_crlf_tokens", func(t *testing.T) {
		t.Setenv("GH_TOKEN", "invalid\ntoken")
		t.Setenv("GITHUB_TOKEN", "ghp_fallback")
		client := NewClient()
		var gotAuth string
		client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
			gotAuth = request.Header.Get("Authorization")
			payload := `{"tag_name":"v0.4.0","draft":false,"prerelease":false}`
			return &http.Response{
				StatusCode:    http.StatusOK,
				ContentLength: int64(len(payload)),
				Body:          io.NopCloser(strings.NewReader(payload)),
			}, nil
		})
		version, err := client.Resolve(t.Context(), "")
		require.NoError(t, err)
		assert.Equal(t, "0.4.0", version)
		assert.Equal(t, "Bearer ghp_fallback", gotAuth)

		t.Setenv("GH_TOKEN", "")
		t.Setenv("GITHUB_TOKEN", "bad\rtoken")
		gotAuth = "sentinel"
		_, err = client.Resolve(t.Context(), "")
		require.NoError(t, err)
		assert.Empty(t, gotAuth)
	})

	t.Run("checkRedirect_retains_api_auth_and_strips_non_api_auth", func(t *testing.T) {
		apiReq, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/foo", nil)
		require.NoError(t, err)
		apiReq.Header.Set("Authorization", "Bearer keep")
		require.NoError(t, checkRedirect(apiReq, nil))
		assert.Equal(t, "Bearer keep", apiReq.Header.Get("Authorization"))

		cdnReq, err := http.NewRequest(http.MethodGet, "https://release-assets.githubusercontent.com/blob", nil)
		require.NoError(t, err)
		cdnReq.Header.Set("Authorization", "Bearer drop")
		require.NoError(t, checkRedirect(cdnReq, nil))
		assert.Empty(t, cdnReq.Header.Get("Authorization"))
	})
}

func TestResolveRateLimitWebFallback(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		for _, location := range []string{
			"https://github.com/EpicBlackWolfZ/microfat/releases/tag/v0.4.0",
			"/EpicBlackWolfZ/microfat/releases/tag/v0.4.0",
		} {
			name := fmt.Sprintf("http_%d_%s", status, location)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				client := NewClient()
				client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
					switch {
					case request.Method == http.MethodGet && request.URL.String() == apiURL:
						return &http.Response{StatusCode: status, Body: http.NoBody}, nil
					case request.Method == http.MethodHead && request.URL.String() == latestReleaseURL:
						return &http.Response{
							StatusCode: http.StatusFound,
							Header:     http.Header{redirectLocationHeader: []string{location}},
							Body:       http.NoBody,
						}, nil
					default:
						return nil, fmt.Errorf("unexpected %s %s", request.Method, request.URL.String())
					}
				})
				version, err := client.Resolve(t.Context(), "")
				require.NoError(t, err)
				assert.Equal(t, "0.4.0", version)
			})
		}
	}

	t.Run("non_rate_limit_http_error_does_not_fallback", func(t *testing.T) {
		t.Parallel()
		client := NewClient()
		headCalled := false
		client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method == http.MethodHead {
				headCalled = true
			}
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
		})
		_, err := client.Resolve(t.Context(), "")
		require.ErrorContains(t, err, "release download returned HTTP 404")
		assert.False(t, headCalled)
	})

	for _, tc := range []struct {
		name     string
		response *http.Response
		err      error
		wantErr  string
	}{
		{name: "transport_error", err: errors.New("network down"), wantErr: "network down"},
		{name: "non_redirect_status", response: &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, wantErr: "HTTP 200"},
		{name: "empty_location", response: &http.Response{StatusCode: http.StatusFound, Body: http.NoBody}, wantErr: "HTTP 302"},
		{
			name: "disallowed_host",
			response: &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{redirectLocationHeader: []string{"https://evil.example/EpicBlackWolfZ/microfat/releases/tag/v0.4.0"}},
				Body:       http.NoBody,
			},
			wantErr: "non-official latest release redirect",
		},
		{
			name: "query_in_redirect",
			response: &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{redirectLocationHeader: []string{"/EpicBlackWolfZ/microfat/releases/tag/v0.4.0?draft=1"}},
				Body:       http.NoBody,
			},
			wantErr: "non-official latest release redirect",
		},
		{
			name: "missing_tag_prefix",
			response: &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{redirectLocationHeader: []string{"/EpicBlackWolfZ/microfat/releases"}},
				Body:       http.NoBody,
			},
			wantErr: "published stable tag",
		},
		{
			name: "double_v_prefix",
			response: &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{redirectLocationHeader: []string{"/EpicBlackWolfZ/microfat/releases/tag/vv0.4.0"}},
				Body:       http.NoBody,
			},
			wantErr: "published stable tag",
		},
		{
			name: "pre_policy_version",
			response: &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{redirectLocationHeader: []string{"/EpicBlackWolfZ/microfat/releases/tag/v0.2.2"}},
				Body:       http.NoBody,
			},
			wantErr: "v0.2.3 and later",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := NewClient()
			client.http.Transport = transportFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodGet {
					return &http.Response{StatusCode: http.StatusForbidden, Body: http.NoBody}, nil
				}
				return tc.response, tc.err
			})
			_, err := client.Resolve(t.Context(), "")
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}
