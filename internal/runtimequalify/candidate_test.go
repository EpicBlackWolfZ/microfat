package runtimequalify

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/internal/releaseaudit"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/stretchr/testify/require"
)

func candidateFixture(t *testing.T) *controller {
	t.Helper()
	c, _ := fakeController(t, runtime.GOARCH)
	c.options.Input, c.options.Tag, c.options.Source, c.options.Dist = Candidate, "v0.2.4", testSource, t.TempDir()
	c.summary.Input, c.summary.Tag, c.summary.Source = Candidate, c.options.Tag, testSource
	contract, err := releasecheck.NewReleaseContract(c.options.Tag)
	require.NoError(t, err)
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	writer := tar.NewWriter(gz)
	for _, name := range []string{"microfat", fullStub, minimalStub, "README.md", "LICENSE", "SECURITY.md"} {
		content := "downloaded " + name
		require.NoError(t, writer.WriteHeader(&tar.Header{Name: name, Size: int64(len(content)), Mode: 0o755, Typeflag: tar.TypeReg}))
		_, err = writer.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	require.NoError(t, gz.Close())
	var rows []string
	for name := range contract.ExpectedPayloadNames {
		content := data.Bytes()
		switch {
		case strings.HasSuffix(name, ".spdx.json"):
			content = []byte(`{"spdxVersion":"SPDX-2.3"}`)
		case strings.HasSuffix(name, ".cyclonedx.json"):
			content = []byte(`{"bomFormat":"CycloneDX","specVersion":"1.5"}`)
		}
		path := filepath.Join(c.options.Dist, name)
		require.NoError(t, os.WriteFile(path, content, dataMode))
		hash, err := releaseaudit.Digest(path)
		require.NoError(t, err)
		rows = append(rows, hash+"  "+name)
	}
	sort.Strings(rows)
	require.NoError(t, os.WriteFile(filepath.Join(c.options.Dist, "checksums.txt"), []byte(strings.Join(rows, "\n")+"\n"), dataMode))
	require.NoError(t, os.WriteFile(filepath.Join(c.options.Dist, "checksums.txt.sig"), []byte("unit signature fixture"), dataMode))
	release := Release{ID: 19, Tag: c.options.Tag, Draft: true}
	for _, name := range append(slices.Sorted(mapKeys(contract.ExpectedPayloadNames)), "checksums.txt", "checksums.txt.sig") {
		path := filepath.Join(c.options.Dist, name)
		stat, err := os.Stat(path)
		require.NoError(t, err)
		hash, err := releaseaudit.Digest(path)
		require.NoError(t, err)
		release.Assets = append(release.Assets,
			Asset{ID: int64(len(release.Assets) + 1), Name: name, Size: stat.Size(), Digest: "sha256:" + hash})
	}
	require.NoError(t, WriteJSON(filepath.Join(c.options.Dist, "release.json"), release))
	return c
}

// Keep fake inventories deterministic, independently of production map iteration.
func mapKeys(values map[string]bool) func(func(string) bool) {
	return func(yield func(string) bool) {
		for name := range values {
			if !yield(name) {
				return
			}
		}
	}
}

func TestCandidateNeverBuildsSubstituteProducts(t *testing.T) {
	t.Parallel()
	c := candidateFixture(t)
	authenticated := false
	c.runner.Execute = func(_ context.Context, spec process.Spec) (releaseaudit.Result, error) {
		if spec.Path == "cosign" {
			require.False(t, authenticated)
			require.Contains(t, spec.Args, "https://github.com/EpicBlackWolfZ/microfat/.github/workflows/release.yml@refs/tags/v0.2.4")
			require.Contains(t, spec.Args, "https://token.actions.githubusercontent.com")
			require.Contains(t, spec.Args, testSource)
			authenticated = true
			return releaseaudit.Result{}, nil
		}
		require.True(t, authenticated, "nothing executes before authentication")
		require.Equal(t, "go", spec.Path)
		if spec.Args[0] == "version" {
			return releaseaudit.Result{Stdout: "recorded build settings"}, nil
		}
		require.Equal(t, "build", spec.Args[0])
		require.Contains(t, []string{"./tests/e2e/testdata/mount_runner", "./tests/e2e/testdata/mount_reporter"}, spec.Args[len(spec.Args)-1],
			"candidate mode may only build qualification helpers")
		require.NoError(t, os.WriteFile(flagValue(spec.Args, "-o"), []byte("helper"), privateMode))
		return releaseaudit.Result{}, nil
	}
	require.NoError(t, c.acquire())
	require.True(t, authenticated)
	require.NoError(t, c.buildHelpers())
	for _, name := range []string{"microfat", fullStub, minimalStub} {
		content, err := os.ReadFile(filepath.Join(c.products, name))
		require.NoError(t, err)
		require.Equal(t, "downloaded "+name, string(content))
	}
	require.FileExists(t, filepath.Join(c.options.Output, "authentication", "checksums.txt.sig"))
}

func TestCandidateAuthenticationFailureStopsBeforeExecution(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"signature", "checksum", "release-json", "release-identity", "signature-digest", "missing-archive"} {
		t.Run(failure, func(t *testing.T) {
			c := candidateFixture(t)
			calls := 0
			c.runner.Execute = func(_ context.Context, spec process.Spec) (releaseaudit.Result, error) {
				calls++
				require.Equal(t, "cosign", spec.Path, "untrusted product must never execute")
				if failure == "signature" {
					return releaseaudit.Result{}, fmt.Errorf("wrong workflow identity")
				}
				return releaseaudit.Result{}, nil
			}
			switch failure {
			case "checksum":
				require.NoError(t, os.WriteFile(filepath.Join(c.options.Dist, "checksums.txt"), []byte("bad"), dataMode))
			case "release-json":
				require.NoError(t, os.WriteFile(filepath.Join(c.options.Dist, "release.json"), []byte("{"), dataMode))
			case "release-identity":
				require.NoError(t, WriteJSON(filepath.Join(c.options.Dist, "release.json"), Release{ID: 1, Tag: "v9.0.0"}))
			case "signature-digest":
				require.NoError(t, os.WriteFile(filepath.Join(c.options.Dist, "checksums.txt.sig"), []byte("changed"), dataMode))
			case "missing-archive":
				require.NoError(t, os.Remove(filepath.Join(c.options.Dist, "microfat_0.2.4_linux_"+runtime.GOARCH+".tar.gz")))
			}
			require.Error(t, c.acquire())
			require.Equal(t, 1, calls)
			require.NoDirExists(t, filepath.Join(c.options.Output, "products"))
		})
	}
}
