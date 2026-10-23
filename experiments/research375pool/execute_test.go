package main

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	launchTestFamily = "demo"
	launchTestReady  = "{\"status\":\"ready\",\"version\":\"1.0.0\"}\n"
	launchTestOutput = "Version: dev (Go: go1.27.3, linux/amd64)\n"
	launchHelperEnv  = "RESEARCH375POOL_LAUNCH_HELPER"
	launchTestFault  = "launch fixture fault"
)

func nativeLaunchFixture(t *testing.T) (corpusManifest, corpusVariant) {
	t.Helper()
	var m corpusManifest
	for _, level := range []string{microarch.AMD64v1, microarch.AMD64v2, microarch.AMD64v3} {
		path := filepath.Join(t.TempDir(), level)
		require.NoError(t, os.WriteFile(path, []byte("trusted fixture "+level), 0o755))
		hash, size, err := digestFile(path, maxInput)
		require.NoError(t, err)
		m.Variants = append(m.Variants, corpusVariant{Family: launchTestFamily, Level: level, Path: path, SHA256: hash, Bytes: size})
	}
	return m, m.Variants[len(m.Variants)-1]
}

func nativeLaunchTestHost() microarch.Info {
	return microarch.Info{OS: "linux", Arch: microarch.ArchAMD64, Level: microarch.AMD64v3}
}

func nativeLaunchTestDiagnostic(t *testing.T, mode string, fixture corpusVariant) string {
	t.Helper()
	if mode == format.ExecModeAuto {
		mode = format.ExecModeCache
	}
	data, err := json.Marshal(format.DispatchTelemetry{
		Event: format.EventDispatch, HostArch: microarch.ArchAMD64, HostLevel: microarch.AMD64v3,
		SelectedVariant: fixture.Level, SelectedSHA256: fixture.SHA256, SelectedSizeBytes: fixture.Bytes, ExecMode: mode,
	})
	require.NoError(t, err)
	return "[microfat] " + string(data) + "\n"
}

func nativeLaunchTestEnvValue(env []string, key string) string {
	for _, value := range env {
		if suffix, found := strings.CutPrefix(value, key+"="); found {
			return suffix
		}
	}
	return ""
}

func nativeLaunchTestHooks(t *testing.T, fixture corpusVariant) nativeLaunchHooks {
	t.Helper()
	return nativeLaunchHooks{
		host: nativeLaunchTestHost,
		run: func(_ context.Context, command string, args, env []string) (string, string, error) {
			assert.Equal(t, []string{"--startup-only", "--json"}, args)
			assert.Contains(t, env, "GOMAXPROCS=4")
			mode := nativeLaunchTestEnvValue(env, format.EnvExecMode)
			if command == fixture.Path {
				assert.Empty(t, mode)
				return launchTestReady, "", nil
			}
			return launchTestReady, nativeLaunchTestDiagnostic(t, mode, fixture), nil
		},
		prewarm: func(_, level, dir string) error {
			assert.Equal(t, fixture.Level, level)
			info, err := os.Stat(dir)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
			return nil
		},
	}
}

func TestNativeLaunchMatrixModesAndOutput(t *testing.T) {
	t.Parallel()
	m, fixture := nativeLaunchFixture(t)
	root := t.TempDir()
	observations, err := verifyNativeLaunchesWithHooks(context.Background(), "artifact", m, launchTestFamily, root,
		nativeLaunchTestHooks(t, fixture))
	require.NoError(t, err)
	require.Len(t, observations, 4)
	for i, mode := range []string{nativeLaunchMode, format.ExecModeMemfd, format.ExecModeCache, format.ExecModeAuto} {
		observation := observations[i]
		assert.Equal(t, mode, observation.RequestedMode)
		assert.True(t, observation.OutputMatched)
		assert.Equal(t, fixture.Level, observation.SelectedVariant)
		assert.Equal(t, fixture.SHA256, observation.SelectedSHA256)
		assert.Equal(t, mode == format.ExecModeAuto, observation.Prewarmed)
		if mode == format.ExecModeAuto {
			assert.Equal(t, format.ExecModeCache, observation.ExecMode)
		}
		if observation.CacheDir != "" {
			_, err := os.Stat(observation.CacheDir)
			assert.ErrorIs(t, err, os.ErrNotExist, "temporary private launch cache is cleaned")
		}
	}
	contents, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, contents)
}

func TestNativeLaunchRejectsUnknownOrForeignFixtures(t *testing.T) {
	t.Parallel()
	m, _ := nativeLaunchFixture(t)
	_, err := nativeFixtureArgs("unknown")
	require.Error(t, err)
	args, err := nativeFixtureArgs("microfat")
	require.NoError(t, err)
	assert.Equal(t, []string{"--version"}, args)
	_, err = verifyNativeLaunches(context.Background(), "artifact", m, "unknown", t.TempDir())
	require.Error(t, err)
	for _, tc := range []struct {
		name string
		host microarch.Info
	}{
		{"foreign OS", microarch.Info{OS: "darwin", Arch: microarch.ArchAMD64, Level: microarch.AMD64v3}},
		{"foreign arch", microarch.Info{OS: "linux", Arch: microarch.ArchARM64, Level: microarch.ARM64v8_0}},
		{"unknown level", microarch.Info{OS: "linux", Arch: microarch.ArchAMD64, Level: "unknown"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := selectNativeFixture(m, launchTestFamily, tc.host)
			require.Error(t, err)
		})
	}
	for _, level := range []string{microarch.AMD64v1, microarch.AMD64v2, microarch.AMD64v3} {
		host := nativeLaunchTestHost()
		host.Level = level
		fixture, err := selectNativeFixture(m, launchTestFamily, host)
		require.NoError(t, err)
		assert.Equal(t, level, fixture.Level)
	}
	bad := m
	bad.Variants = slices.Clone(m.Variants)
	bad.Variants[0].Level = microarch.AMD64v4
	_, err = selectNativeFixture(bad, launchTestFamily, nativeLaunchTestHost())
	require.Error(t, err)
	bad.Variants = append(slices.Clone(m.Variants), m.Variants[0])
	_, err = selectNativeFixture(bad, launchTestFamily, nativeLaunchTestHost())
	require.Error(t, err)
	_, err = selectNativeFixture(corpusManifest{}, launchTestFamily, nativeLaunchTestHost())
	require.Error(t, err)
}

func TestNativeLaunchFailuresRetainAttempt(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"native-error", "native-output", "memfd-error", "wrong-mode", "wrong-output", "prewarm"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			m, fixture := nativeLaunchFixture(t)
			hooks := nativeLaunchTestHooks(t, fixture)
			original := hooks.run
			failure := errors.New(launchTestFault)
			hooks.run = func(ctx context.Context, command string, args, env []string) (string, string, error) {
				stdout, stderr, err := original(ctx, command, args, env)
				mode := nativeLaunchTestEnvValue(env, format.EnvExecMode)
				switch {
				case fault == "native-error" && command == fixture.Path, fault == "memfd-error" && mode == format.ExecModeMemfd:
					err = failure
				case fault == "native-output" && command == fixture.Path, fault == "wrong-output" && mode == format.ExecModeMemfd:
					stdout = "unexpected output"
				case fault == "wrong-mode" && mode == format.ExecModeMemfd:
					stderr = nativeLaunchTestDiagnostic(t, format.ExecModeCache, fixture)
				}
				return stdout, stderr, err
			}
			if fault == "prewarm" {
				hooks.prewarm = func(string, string, string) error { return failure }
			}
			observations, err := verifyNativeLaunchesWithHooks(context.Background(), "artifact", m, launchTestFamily, t.TempDir(), hooks)
			require.Error(t, err)
			require.NotEmpty(t, observations)
			assert.NotEmpty(t, observations[len(observations)-1].Error)
			assert.False(t, observations[len(observations)-1].OutputMatched)
		})
	}
}

func TestNativeLaunchManifestAndDirectoryFailures(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"foreign", "missing-file", "changed-hash", "missing-parent", "blocked-cache"} {
		t.Run(fault, func(t *testing.T) {
			m, fixture := nativeLaunchFixture(t)
			hooks := nativeLaunchTestHooks(t, fixture)
			dir := t.TempDir()
			switch fault {
			case "foreign":
				hooks.host = func() microarch.Info { return microarch.Info{} }
			case "missing-file":
				require.NoError(t, os.Remove(fixture.Path))
			case "changed-hash":
				require.NoError(t, os.WriteFile(fixture.Path, []byte("changed"), 0o755))
			case "missing-parent":
				dir = filepath.Join(dir, fixtureMissing)
			case "blocked-cache":
				original := hooks.run
				hooks.run = func(ctx context.Context, command string, args, env []string) (string, string, error) {
					cache := nativeLaunchTestEnvValue(env, format.EnvCacheDir)
					if nativeLaunchTestEnvValue(env, format.EnvExecMode) == format.ExecModeMemfd {
						require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(cache), format.ExecModeCache), []byte("block"), 0o600))
					}
					return original(ctx, command, args, env)
				}
			}
			_, err := verifyNativeLaunchesWithHooks(context.Background(), "artifact", m, launchTestFamily, dir, hooks)
			require.Error(t, err)
		})
	}
}

func TestNativeLaunchOutputAndDiagnosticValidation(t *testing.T) {
	t.Parallel()
	require.NoError(t, validateNativeFixtureOutput(launchTestFamily, launchTestReady))
	require.NoError(t, validateNativeFixtureOutput("microfat", launchTestOutput))
	for _, output := range []string{"not JSON", `{}`, `{"status":"starting","version":"1"}`, `{"status":"ready"}`} {
		require.Error(t, validateNativeFixtureOutput(launchTestFamily, output))
	}
	require.Error(t, validateNativeFixtureOutput("microfat", "Version: dev windows/amd64"))
	_, fixture := nativeLaunchFixture(t)
	faults := []string{fixtureMissing, "bad-json", "wrong-event", "duplicate", "wrong-host", "wrong-hash", "wrong-size", "wrong-level"}
	for _, fault := range faults {
		t.Run(fault, func(t *testing.T) {
			stderr := nativeLaunchTestDiagnostic(t, format.ExecModeMemfd, fixture)
			switch fault {
			case fixtureMissing:
				stderr = "ordinary payload stderr\n"
			case "bad-json":
				stderr = "[microfat] broken"
			case "wrong-event":
				stderr = strings.ReplaceAll(stderr, format.EventDispatch, format.EventError)
			case "duplicate":
				stderr += stderr
			case "wrong-host":
				stderr = strings.ReplaceAll(stderr, microarch.ArchAMD64, microarch.ArchARM64)
			case "wrong-hash":
				stderr = strings.ReplaceAll(stderr, fixture.SHA256, "other-hash")
			case "wrong-size":
				stderr = strings.ReplaceAll(stderr, fmt.Sprintf(`"selected_size_bytes":%d`, fixture.Bytes), `"selected_size_bytes":1`)
			case "wrong-level":
				stderr = strings.ReplaceAll(stderr, `"selected_variant":"v3"`, `"selected_variant":"v1"`)
			}
			observation := launchObservation{RequestedMode: format.ExecModeMemfd, ExpectedVariant: fixture.Level,
				ExpectedSHA256: fixture.SHA256, ExpectedSizeBytes: fixture.Bytes, Stdout: launchTestReady, Stderr: stderr}
			require.Error(t, validateNativeDispatch(&observation, launchTestReady))
		})
	}
}

func TestNativeLaunchPrewarmChecks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	opts := pack.DefaultOptions()
	opts.SkipELFValidation, opts.Compression = true, codec.AlgorithmNone
	opts.StubPath, opts.OutputPath = filepath.Join(dir, "stub"), filepath.Join(dir, "artifact")
	opts.Variants = map[string]string{microarch.AMD64v1: filepath.Join(dir, "payload")}
	require.NoError(t, os.WriteFile(opts.StubPath, []byte("stub"), 0o755))
	require.NoError(t, os.WriteFile(opts.Variants[microarch.AMD64v1], []byte("payload"), 0o755))
	_, err := pack.Pack(opts)
	require.NoError(t, err)
	require.NoError(t, prewarmNativeLaunch(opts.OutputPath, microarch.AMD64v1, filepath.Join(dir, "cache")))
	require.Error(t, prewarmNativeLaunch(opts.OutputPath, microarch.AMD64v2, filepath.Join(dir, "cache")))
	require.Error(t, prewarmNativeLaunch(filepath.Join(dir, fixtureMissing), microarch.AMD64v1, filepath.Join(dir, "cache")))
}

func TestNativeLaunchHelperProcess(t *testing.T) {
	mode := os.Getenv(launchHelperEnv)
	if mode == "" {
		return
	}
	switch mode {
	case fixtureSuccess:
		_, _ = fmt.Fprint(os.Stdout, launchTestReady)
		_, _ = fmt.Fprint(os.Stderr, "helper stderr")
	case "flood":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), nativeLaunchLimit+1))
	case "sleep":
		time.Sleep(nativeLaunchTimeout)
	case fixtureFailure:
		_, _ = fmt.Fprint(os.Stderr, launchTestFault)
		os.Exit(3)
	}
	os.Exit(0)
}

func TestNativeLaunchOwnedChildAndCaptureBounds(t *testing.T) {
	t.Parallel()
	executable, err := os.Executable()
	require.NoError(t, err)
	args := []string{"-test.run=^TestNativeLaunchHelperProcess$"}
	for _, mode := range []string{fixtureSuccess, "flood", fixtureFailure, "sleep"} {
		t.Run(mode, func(t *testing.T) {
			env := append(nativeLaunchEnv("", ""), launchHelperEnv+"="+mode, "GORACE=atexit_sleep_ms=0", "GOCOVERDIR="+t.TempDir())
			var stdout, stderr string
			var runErr error
			if mode == "sleep" {
				stdout, stderr, runErr = runNativeLaunchWithTimeout(context.Background(), executable, args, env, time.Second)
				require.ErrorIs(t, runErr, context.DeadlineExceeded)
			} else {
				stdout, stderr, runErr = runNativeLaunch(context.Background(), executable, args, env)
			}
			switch mode {
			case fixtureSuccess:
				require.NoError(t, runErr)
				assert.Equal(t, launchTestReady, stdout)
				assert.Equal(t, "helper stderr", stderr)
			case "flood":
				require.ErrorContains(t, runErr, "capture bound")
				assert.Len(t, stdout, nativeLaunchLimit)
			case fixtureFailure:
				require.Error(t, runErr)
				assert.Equal(t, launchTestFault, stderr)
			}
		})
	}
	_, _, err = runNativeLaunch(context.Background(), filepath.Join(t.TempDir(), fixtureMissing), nil, nativeLaunchEnv("", ""))
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = runNativeLaunch(ctx, executable, args, nativeLaunchEnv("", ""))
	require.ErrorIs(t, err, context.Canceled)
}
