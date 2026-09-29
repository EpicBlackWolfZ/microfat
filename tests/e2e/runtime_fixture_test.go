//go:build linux

package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/runtimequalify"
	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"github.com/stretchr/testify/require"
)

func runtimeFixtureBackend(t *testing.T) string {
	t.Helper()
	if os.Getenv("MICROFAT_RUNTIME_FIXTURES") != mountRequired {
		t.Skip("run task test-runtime-fixtures for native runtime controls")
	}
	backend := os.Getenv(mountBackendEnv)
	if backend == "" {
		backend = "userns"
	}
	require.Empty(t, mountPreflight(backend), "required runtime fixture prerequisites")
	return backend
}

// This source regression deliberately uses the existing E2E product setup.
// Candidate qualification is a separate controller and never enters TestMain.
func TestRuntimeFixtureTeardown(t *testing.T) {
	backend := runtimeFixtureBackend(t)
	p := buildMountProducts(t, filepath.Join(t.TempDir(), "products"))
	for _, mode := range []string{"--hang", "--spawn-detached"} {
		t.Run(mode, func(t *testing.T) {
			req := prepareMountRequest(t, p, p.reporter, "directory", execModeNative)
			req.Args = []string{mode}
			req.Runtime = &mountfixture.Runtime{Workers: 1}
			assertFixtureTimeoutCleanup(t, req, mountNamespaceCommand(backend, p.controller, filepath.Join(req.Root, "request.json")))
		})
	}
}

func TestRuntimeFixtureFatCLI(t *testing.T) {
	backend := runtimeFixtureBackend(t)
	output := t.TempDir()
	p := buildMountProducts(t, filepath.Join(output, "products"))
	// Keep a real CLI as the payload. Padding extends inspection beyond Go's
	// preemption interval even on fast runners, without changing its ELF code.
	const payloadBytes = 64 << 20
	stat, err := os.Stat(p.cli)
	require.NoError(t, err)
	require.Less(t, stat.Size(), int64(payloadBytes))
	require.NoError(t, os.Truncate(p.cli, payloadBytes))
	p.reporter = p.cli
	hash := mountDigest(t, p.cli)
	for _, profile := range []string{launcherFullProfile, launcherMinimalProfile} {
		t.Run(profile, func(t *testing.T) {
			binary := packMountProduct(t, p, output, mountConfiguration{Version: 2, Profile: profile, Codec: mountCodecZstd})
			for _, item := range runtimequalify.Cases() {
				if item.Scenario != "distributed-cli" {
					continue
				}
				t.Run(item.Mode, func(t *testing.T) {
					req := prepareMountRequest(t, p, binary, "directory", item.Mode)
					req.Runs, req.Args = item.Runs, []string{"detect", "--json"}
					req.Env = append(req.Env, "MICROFAT_LOG=json", "GOMAXPROCS=2")
					req.Runtime = &mountfixture.Runtime{Workers: item.Workers, Policy: item.Policy}
					request := filepath.Join(req.Root, "request.json")
					writeMountJSON(t, request, req)
					command, result, stderr, err := invokeMountRunner(backend, p.controller, request)
					require.NoError(t, err, "stage=%s error=%s stderr=%s", result.Stage, result.Error, stderr)
					bundleHash := mountDigest(t, binary)
					evidence := runtimequalify.Evidence{Case: item, Request: req, Result: result, Stderr: stderr, Command: command,
						ExecutedSHA256: bundleHash, Lineage: runtimequalify.Lineage{BundleSHA256: bundleHash,
							PayloadSHA256: hash, PayloadSize: payloadBytes, SelectedTier: mountLevel()}}
					require.NoError(t, runtimequalify.ValidateEvidence(evidence))
					var detected struct{ Arch string }
					require.NoError(t, json.Unmarshal([]byte(result.Executions[0].Stdout), &detected))
					require.Equal(t, runtime.GOARCH, detected.Arch)
				})
			}
		})
	}
}
