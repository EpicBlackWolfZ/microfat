package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/releaseworkflow"
	"github.com/stretchr/testify/require"
)

const testSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func emptyEnvironment(string) string { return "" }

func TestArguments(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"--recovery-source"}, {"--recovery-source", "1"}, {"--recovery-source", "1", "2"},
		{"root", "--bad"}, {"root", "extra"}, {"root"}} {
		require.Error(t, run(args, emptyEnvironment, nil, io.Discard))
	}
}

func TestRecoverySourceCommand(t *testing.T) {
	t.Parallel()
	values := map[string]string{"GH_REPO": "owner/repo", "GITHUB_EVENT_NAME": "workflow_dispatch", "GITHUB_REF": "refs/heads/main"}
	env := func(key string) string { return values[key] }
	gh := func(args ...string) ([]byte, error) {
		require.Equal(t, "api", args[0], "source resolution must be read-only")
		var value any
		switch {
		case strings.Contains(args[1], "/jobs?"):
			jobs := []releaseworkflow.Job{{Name: releaseworkflow.VerificationJob, Conclusion: "success"}}
			for _, name := range releaseworkflow.MeasurementJobs() {
				jobs = append(jobs, releaseworkflow.Job{Name: name, Conclusion: "success"})
			}
			value = map[string]any{"jobs": jobs}
		case strings.Contains(args[1], "/commits/"):
			value = map[string]string{"sha": testSHA}
		default:
			value = releaseworkflow.WorkflowRun{ID: 1, Attempt: 2, HeadSHA: testSHA, HeadBranch: "v0.2.5",
				Status: "completed", Path: releaseworkflow.BenchmarkWorkflow}
		}
		return json.Marshal(value)
	}
	var out bytes.Buffer
	require.NoError(t, run([]string{"--recovery-source", "1", "2"}, env, gh, &out))
	require.Equal(t, "v0.2.5 "+testSHA+"\n", out.String())
}

func TestGitHubProcess(t *testing.T) {
	root := t.TempDir()
	const script = "#!/bin/sh\nif [ \"$1\" = fail ]; then echo failed >&2; exit 7; fi\nprintf result\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "gh"), []byte(script), 0o700))
	t.Setenv("PATH", root)
	out, err := executeGitHub(context.Background(), "api", "fixture")
	require.NoError(t, err)
	require.Equal(t, "result", string(out))
	_, err = executeGitHub(context.Background(), "fail")
	require.ErrorContains(t, err, "failed")
}

func TestMain(t *testing.T) {
	if os.Getenv("MICROFAT_FINALIZE_COMMAND_HELPER") == "1" {
		os.Args = []string{"release-finalize"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMain$")
	cmd.Env = append(os.Environ(), "MICROFAT_FINALIZE_COMMAND_HELPER=1")
	output, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "usage: release-finalize")
}
