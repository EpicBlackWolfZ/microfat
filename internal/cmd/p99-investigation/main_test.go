package main

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
	"github.com/EpicBlackWolfZ/microfat/internal/p99investigation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const investigationOutputFlag = "--output"

func TestInvalidInvestigationCommands(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"unknown"}, {"summarize", "--unknown"}, {"measure", "unexpected"},
		{"prepare", "--cohort", "invalid"}, {"summarize", investigationOutputFlag, ""}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			require.Error(t, run(context.Background(), args, func(context.Context, process.Spec) ([]byte, error) {
				t.Fatal("invalid arguments must not execute commands")
				return nil, nil
			}))
		})
	}
}

func TestInvestigationCommandLifecycle(t *testing.T) {
	t.Parallel()
	output := filepath.Join(t.TempDir(), "cohort")
	controls := filepath.Join(t.TempDir(), "controls.json")
	target := system.Options{Affinity: []int{0}, CgroupRoot: "/sys/fs/cgroup/cli-fixture", Version: "v2",
		CPUQuotaUS: 100000, CPUPeriodUS: 100000, MemoryBytes: 2147483648}
	generator := target
	generator.Affinity = []int{3}
	data, err := json.Marshal(map[string]system.Options{"target": target, "generator": generator})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(controls, data, 0o600))
	const harness = "0123456789abcdef0123456789abcdef01234567"
	measured := false
	executor := func(ctx context.Context, spec process.Spec) ([]byte, error) {
		if spec.Path == "bin/microfat" {
			deadline, ok := ctx.Deadline()
			require.True(t, ok, "CLI bounds every measurement")
			assert.InDelta(t, float64(165*time.Minute), float64(time.Until(deadline)), float64(5*time.Second))
			measured = true
			return []byte("/fixture/evidence\n"), errors.New("retained trial failure")
		}
		require.Equal(t, "git", spec.Path)
		if spec.Args[0] != "rev-parse" {
			return nil, nil
		}
		sha := harness
		switch filepath.Base(spec.Dir) {
		case "base":
			sha = p99investigation.BaseSHA
		case "head":
			sha = p99investigation.HeadSHA
		}
		return []byte(sha + "\n"), nil
	}
	require.NoError(t, run(context.Background(), []string{"prepare", "--scenario", "mixed4", "--cohort", "1",
		"--controls", controls, investigationOutputFlag, output}, executor))
	err = run(context.Background(), []string{"measure", investigationOutputFlag, output}, executor)
	require.ErrorContains(t, err, "retained trial failure")
	assert.True(t, measured)
	stdout, err := os.ReadFile(filepath.Join(output, "measurement.stdout"))
	require.NoError(t, err)
	assert.Equal(t, "/fixture/evidence\n", string(stdout))
	require.Error(t, run(context.Background(), []string{"summarize", investigationOutputFlag, output}, executor))
	data, err = os.ReadFile(filepath.Join(output, "summary.json"))
	require.NoError(t, err)
	var summary p99investigation.Summary
	require.NoError(t, json.Unmarshal(data, &summary))
	require.NotNil(t, summary.Plan)
	assert.Equal(t, "mixed4", summary.Plan.Scenario)
	assert.Equal(t, 1, summary.Plan.Cohort)
	assert.False(t, summary.Complete)
	assert.NotEmpty(t, summary.Reasons)
}

func TestExecuteRetainsOutputAndExitFailure(t *testing.T) {
	t.Parallel()
	stdout, err := execute(context.Background(), process.Spec{Path: "sh", Args: []string{"-c", "printf retained; exit 3"}})
	require.Error(t, err)
	assert.Equal(t, "retained", string(stdout))
}

func TestInvestigationMainRetainsFailure(t *testing.T) {
	const outputEnvironment = "MICROFAT_P99_TEST_OUTPUT"
	if output := os.Getenv(outputEnvironment); output != "" {
		os.Args = []string{"p99-investigation", "summarize", investigationOutputFlag, output}
		main()
		return
	}
	t.Parallel()
	output := filepath.Join(t.TempDir(), "summary")
	binary, err := os.Executable()
	require.NoError(t, err)
	_, _, err = process.Run(context.Background(), process.Spec{Path: binary,
		Args: []string{"-test.run=^TestInvestigationMainRetainsFailure$"},
		Env:  append(os.Environ(), outputEnvironment+"="+output)})
	require.Error(t, err, "the command must fail after retaining an incomplete cohort report")
	data, err := os.ReadFile(filepath.Join(output, "summary.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "Maintainer review required")
}

type investigationErrorWriter struct{ err error }

func (w investigationErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestExecuteOutputPreservesStreamsAndFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, script string
		failure      bool
	}{
		{name: "success", script: "printf output; printf diagnostic >&2"},
		{name: "exit failure", script: "printf output; printf diagnostic >&2; exit 3", failure: true},
		{name: "signal", script: "printf output; printf diagnostic >&2; kill -TERM $$", failure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			stdout, err := executeOutput(context.Background(), process.Spec{Path: "sh", Args: []string{"-c", tc.script}}, &stderr)
			if tc.failure {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, "output", string(stdout))
			assert.Equal(t, "diagnostic", stderr.String())
		})
	}
	_, err := executeOutput(context.Background(), process.Spec{Path: filepath.Join(t.TempDir(), "missing")}, &bytes.Buffer{})
	require.Error(t, err)
	writeErr := errors.New("diagnostic sink failed")
	_, err = executeOutput(context.Background(), process.Spec{Path: "sh", Args: []string{"-c", "printf diagnostic >&2"}},
		investigationErrorWriter{err: writeErr})
	require.ErrorIs(t, err, writeErr)
}

func TestExecuteOutputCancelsLiveChildWhenProgressFails(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	writeErr := errors.New("live diagnostic sink failed")
	stdout, err := executeOutput(ctx,
		process.Spec{Path: "sh", Args: []string{"-c", "printf started; printf diagnostic >&2; exec sleep 10"}},
		investigationErrorWriter{err: writeErr})
	require.ErrorIs(t, err, writeErr, "the one-second progress flush must cancel the owned process group")
	assert.Equal(t, "started", string(stdout))
	assert.NoError(t, ctx.Err(), "cancellation comes from the failed progress sink before the safety deadline")
}
