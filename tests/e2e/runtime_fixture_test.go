//go:build linux

package e2e_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"github.com/stretchr/testify/require"
)

// This source regression deliberately uses the existing E2E product setup.
// Candidate qualification is a separate controller and never enters TestMain.
func TestRuntimeFixtureTeardown(t *testing.T) {
	if os.Getenv("MICROFAT_RUNTIME_FIXTURES") != mountRequired {
		t.Skip("run task test-runtime-fixtures for native namespace cleanup controls")
	}
	backend := os.Getenv(mountBackendEnv)
	if backend == "" {
		backend = "userns"
	}
	require.Empty(t, mountPreflight(backend), "required runtime fixture prerequisites")
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
