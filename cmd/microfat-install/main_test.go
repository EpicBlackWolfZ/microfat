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

type fixtureClient struct {
	t                      *testing.T
	resolveErr, acquireErr error
	calls                  int
}

func (c *fixtureClient) Resolve(_ context.Context, version string) (string, error) {
	c.calls++
	if version == "" {
		version = "0.3.0"
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
	opts.uninstall, opts.cosign, opts.pin = true, "", ""
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
				opts.version = "0.3.0"
				require.NoError(t, execute(ctx, opts, env, io.Discard))
			}
		})
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRunAndOptions(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"positional"}, {"--unknown"}, {"--uninstall", "--version", "0.3.0"},
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
	assert.Equal(t, failureCode, run(t.Context(), nil, environment{}, io.Discard, io.Discard))
	_, err := parseOptions([]string{"--uninstall", "--allow-downgrade"}, io.Discard)
	require.Error(t, err)
	assert.False(t, errors.Is(err, context.Canceled))
}
