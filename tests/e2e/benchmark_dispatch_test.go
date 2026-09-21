package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/require"
)

func TestPackedCLIBenchmarkHelpers(t *testing.T) {
	// This is a functional orchestration regression, never performance evidence.
	root := t.TempDir()
	packed := filepath.Join(root, "packed-cli")
	base := "v1"
	if currentHostArch == archARM64 {
		base = manifestARM64Base
	}
	require.NoError(t, packBinary(cliPath, stubPath, "benchmark-cli", packed, map[string]string{base: cliPath}))
	repository, err := filepath.Abs("../..")
	require.NoError(t, err)
	fixture := filepath.Join(repository, "benchmarks/load/testdata/fortio-1.75.2.json")
	tool := filepath.Join(root, "fortio")
	// Saved load-tool output keeps this required test independent of downloads.
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = version ]; then echo 1.75.2; else sleep 0.1; cat %q; fi\n", fixture)
	require.NoError(t, os.WriteFile(tool, []byte(script), defaultFilePerm))
	config := filepath.Join(root, "suite.json")
	require.NoError(t, os.WriteFile(config, []byte(`{"version":"v1","name":"packed-cli-functional",
		"blocks":1,"cache_states":["warm"],"duration_ms":300,"warmup_ms":100}`), privateFilePerm))
	nativeBytes, err := os.ReadFile(cliPath)
	require.NoError(t, err)
	wantHarness := fmt.Sprintf("%x", sha256.Sum256(nativeBytes))
	for _, mode := range []string{"auto", format.ExecModeMemfd, format.ExecModeCache, "native"} {
		t.Run(mode, func(t *testing.T) {
			const timeout = 2 * time.Minute
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			binary := packed
			if mode == "native" {
				binary = cliPath
			}
			cmd := exec.CommandContext(ctx, binary, "benchmark", "run", "--config", config,
				"--output-dir", filepath.Join(root, mode), "--fortio", tool, "--repository", repository)
			cmd.Env = append(os.Environ(), "MICROFAT_EXEC_MODE="+mode,
				"MICROFAT_ORIGINAL_EXE=/untrusted/location", "MICROFAT_CACHE_DIR="+filepath.Join(root, mode+"-cache"))
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			require.NoError(t, cmd.Run(), stderr.String())
			bundle := strings.TrimSpace(stdout.String())
			verify := exec.CommandContext(ctx, cliPath, "benchmark", "verify", bundle)
			out, err := verify.CombinedOutput()
			require.NoError(t, err, string(out))
			assertBenchmarkHelpers(t, bundle, wantHarness)
		})
	}
}

func assertBenchmarkHelpers(t *testing.T, bundle, wantHarness string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(bundle, "raw.json"))
	require.NoError(t, err)
	var result struct {
		Complete  bool `json:"complete"`
		Artifacts []struct {
			ID     string `json:"id"`
			SHA256 string `json:"sha256"`
		} `json:"artifacts"`
		Trials []struct {
			Outcome     string `json:"outcome"`
			SampleCount int    `json:"sample_count"`
		} `json:"trials"`
	}
	require.NoError(t, json.Unmarshal(data, &result))
	require.True(t, result.Complete)
	require.GreaterOrEqual(t, len(result.Trials), 2)
	for _, trial := range result.Trials {
		require.Equal(t, "ok", trial.Outcome)
		require.Positive(t, trial.SampleCount, "same-image observer must execute")
	}
	for _, artifact := range result.Artifacts {
		if artifact.ID == "harness" {
			require.Equal(t, wantHarness, artifact.SHA256)
			return
		}
	}
	t.Fatal("missing harness identity")
}
