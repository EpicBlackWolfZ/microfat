//go:build linux

package update

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/install"
	"github.com/EpicBlackWolfZ/microfat/internal/installrelease"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureDowngrade = "downgrade"

type fixtureClient struct {
	t            *testing.T
	target       string
	calls        []string
	fail         string
	afterLookup  func()
	afterAcquire func()
	verify       bool
}

func fixtureGeneration(t *testing.T, version, directory string) install.Generation {
	t.Helper()
	generation := install.Generation{Schema: 1, ID: install.NewID(), Version: version, Arch: runtime.GOARCH,
		ArchiveSHA256: strings.Repeat("a", 64), Files: map[string]install.File{}}
	for _, name := range install.Products() {
		data := []byte("#!/bin/sh\necho " + version + "-" + name + "\n")
		require.NoError(t, os.WriteFile(filepath.Join(directory, name), data, 0o755))
		generation.Files[name] = install.File{Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	}
	return generation
}

func (c *fixtureClient) Published(_ context.Context, requested, _ string) (string, error) {
	c.calls = append(c.calls, "lookup")
	if c.afterLookup != nil {
		c.afterLookup()
	}
	if c.fail == "lookup" {
		return "", errors.New("lookup failed")
	}
	if requested != "" {
		return installrelease.ParseVersion(requested)
	}
	return c.target, nil
}
func (c *fixtureClient) PrepareVerifier(_ context.Context, _, _ string, override installrelease.Cosign) (installrelease.Cosign, error) {
	c.calls = append(c.calls, "verifier")
	if c.fail == "verifier" {
		return override, errors.New("verifier failed")
	}
	return override, nil
}
func (c *fixtureClient) Acquire(
	_ context.Context, version, _, stage string, verifier installrelease.Verifier,
) (install.Generation, string, error) {
	c.calls = append(c.calls, "acquire")
	if c.fail == "acquire" {
		return install.Generation{}, "", errors.New("authentication failed")
	}
	if c.verify {
		checksums, bundle := filepath.Join(stage, "checksums.txt"), filepath.Join(stage, "checksums.txt.sig")
		if err := verifier.Verify(context.Background(), version, checksums, bundle); err != nil {
			return install.Generation{}, "", err
		}
	}
	generation := fixtureGeneration(c.t, version, stage)
	if c.afterAcquire != nil {
		c.afterAcquire()
	}
	return generation, stage, nil
}

func updateFixture(t *testing.T) (*Service, Options, *fixtureClient, install.Paths, string) {
	t.Helper()
	root := t.TempDir()
	paths := install.Paths{Bin: filepath.Join(root, "bin space"), Store: filepath.Join(root, "store space")}
	snapshot, err := install.Inspect(paths)
	require.NoError(t, err)
	source := t.TempDir()
	generation := fixtureGeneration(t, "0.3.0", source)
	_, err = install.Apply(t.Context(), snapshot, generation, source, install.ApplyOptions{})
	require.NoError(t, err)
	physical, err := filepath.EvalSymlinks(filepath.Join(paths.Bin, "microfat"))
	require.NoError(t, err)
	client := &fixtureClient{t: t, target: "0.3.1"}
	service := NewService("v0.3.0")
	service.executable = func() (string, error) { return physical, nil }
	service.client = client
	opts := Options{System: os.Geteuid() == 0, Staging: root}
	return service, opts, client, paths, physical
}

func TestUpdateSelectionAndExecution(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"check-newer", "check-equal", "check-older", "check-selected-older", "update", "current",
		"installed-newer",
		"downgrade-denied", fixtureDowngrade, "lookup", "verifier", "acquire", "apply", "post-activation", "staging"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			service, opts, client, paths, physical := updateFixture(t)
			if strings.HasPrefix(scenario, "check-") {
				opts = Options{Check: true}
			}
			switch scenario {
			case "check-equal", "current":
				client.target = "0.3.0"
			case "check-older", "installed-newer":
				client.target = "0.2.5"
			case "check-selected-older", "downgrade-denied", fixtureDowngrade:
				opts.Version = "v0.2.5"
				opts.AllowDowngrade = scenario == fixtureDowngrade
			case "lookup", "verifier", "acquire":
				client.fail = scenario
			case "apply":
				service.apply = func(context.Context, install.Snapshot, install.Generation, string, install.ApplyOptions) (install.Result, error) {
					return install.Result{}, errors.New("apply failed")
				}
			case "post-activation":
				service.apply = func(
					ctx context.Context, snapshot install.Snapshot, generation install.Generation, source string, opts install.ApplyOptions,
				) (install.Result, error) {
					result, err := install.Apply(ctx, snapshot, generation, source, opts)
					require.NoError(t, err)
					return result, errors.New("durability failed")
				}
			case "staging":
				opts.Staging = filepath.Join(t.TempDir(), "absent")
			}
			old, err := os.Readlink(filepath.Join(paths.Store, "current"))
			require.NoError(t, err)
			result, err := service.Run(t.Context(), opts)
			success := strings.HasPrefix(scenario, "check-") || scenario == "update" || scenario == "current" ||
				scenario == "installed-newer" || scenario == fixtureDowngrade
			if success {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Equal(t, "error", result.Status)
				assert.NotEmpty(t, result.Error)
			}
			changed := scenario == "update" || scenario == fixtureDowngrade || scenario == "post-activation"
			assert.Equal(t, changed, result.Activated)
			after, readErr := os.Readlink(filepath.Join(paths.Store, "current"))
			require.NoError(t, readErr)
			if changed {
				assert.NotEqual(t, old, after)
				assert.Equal(t, artifactVerified, result.Verification)
				assert.False(t, result.CanSelfUpdate)
			} else {
				assert.Equal(t, old, after)
			}
			assert.FileExists(t, physical, "old generation remains")
			if opts.Check || scenario == "current" || scenario == "installed-newer" || scenario == "downgrade-denied" {
				assert.Equal(t, []string{"lookup"}, client.calls)
			}
			if opts.Check {
				assert.Equal(t, metadataOnly, result.Verification)
				assert.Equal(t, scenario == "check-newer", result.UpdateAvailable)
			}
			if scenario == fixtureDowngrade {
				assert.Equal(t, "downgraded", result.Status)
				assert.Equal(t, "0.2.5", *result.CurrentVersion)
			}
			if scenario == "post-activation" {
				assert.Contains(t, err.Error(), "is active")
			}
			if scenario == "update" {
				assert.Equal(t, "updated", result.Status)
				assert.False(t, result.UpdateAvailable)
			}
			matches, matchErr := filepath.Glob(filepath.Join(opts.Staging, "microfat-install-*"))
			require.NoError(t, matchErr)
			assert.Empty(t, matches)
		})
	}
}

func TestUpdatePreflightAndConflicts(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"os", "arch", "dev", "resolve", "version-mismatch", "arch-mismatch", "stale",
		"foreign-owner", "root-consent",
		"system-nonroot", "corrupt", "missing-link", "concurrent-activation", "cancelled", "bad-options"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			service, opts, client, paths, physical := updateFixture(t)
			ctx := t.Context()
			activate := func() {
				snapshot, err := install.Inspect(paths)
				require.NoError(t, err)
				source := t.TempDir()
				_, err = install.Apply(t.Context(), snapshot, fixtureGeneration(t, "0.3.2", source), source, install.ApplyOptions{})
				require.NoError(t, err)
			}
			switch scenario {
			case "os":
				service.goos = "darwin"
			case "arch":
				service.arch = "riscv64"
			case "dev":
				service.version = "dev"
			case "resolve":
				service.executable = func() (string, error) { return "", errors.New("no procfs") }
			case "version-mismatch":
				service.version = "0.3.9"
			case "arch-mismatch":
				if runtime.GOARCH == "amd64" {
					service.arch = "arm64"
				} else {
					service.arch = "amd64"
				}
			case "stale":
				activate()
			case "foreign-owner":
				service.uid = os.Geteuid() + 1
				opts.System = false
			case "root-consent":
				service.uid = 0
				opts.System = false
			case "system-nonroot":
				service.uid = 1000
				opts.System = true
			case "corrupt":
				require.NoError(t, os.WriteFile(physical, []byte("corrupt"), 0o755))
			case "missing-link":
				require.NoError(t, os.Remove(filepath.Join(paths.Bin, "microfat-stub")))
			case "concurrent-activation":
				client.afterAcquire = activate
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "bad-options":
				opts.AllowDowngrade = true
			}
			result, err := service.Run(ctx, opts)
			require.Error(t, err)
			assert.False(t, result.Activated)
			if scenario != "concurrent-activation" {
				assert.Empty(t, client.calls)
			}
			if scenario == "stale" || scenario == "foreign-owner" {
				result, err = service.Run(t.Context(), Options{Check: true})
				require.NoError(t, err)
				assert.False(t, result.CanSelfUpdate)
				assert.NotEmpty(t, result.Guidance)
			}
			if scenario == "concurrent-activation" {
				assert.ErrorIs(t, err, install.ErrChanged)
			}
		})
	}
}

func TestUpdateOptions(t *testing.T) {
	t.Parallel()
	for _, opts := range []Options{{Version: "0.3.0-rc1"}, {Version: "0.2.2"}, {AllowDowngrade: true}, {Cosign: "/file"},
		{CosignSHA256: "pin"},
		{Check: true, System: true}, {Check: true, Staging: "/tmp"}, {Check: true, Cosign: "/file", CosignSHA256: "pin"},
		{Check: true, Version: "0.2.5", AllowDowngrade: true}} {
		require.Error(t, opts.Validate())
	}
	for _, opts := range []Options{{}, {Check: true}, {Version: "v0.3.0"}, {Version: "0.2.5", AllowDowngrade: true}} {
		require.NoError(t, opts.Validate())
	}
}

func TestExternalChecksAndRefusal(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"manual", "homebrew", "bad-marker", "unknown-owner", "unknown-schema", "unknown-field",
		"mode", "empty", "huge", "directory", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			service, _, client, _, _ := updateFixture(t)
			directory := t.TempDir()
			physical := filepath.Join(directory, "microfat")
			require.NoError(t, os.WriteFile(physical, []byte("native"), 0o755))
			service.executable = func() (string, error) { return physical, nil }
			marker := filepath.Join(directory, distributionFile)
			content := `{"schema":1,"owner":"homebrew"}`
			switch scenario {
			case "bad-marker":
				content = "{"
			case "unknown-owner":
				content = `{"schema":1,"owner":"apt"}`
			case "unknown-schema":
				content = `{"schema":2,"owner":"homebrew"}`
			case "unknown-field":
				content = `{"schema":1,"owner":"homebrew","command":"bad"}`
			case "empty":
				content = ""
			case "huge":
				content = strings.Repeat("x", distributionLimit+1)
			}
			if scenario != "manual" {
				require.NoError(t, os.WriteFile(marker, []byte(content), 0o644))
			}
			if scenario == "mode" {
				require.NoError(t, os.Chmod(marker, 0o666))
			}
			if scenario == "directory" {
				require.NoError(t, os.Remove(marker))
				require.NoError(t, os.Mkdir(marker, 0o755))
			}
			if scenario == "symlink" {
				require.NoError(t, os.Rename(marker, marker+"-old"))
				require.NoError(t, os.Symlink(marker+"-old", marker))
			}
			result, err := service.Run(t.Context(), Options{Check: true})
			if scenario == "manual" || scenario == "homebrew" {
				require.NoError(t, err)
				assert.True(t, result.UpdateAvailable)
				assert.False(t, result.CanSelfUpdate)
				assert.Contains(t, result.Guidance, "brew upgrade microfat")
				data, err := json.Marshal(result)
				require.NoError(t, err)
				assert.Contains(t, string(data), `"verification":"metadata_only"`)
			} else {
				require.Error(t, err)
				assert.Empty(t, client.calls)
			}
			client.calls = nil
			_, err = service.Run(t.Context(), Options{})
			require.Error(t, err)
			assert.Empty(t, client.calls)
		})
	}
}

func TestUpdateNoexecVerifier(t *testing.T) {
	parent := os.Getenv("MICROFAT_TEST_NOEXEC_PARENT")
	if parent == "" {
		t.Skip("real disposable noexec mount is required by native qualification")
	}
	directory, err := os.MkdirTemp(parent, "microfat-update-noexec-")
	require.NoError(t, err)
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "cosign")
	data := []byte("#!/bin/sh\nexit 0\n")
	require.NoError(t, os.WriteFile(path, data, 0o700))
	service, opts, client, paths, _ := updateFixture(t)
	client.verify = true
	opts.Cosign, opts.CosignSHA256 = path, fmt.Sprintf("%x", sha256.Sum256(data))
	before, err := os.Readlink(filepath.Join(paths.Store, "current"))
	require.NoError(t, err)
	result, err := service.Run(t.Context(), opts)
	require.ErrorContains(t, err, "verifier execution denied")
	assert.False(t, result.Activated)
	after, err := os.Readlink(filepath.Join(paths.Store, "current"))
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestSourceChangesDuringAcquisition(t *testing.T) {
	t.Parallel()
	for _, cause := range []string{"path", "error"} {
		t.Run(cause, func(t *testing.T) {
			t.Parallel()
			service, opts, client, paths, _ := updateFixture(t)
			before, err := os.Readlink(filepath.Join(paths.Store, "current"))
			require.NoError(t, err)
			client.afterAcquire = func() {
				service.executable = func() (string, error) {
					if cause == "error" {
						return "", errors.New("source was replaced")
					}
					return "/different/physical/path", nil
				}
			}
			result, err := service.Run(t.Context(), opts)
			require.ErrorIs(t, err, install.ErrChanged)
			assert.False(t, result.Activated)
			after, err := os.Readlink(filepath.Join(paths.Store, "current"))
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

func TestDistributionParentIsNotDirectory(t *testing.T) {
	t.Parallel()
	parent := filepath.Join(t.TempDir(), "regular-file")
	require.NoError(t, os.WriteFile(parent, []byte("unrelated"), 0o600))
	_, err := externalManagement(filepath.Join(parent, "microfat"))
	require.Error(t, err)
}

func TestCapabilityAfterActivation(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"success", "completion-error"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			service, opts, _, _, _ := updateFixture(t)
			if scenario == "completion-error" {
				service.apply = func(
					ctx context.Context, snapshot install.Snapshot, generation install.Generation, source string, opts install.ApplyOptions,
				) (install.Result, error) {
					result, err := install.Apply(ctx, snapshot, generation, source, opts)
					require.NoError(t, err)
					return result, errors.New("completion failed after activation")
				}
			}
			result, err := service.Run(t.Context(), opts)
			if scenario == "success" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "is active")
			}
			require.True(t, result.Activated)
			assert.False(t, result.CanSelfUpdate, "the running process now belongs to a retained generation")
			assert.NotEqual(t, *result.RunningVersion, *result.CurrentVersion)
			rechecked, err := service.Run(t.Context(), Options{Check: true})
			require.NoError(t, err)
			assert.Equal(t, rechecked.CanSelfUpdate, result.CanSelfUpdate)
		})
	}
}
