package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/EpicBlackWolfZ/microfat/internal/install"
	"github.com/EpicBlackWolfZ/microfat/internal/installrelease"
)

const fixtureVersion = "0.3.0"

type fixtureClient struct {
	t                      *testing.T
	resolveErr, acquireErr error
	calls                  int
}

func (c *fixtureClient) Resolve(_ context.Context, version string) (string, error) {
	c.calls++
	if version == "" {
		version = fixtureVersion
	}
	return version, c.resolveErr
}

func (c *fixtureClient) Acquire(_ context.Context, version, arch, staging string,
	_ installrelease.Verifier) (install.Generation, string, error) {
	c.calls++
	g := install.Generation{Schema: install.SchemaVersion, ID: install.NewID(), Version: version, Arch: arch,
		ArchiveSHA256: strings.Repeat("a", 64), Files: map[string]install.File{}}
	for _, name := range install.Products() {
		data := []byte("#!/bin/sh\necho " + version + "\n")
		require.NoError(c.t, os.WriteFile(filepath.Join(staging, name), data, 0o755))
		g.Files[name] = install.File{Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	}
	return g, staging, c.acquireErr
}

func fixtureEnvironment(t *testing.T) (environment, options, *fixtureClient) {
	t.Helper()
	root := t.TempDir()
	client := &fixtureClient{t: t}
	env := environment{goos: "linux", arch: "amd64", uid: os.Geteuid(), client: client,
		home: func() (string, error) { return root, nil }, getenv: func(string) string { return "" }}
	opts := options{bin: filepath.Join(root, "bin space"), store: filepath.Join(root, "store space"),
		staging: root, cosign: "/independently/trusted/cosign", pin: strings.Repeat("a", 64), system: os.Geteuid() == 0}
	return env, opts, client
}

func TestResolvePaths(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"defaults", "xdg", "explicit", "system", "system-not-root", "system-missing-roots",
		"root-default", "os", "arch", "home-error", "relative-home", "relative-xdg", "overlap"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			env, _, _ := fixtureEnvironment(t)
			env.uid = 1000
			env.home = func() (string, error) { return "/home/example", nil }
			var opts options
			expected := install.Paths{Bin: "/home/example/.local/bin", Store: "/home/example/.local/share/microfat/installations/default"}
			valid := false
			switch scenario {
			case "defaults":
				valid = true
			case "xdg":
				valid = true
				env.getenv = func(string) string { return "/some data" }
				expected.Store = "/some data/microfat/installations/default"
			case "explicit", "system":
				valid = true
				opts.bin, opts.store = "/custom bin", "/custom store"
				expected = install.Paths{Bin: opts.bin, Store: opts.store}
				env.home = func() (string, error) { t.Fatal("explicit roots must not require HOME"); return "", nil }
				if scenario == "system" {
					opts.system, env.uid = true, 0
				}
			case "system-not-root":
				opts.system, opts.bin, opts.store = true, "/bin-dir", "/store"
			case "system-missing-roots":
				opts.system, env.uid = true, 0
			case "root-default":
				env.uid = 0
			case "os":
				env.goos = "darwin"
			case "arch":
				env.arch = "riscv64"
			case "home-error":
				env.home = func() (string, error) { return "", io.ErrUnexpectedEOF }
			case "relative-home":
				env.home = func() (string, error) { return "relative", nil }
			case "relative-xdg":
				env.getenv = func(string) string { return "relative" }
			case "overlap":
				opts.bin, opts.store = "/same", "/same/child"
			}
			paths, err := resolvePaths(opts, env)
			if valid {
				require.NoError(t, err)
				assert.Equal(t, expected, paths)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestHelperLifecycle(t *testing.T) {
	t.Parallel()
	env, opts, client := fixtureEnvironment(t)
	var output bytes.Buffer
	require.NoError(t, execute(t.Context(), opts, env, &output))
	assert.Contains(t, output.String(), "Activated microfat v0.3.0")
	output.Reset()
	require.NoError(t, execute(t.Context(), opts, env, &output))
	assert.Contains(t, output.String(), "Verified existing microfat v0.3.0")
	opts.version = "0.2.5"
	before := client.calls
	require.ErrorContains(t, execute(t.Context(), opts, env, &output), "refusing downgrade")
	assert.Equal(t, before+1, client.calls, "no download on refused downgrade")
	opts.downgrade = true
	require.NoError(t, execute(t.Context(), opts, env, &output))
	snapshot, err := install.Inspect(install.Paths{Bin: opts.bin, Store: opts.store})
	require.NoError(t, err)
	current, err := snapshot.Current()
	require.NoError(t, err)
	assert.Equal(t, "0.2.5", current.Version)
	current.Files["microfat"] = install.File{}
	second, err := snapshot.Current()
	require.NoError(t, err)
	assert.Positive(t, second.Files["microfat"].Size, "snapshot must not expose mutable facts")
	opts.uninstall, opts.cosign, opts.pin, opts.version, opts.downgrade = true, "", "", "", false
	before = client.calls
	output.Reset()
	require.NoError(t, execute(t.Context(), opts, env, &output))
	assert.Equal(t, before, client.calls, "uninstall is offline")
	assert.Contains(t, output.String(), "Removed 3 owned entrypoints")
	_, err = os.Lstat(filepath.Join(opts.bin, "microfat"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(opts.store, "current", "microfat"))
	require.NoError(t, err, "retain complete generations")
}

func TestHelperFailures(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"paths", "inspect", "verifier", "resolve", "staging", "acquire", "conflict",
		"corrupt-metadata", "output", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			env, opts, client := fixtureEnvironment(t)
			ctx := t.Context()
			var output io.Writer = io.Discard
			switch scenario {
			case "paths":
				opts.store = "relative"
			case "inspect":
				require.NoError(t, os.Mkdir(opts.store, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(opts.store, "unrelated"), nil, 0o600))
			case "verifier":
				opts.pin = ""
			case "resolve":
				client.resolveErr = io.ErrUnexpectedEOF
			case "staging":
				opts.staging = filepath.Join(opts.staging, "absent", "child")
			case "acquire":
				client.acquireErr = io.ErrUnexpectedEOF
			case "conflict":
				require.NoError(t, os.Mkdir(opts.bin, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(opts.bin, "microfat"), []byte("manual"), 0o755))
			case "corrupt-metadata":
				require.NoError(t, execute(ctx, opts, env, io.Discard))
				require.NoError(t, os.WriteFile(filepath.Join(opts.store, "current", "generation.json"), []byte("invalid"), 0o644))
			case "output":
				output = failingWriter{}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := execute(ctx, opts, env, output)
			require.Error(t, err)
			if scenario == "output" {
				require.ErrorContains(t, err, "is active, but writing installation confirmation failed")
				require.FileExists(t, filepath.Join(opts.store, "current", "microfat"))
				require.ErrorContains(t, execute(ctx, opts, env, output), "is active, but writing installation confirmation failed")
			}
			if scenario == "corrupt-metadata" {
				opts.repair = true
				require.Error(t, execute(ctx, opts, env, io.Discard), "repair must pin the target")
				opts.version = fixtureVersion
				require.NoError(t, execute(ctx, opts, env, io.Discard))
			}
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRunAndOptions(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"positional"}, {"--unknown"}, {"--uninstall", "--version", fixtureVersion},
		{"--uninstall", "--repair"}, {"--allow-downgrade"}} {
		var stderr bytes.Buffer
		assert.Equal(t, usageCode, run(t.Context(), args, environment{}, io.Discard, &stderr))
		assert.NotEmpty(t, stderr.String())
	}
	assert.Zero(t, run(t.Context(), []string{"--help"}, environment{}, io.Discard, io.Discard))
	assert.Zero(t, run(t.Context(), []string{"--helper-version"}, environment{}, io.Discard, io.Discard))
	assert.Equal(t, failureCode, run(t.Context(), []string{"--helper-version"}, environment{}, failingWriter{}, io.Discard))
	for _, args := range [][]string{{"--release-dir", "/candidate"}, {"--release-dir", "/candidate", "--uninstall"}} {
		assert.Equal(t, usageCode, run(t.Context(), args, environment{}, io.Discard, io.Discard))
	}
	env, opts, _ := fixtureEnvironment(t)
	args := []string{"--bin-dir", opts.bin, "--store-dir", opts.store, "--cosign", opts.cosign, "--cosign-sha256", opts.pin}
	if opts.system {
		args = append(args, "--system")
	}
	assert.Zero(t, run(t.Context(), args, env, io.Discard, io.Discard))
	assert.Equal(t, failureCode, run(t.Context(), args, environment{}, io.Discard, io.Discard))
	_, err := parseOptions([]string{"--uninstall", "--allow-downgrade"}, io.Discard)
	require.Error(t, err)
	assert.False(t, errors.Is(err, context.Canceled))
	assert.ErrorIs(t, argumentError{context.Canceled}, context.Canceled)
}

func TestRunSemanticArgumentErrors(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"prerelease", "metadata-version", "leading-zero-version", "unsigned-version",
		"relative-bin", "root-bin", "unclean-bin", "relative-store", "root-store", "unclean-store",
		"equal-roots", "bin-ancestor", "store-ancestor", "relative-staging", "unclean-staging",
		"relative-cosign", "unclean-cosign", "short-pin", "nonhex-pin", "uppercase-pin", "missing-cosign", "missing-pin",
		"relative-release", "unclean-release", "system-missing-bin", "system-missing-store"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			env, opts, client := fixtureEnvironment(t)
			opts.version = fixtureVersion
			// An inspection would reject this unmanaged store. Argument validation must
			// finish first, without reading installation state or acquiring a release.
			require.NoError(t, os.Mkdir(opts.store, 0o755))
			sentinel := filepath.Join(opts.store, "unmanaged")
			require.NoError(t, os.WriteFile(sentinel, []byte("untouched"), 0o600))
			originalBin, originalStore := opts.bin, opts.store
			var expected string
			switch scenario {
			case "prerelease":
				opts.version, expected = "0.3.0-rc1", "expected a stable version"
			case "metadata-version":
				opts.version, expected = "0.3.0+build", "expected a stable version"
			case "leading-zero-version":
				opts.version, expected = "0.03.0", "expected a stable version"
			case "unsigned-version":
				opts.version, expected = "0.2.2", "release signing policy"
			case "relative-bin":
				opts.bin = "relative-bin"
			case "root-bin":
				opts.bin = "/"
			case "unclean-bin":
				opts.bin += "/../bin"
			case "relative-store":
				opts.store = "relative-store"
			case "root-store":
				opts.store = "/"
			case "unclean-store":
				opts.store += "/../store"
			case "equal-roots":
				opts.bin, expected = opts.store, "must not overlap"
			case "bin-ancestor":
				opts.bin, expected = opts.staging, "must not overlap"
			case "store-ancestor":
				opts.store, expected = opts.staging, "must not overlap"
			case "relative-staging":
				opts.staging, expected = "relative-staging", "--staging-dir must be a clean absolute path"
			case "unclean-staging":
				opts.staging, expected = opts.staging+"/../staging", "--staging-dir must be a clean absolute path"
			case "relative-cosign":
				opts.cosign, expected = "cosign", "--cosign must be a clean absolute path"
			case "unclean-cosign":
				opts.cosign, expected = "/trusted/../cosign", "--cosign must be a clean absolute path"
			case "short-pin":
				opts.pin, expected = "aa", "invalid SHA-256 trust pin"
			case "nonhex-pin":
				opts.pin, expected = strings.Repeat("g", 64), "invalid SHA-256 trust pin"
			case "uppercase-pin":
				opts.pin, expected = strings.ToUpper(opts.pin), "invalid SHA-256 trust pin"
			case "missing-cosign":
				opts.cosign, expected = "", "supply --cosign and --cosign-sha256"
			case "missing-pin":
				opts.pin, expected = "", "supply --cosign and --cosign-sha256"
			case "relative-release":
				opts.releaseDir, expected = "release", "--release-dir must be a clean absolute path"
			case "unclean-release":
				opts.releaseDir, expected = "/release/../release", "--release-dir must be a clean absolute path"
			case "system-missing-bin":
				opts.system, opts.bin, expected = true, "", "--system requires explicit --bin-dir and --store-dir"
			case "system-missing-store":
				opts.system, opts.store, expected = true, "", "--system requires explicit --bin-dir and --store-dir"
			}
			if expected == "" {
				expected = "installation roots must be clean absolute non-root paths"
			}
			var stdout, stderr bytes.Buffer
			assert.Equal(t, usageCode, run(t.Context(), helperArguments(opts), env, &stdout, &stderr))
			assert.Empty(t, stdout.String())
			assert.Contains(t, stderr.String(), expected)
			assert.Zero(t, client.calls)
			data, err := os.ReadFile(sentinel)
			require.NoError(t, err)
			assert.Equal(t, "untouched", string(data))
			entries, err := os.ReadDir(originalStore)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			assert.Equal(t, "unmanaged", entries[0].Name())
			assert.NoDirExists(t, originalBin)
		})
	}
}

func TestRunOperationalFailuresKeepFailureCode(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"platform", "system-not-root", "root-policy", "home", "inspect", "resolve", "staging",
		"acquire", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			env, opts, client := fixtureEnvironment(t)
			ctx := t.Context()
			var expectedCalls int
			switch scenario {
			case "platform":
				env.goos = "darwin"
			case "system-not-root":
				opts.system, env.uid = true, 1000
			case "root-policy":
				opts.system, env.uid = false, 0
			case "home":
				opts.system, opts.bin, env.uid = false, "", 1000
				env.home = func() (string, error) { return "", io.ErrUnexpectedEOF }
			case "inspect":
				require.NoError(t, os.Mkdir(opts.store, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(opts.store, "unmanaged"), []byte("untouched"), 0o600))
			case "resolve":
				client.resolveErr, expectedCalls = io.ErrUnexpectedEOF, 1
			case "staging":
				opts.staging, expectedCalls = filepath.Join(opts.staging, "absent"), 1
			case "acquire":
				client.acquireErr, expectedCalls = errors.New("signature verification failed"), 2
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				expectedCalls = 2
			}
			var stdout, stderr bytes.Buffer
			assert.Equal(t, failureCode, run(ctx, helperArguments(opts), env, &stdout, &stderr))
			assert.Empty(t, stdout.String())
			assert.NotEmpty(t, stderr.String())
			assert.Equal(t, expectedCalls, client.calls)
			assert.NoFileExists(t, filepath.Join(opts.store, "current", "microfat"))
		})
	}
}

func helperArguments(opts options) []string {
	args := []string{"--bin-dir", opts.bin, "--store-dir", opts.store, "--version", opts.version,
		"--cosign", opts.cosign, "--cosign-sha256", opts.pin, "--staging-dir", opts.staging, "--release-dir", opts.releaseDir}
	if opts.system {
		args = append(args, "--system")
	}
	return args
}

func TestLocalReleaseDirectoryDoesNotFallBackToNetwork(t *testing.T) {
	t.Parallel()
	for _, directory := range []string{"relative-release", t.TempDir()} {
		env, opts, client := fixtureEnvironment(t)
		opts.version, opts.releaseDir = fixtureVersion, directory
		require.Error(t, execute(t.Context(), opts, env, io.Discard))
		assert.Zero(t, client.calls)
		assert.NoDirExists(t, opts.store)
	}
}

func TestFirstActivationReportsUnwritableEntrypointsAndRecovers(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("requires ordinary-user permissions")
	}
	env, opts, _ := fixtureEnvironment(t)
	require.NoError(t, os.Mkdir(opts.bin, 0o500))
	t.Cleanup(func() { _ = os.Chmod(opts.bin, 0o755) })
	err := execute(t.Context(), opts, env, io.Discard)
	require.ErrorContains(t, err, "v0.3.0 was activated, but installation completion failed")
	require.FileExists(t, filepath.Join(opts.store, "current", "microfat"))
	require.NoFileExists(t, filepath.Join(opts.bin, "microfat"))
	require.NoError(t, os.Chmod(opts.bin, 0o755))
	var output bytes.Buffer
	require.NoError(t, execute(t.Context(), opts, env, &output))
	assert.Contains(t, output.String(), "Verified existing microfat v0.3.0")
	require.FileExists(t, filepath.Join(opts.bin, "microfat"))
}
