//go:build linux

package e2e_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/tests/e2e/testdata/mountfixture"
	"github.com/stretchr/testify/require"
)

func TestMountEvidenceCompleteness(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"complete", "missing", "duplicate", "skipped", "failed", "unknown", "duplicate-expected"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			summary := mountSummary{Expected: []string{"one", "two"}, Results: []mountEvidence{
				{Case: "one", Status: mountPass}, {Case: "two", Status: mountPass}}}
			switch name {
			case "missing":
				summary.Results = summary.Results[:1]
			case "duplicate":
				summary.Results[1].Case = "one"
			case "skipped":
				summary.Results[1].Status = mountSkip
			case "failed":
				summary.Results[1].Status = mountFail
			case "unknown":
				summary.Results[1].Case = "three"
			case "duplicate-expected":
				summary.Expected[1] = "one"
			}
			if name == "complete" {
				require.NoError(t, validateMountSummary(summary))
			} else {
				require.Error(t, validateMountSummary(summary))
			}
		})
	}
	for _, stdout := range []string{"not json", `{"digest":"wrong"}`, `{}`} {
		_, err := decodeMountReport(stdout, strings.Repeat("a", 64))
		require.Error(t, err, "a malformed report or wrong executed image must fail")
	}
}

func TestMountHarnessContracts(t *testing.T) {
	t.Run("missing-tool", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		require.Contains(t, mountPreflight("userns"), "prerequisite unavailable")
	})
	dir := t.TempDir()
	controller := filepath.Join(dir, "controller")
	reporter := filepath.Join(dir, "reporter")
	env := []string{"CGO_ENABLED=0", envBaselineAMD64, stubEnvARM64}
	require.NoError(t, compileBinary("./testdata/mount_runner", controller, env))
	require.NoError(t, compileBinary("./testdata/mount_reporter", reporter, env))
	p := mountProducts{controller: controller, reporter: reporter, replacement: reporter}
	t.Run("refuse-parent-namespace", func(t *testing.T) {
		req := prepareMountRequest(t, p, reporter, "directory", execModeNative)
		request := filepath.Join(req.Root, "request.json")
		writeMountJSON(t, request, req)
		out, err := exec.Command(controller, request).CombinedOutput()
		require.Error(t, err)
		var result mountfixture.Result
		require.NoError(t, json.Unmarshal(out, &result))
		require.Equal(t, "namespace", result.Stage)
		require.Contains(t, result.Error, "refusing to mount in the parent namespace")
	})
	t.Run("malformed-request", func(t *testing.T) {
		request := filepath.Join(t.TempDir(), "request.json")
		require.NoError(t, os.WriteFile(request, []byte(`{"unknown":true}`), privateFilePerm))
		out, err := exec.Command(controller, request).CombinedOutput()
		require.Error(t, err)
		require.Contains(t, string(out), "unknown field")
	})
	backend := os.Getenv(mountBackendEnv)
	if backend == "" {
		backend = "userns"
	}
	if os.Getenv(mountModeEnv) == "" {
		t.Skip("real setup and cleanup checks run with task qualify-mounts")
	}
	if reason := mountPreflight(backend); reason != "" {
		if os.Getenv(mountModeEnv) == mountRequired {
			t.Fatal(reason)
		}
		t.Skip(reason)
	}
	t.Run("setup-failure-is-not-a-pass", func(t *testing.T) {
		req := prepareMountRequest(t, p, reporter, "directory", execModeNative)
		req.Mounts[0].Source = "missing-source"
		request := filepath.Join(req.Root, "request.json")
		writeMountJSON(t, request, req)
		_, result, _, err := invokeMountRunner(backend, controller, request)
		require.Error(t, err)
		require.Equal(t, "setup", result.Stage)
		require.Empty(t, result.Executions)
	})
	t.Run("refuse-shared-pid-namespace", func(t *testing.T) {
		req := prepareMountRequest(t, p, reporter, "directory", execModeNative)
		request := filepath.Join(req.Root, "request.json")
		writeMountJSON(t, request, req)
		args := []string{mountUnshare, "--mount", "--propagation", "private"}
		if backend == mountSudo {
			args = append([]string{mountSudo, "-n", "--"}, args...)
		} else {
			args = append(args, "--user", "--map-current-user", "--keep-caps")
		}
		args = append(args, "--", controller, request)
		output := mountCommandOutput(args[0], args[1:]...)
		require.Contains(t, output, "fixture supervisor must be PID 1 in a new PID namespace")
	})
	for _, mode := range []string{"--hang", "--spawn-detached"} {
		t.Run("timeout-reaps/"+mode, func(t *testing.T) {
			assertMountTimeoutCleanup(t, backend, p, mode)
		})
	}
}
