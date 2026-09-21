package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/releaseworkflow"
	"github.com/stretchr/testify/require"
)

const testSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func emptyEnvironment(string) string { return "" }

func TestArgumentContract(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, args := range [][]string{nil, {root}, {root, "--bad"}, {root, "--source-cohort", "1"},
		{root, "--archive", "--shard"}, {root, "--calibration", "extra"}, {t.TempDir(), "--archive"},
		{t.TempDir(), "--source-cohort", "1", "2"}} {
		require.Error(t, run(args, emptyEnvironment, nil, io.Discard))
	}
}

func TestSourceCohortOutput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write := func(name string, value any) {
		data, err := json.Marshal(value)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(root, name), data, 0o600))
	}
	write("source-run.json", releaseworkflow.WorkflowRun{ID: 1, Attempt: 2, HeadSHA: testSHA,
		Status: "completed", Path: releaseworkflow.BenchmarkWorkflow})
	var jobs []releaseworkflow.Job
	for _, name := range releaseworkflow.MeasurementJobs() {
		jobs = append(jobs, releaseworkflow.Job{Name: name, Conclusion: "success"})
	}
	write("source-jobs.json", map[string]any{"jobs": jobs})
	var out bytes.Buffer
	require.NoError(t, run([]string{root, "--source-cohort", "1", "2"}, emptyEnvironment, nil, &out))
	require.Equal(t, testSHA+"\n", out.String())
}

func TestShardCommand(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for name, value := range map[string]string{"SHA256SUMS": "fixture", "report.json": "json", "report.md": "markdown",
		"raw.json": `{"source_sha":"fixture","release_eligible":false,"config":{"workload":"mixed","iterations":4},
		"environment":{"host":{"arch":"amd64"}}}`} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(value), 0o600))
	}
	execute := func(args ...string) ([]byte, error) {
		if args[0] == "report" {
			return []byte(args[len(args)-1] + "\n"), nil
		}
		if args[0] == "qualify" {
			return []byte(`{"publishable":true}`), nil
		}
		return nil, nil
	}
	require.NoError(t, run([]string{root, "--shard"}, emptyEnvironment, execute, io.Discard))
}

func TestBenchmarkProcess(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.Mkdir("bin", 0o700))
	const script = "#!/bin/sh\nif [ \"$2\" = fail ]; then echo failed >&2; exit 7; fi\nprintf '%s' \"$*\"\n"
	require.NoError(t, os.WriteFile("bin/microfat", []byte(script), 0o700))
	out, err := executeBenchmark(context.Background(), "verify", "fixture")
	require.NoError(t, err)
	require.Equal(t, "benchmark verify fixture", string(out))
	out, err = executeBenchmark(context.Background(), "report", "--input", "fixture")
	require.NoError(t, err)
	require.Equal(t, "benchmark report --input fixture", string(out))
	_, err = executeBenchmark(context.Background(), "fail")
	require.ErrorContains(t, err, "failed")
}

func TestMissingWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.Remove(root))
	_, err := executeBenchmark(context.Background(), "verify", "fixture")
	require.Error(t, err)
}

func TestMain(t *testing.T) {
	if os.Getenv("MICROFAT_EVIDENCE_COMMAND_HELPER") == "1" {
		os.Args = []string{"benchmark-evidence"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMain$")
	cmd.Env = append(os.Environ(), "MICROFAT_EVIDENCE_COMMAND_HELPER=1")
	output, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "usage: benchmark-evidence")
}
