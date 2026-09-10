package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/compare"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/report"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/runner"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const experimentInputFlag = "--input"
const experimentMissing = "/missing"
const experimentGate = "gate"

func cliExperiment(t *testing.T) string {
	t.Helper()
	const shaLength, digestLength = 40, 64
	data := []byte(`{}`)
	exp := &schema.ExperimentV2{SchemaVersion: schema.VersionV2, ID: "cli-fixture", CreatedAt: "2026-09-09T00:00:00Z",
		SourceSHA: strings.Repeat("a", shaLength), Config: data, ConfigSHA256: schema.Digest(data), Complete: true,
		Artifacts:      []schema.Artifact{{ID: "app", SHA256: strings.Repeat("b", digestLength), Bytes: 1}},
		Configurations: []schema.Configuration{{ID: "base-app", ArtifactID: "app"}, {ID: "head-app", ArtifactID: "app"}}}
	schedule, err := compare.Schedule(exp.Configurations, 1, 1)
	require.NoError(t, err)
	exp.Schedule = schedule
	for _, scheduled := range schedule {
		exp.Trials = append(exp.Trials, schema.ProcessTrial{ScheduledTrial: scheduled, StartedAt: exp.CreatedAt, Outcome: schema.OutcomeOK,
			Metrics: map[string]schema.Measurement{"startup_ns": schema.Measured(1, "ns", "startup", "fixture")}})
	}
	path := filepath.Join(t.TempDir(), "bundle")
	require.NoError(t, report.WriteBundle(exp, path, nil))
	return path
}

func TestExperimentReadCommands(t *testing.T) {
	t.Parallel()
	bundle := cliExperiment(t)
	for _, args := range [][]string{
		{cmdBenchmark, "verify", bundle}, {cmdBenchmark, "report", experimentInputFlag, bundle, "--format", "markdown"},
		{cmdBenchmark, "report", experimentInputFlag, bundle, flagJSON}, {cmdBenchmark, "compare", experimentInputFlag, bundle, flagJSON},
		{cmdBenchmark, "compare", experimentInputFlag, bundle}, {cmdBenchmark, experimentGate, experimentInputFlag, bundle},
	} {
		cmd := newRootCmd()
		var stdout bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(args)
		require.NoError(t, cmd.Execute(), args)
		assert.NotEmpty(t, stdout.String())
	}
	for _, args := range [][]string{
		{cmdBenchmark, "report"}, {cmdBenchmark, "report", experimentInputFlag, experimentMissing},
		{cmdBenchmark, "compare", experimentInputFlag, bundle, "--baseline", "missing"},
		{cmdBenchmark, "compare", experimentInputFlag, bundle, "--format", "invalid"},
		{cmdBenchmark, "verify", experimentMissing}, {cmdBenchmark, experimentGate, experimentInputFlag, experimentMissing},
		{cmdBenchmark, experimentGate, experimentInputFlag, bundle, "--startup-calibrated"},
		{cmdBenchmark, "child", "{}"}, {cmdBenchmark, "child", `{"path":experimentMissing}`},
		{cmdBenchmark, "observe", "{"}, {cmdBenchmark, "observe", "{}"},
	} {
		cmd := newRootCmd()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(args)
		require.Error(t, cmd.Execute(), args)
	}
}

func TestExperimentRunCommand(t *testing.T) {
	previous := executeExperiment
	t.Cleanup(func() { executeExperiment = previous })
	executeExperiment = func(_ context.Context, cfg runner.ExperimentConfig, opts runner.RunOptions) (*runner.ExperimentResult, error) {
		assert.Equal(t, "test", cfg.Name)
		assert.Equal(t, "fake-tool", opts.Fortio)
		return &runner.ExperimentResult{Bundle: "/fixture/evidence"}, nil
	}
	config := filepath.Join(t.TempDir(), "suite.json")
	require.NoError(t, os.WriteFile(config, []byte(`{"name":"test"}`), 0o600))
	cmd := newRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{cmdBenchmark, "run", "--config", config, "--fortio", "fake-tool"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, "/fixture/evidence\n", stdout.String())
	executeExperiment = func(context.Context, runner.ExperimentConfig, runner.RunOptions) (*runner.ExperimentResult, error) {
		return nil, errors.New("execution failed")
	}
	for _, args := range [][]string{{cmdBenchmark, "run"}, {cmdBenchmark, "run", "--config", experimentMissing},
		{cmdBenchmark, "run", "--config", config}} {
		cmd := newRootCmd()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(args)
		require.Error(t, cmd.Execute())
	}
}
