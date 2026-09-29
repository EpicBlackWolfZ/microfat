package homebrew

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func metadata(t *testing.T, tag string) release {
	t.Helper()
	var r release
	require.NoError(t, json.Unmarshal([]byte(`{"id":1,"tag_name":"`+tag+`","draft":false,"prerelease":false,`+
		`"immutable":true,"published_at":"2026-09-29T10:00:00Z"}`), &r))
	return r
}

func fixtureClient(t *testing.T, r release) client {
	t.Helper()
	return client{http: &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		var data []byte
		switch {
		case strings.Contains(req.URL.Path, "/releases/tags/"), strings.HasSuffix(req.URL.Path, "/latest"):
			data, _ = json.Marshal(r)
		case strings.Contains(req.URL.Path, "/commits/"):
			data = []byte(`{"sha":"` + strings.Repeat("a", 40) + `"}`)
		default:
			data = []byte("fixture bytes")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
	})}}
}

func populatedRelease(t *testing.T) release {
	t.Helper()
	r := metadata(t, "v0.3.0")
	_, err := requiredAssets(r, "0.3.0")
	require.Error(t, err)
	names := []string{"checksums.txt", "checksums.txt.sig"}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, product := range []string{archiveName("0.3.0", arch), "microfat-install_0.3.0_linux_" + arch} {
			for _, suffix := range []string{"", ".spdx.json", ".cyclonedx.json"} {
				names = append(names, product+suffix)
			}
		}
	}
	for _, name := range names {
		r.Assets = append(r.Assets, struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		}{name, 1})
	}
	return r
}

func TestReleaseMetadata(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"", "id", "tag", "draft", "missing draft", "prerelease", "mutable", "missing immutable", "date"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			r := metadata(t, "v0.3.0")
			switch failure {
			case "id":
				r.ID = 0
			case "tag":
				r.Tag = "v0.3.1"
			case "draft":
				*r.Draft = true
			case "missing draft":
				r.Draft = nil
			case "prerelease":
				*r.Prerelease = true
			case "mutable":
				*r.Immutable = false
			case "missing immutable":
				r.Immutable = nil
			case "date":
				r.Published = ""
			}
			assert.Equal(t, failure != "", r.validate("v0.3.0") != nil)
		})
	}
	for _, tag := range []string{"v0.2.5", "v0.3.0"} {
		r := metadata(t, tag)
		value, err := fixtureClient(t, r).discover(context.Background())
		require.NoError(t, err)
		if tag == "v0.2.5" {
			assert.Empty(t, value)
		} else {
			assert.Equal(t, tag, value)
		}
	}
	for _, change := range []func(*release){func(r *release) { r.Tag = "bad" }, func(r *release) { r.Draft = nil },
		func(r *release) { r.Published = "bad" }} {
		r := metadata(t, "v0.3.0")
		change(&r)
		_, err := fixtureClient(t, r).discover(context.Background())
		require.Error(t, err)
	}
}

func TestPreparationOrdering(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"", "signature", "products", "missing", "duplicate", "oversized", "existing", "bad tag"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			r := populatedRelease(t)
			output := filepath.Join(t.TempDir(), "candidate")
			tag := r.Tag
			switch failure {
			case "missing":
				r.Assets = r.Assets[1:]
			case "duplicate":
				r.Assets = append(r.Assets, r.Assets[0])
			case "oversized":
				r.Assets[0].Size = assetLimit + 1
			case "existing":
				require.NoError(t, os.Mkdir(output, privateMode))
			case "bad tag":
				tag = "v0.2.5"
			}
			var authenticated, validated bool
			auth := func(_ context.Context, dist, version, source string) (map[string]string, error) {
				require.FileExists(t, filepath.Join(dist, "checksums.txt.sig"))
				require.Equal(t, "0.3.0", version)
				require.Equal(t, strings.Repeat("a", 40), source)
				if failure == "signature" {
					return nil, errors.New("signature rejected")
				}
				authenticated = true
				return map[string]string{archiveName(version, "amd64"): strings.Repeat("a", 64),
					archiveName(version, "arm64"): strings.Repeat("b", 64)}, nil
			}
			validate := func(string, string) (map[string]map[string]string, error) {
				require.True(t, authenticated)
				if failure == "products" {
					return nil, errors.New("invalid ELF")
				}
				validated = true
				return map[string]map[string]string{}, nil
			}
			evidence, err := fixtureClient(t, r).prepare(context.Background(), tag, output, auth, validate)
			if failure != "" {
				require.Error(t, err)
				assert.NoFileExists(t, filepath.Join(output, "microfat.rb"))
				return
			}
			require.NoError(t, err)
			require.True(t, validated)
			data, err := os.ReadFile(filepath.Join(output, "microfat.rb"))
			require.NoError(t, err)
			assert.Equal(t, fixtureRecipe(t, "0.3.0"), data)
			assert.Equal(t, digest(data), evidence.Recipe)
			assert.FileExists(t, filepath.Join(output, "verification.json"))
		})
	}
}

func TestDownloadLimitsAndRedirects(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"valid", "status", "length", "stream", "transport", "json", "write"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			c := client{http: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
				if scenario == "transport" {
					return nil, errors.New("offline")
				}
				r := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("1234"))}
				if scenario == "status" {
					r.StatusCode = http.StatusNotFound
				}
				if scenario == "length" {
					r.ContentLength = metadataLimit + 1
				}
				return r, nil
			})}}
			var out bytes.Buffer
			limit := metadataLimit
			if scenario == "stream" {
				limit = 1
			}
			var err error
			switch scenario {
			case "json":
				err = c.json(context.Background(), apiBase, &struct{}{})
			case "write":
				err = c.download(context.Background(), apiBase, filepath.Join(t.TempDir(), "missing", "file"), limit)
			default:
				err = c.fetch(context.Background(), apiBase, limit, &out)
			}
			assert.Equal(t, scenario != "valid", err != nil)
		})
	}
	c := newClient()
	for _, address := range []string{"https://github.com/allowed", "http://github.com/bad", "https://example.com/bad",
		"https://user@github.com/bad", "https://github.com:1234/bad"} {
		req, err := http.NewRequest(http.MethodGet, address, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer fixture")
		assert.Equal(t, address != "https://github.com/allowed", c.http.CheckRedirect(req, nil) != nil)
		if address == "https://github.com/allowed" {
			assert.Empty(t, req.Header.Get("Authorization"))
		}
		require.Error(t, c.http.CheckRedirect(req, make([]*http.Request, redirectLimit)))
	}
}
