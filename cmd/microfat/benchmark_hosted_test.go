package main

import (
	"bytes"
	json "encoding/json/v2"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/compare"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/report"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostedCLI(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	exp, err := report.ReadBundle(cliExperiment(t))
	require.NoError(t, err)
	const calibrationBlocks = 5
	exp.Schedule, err = compare.Schedule(exp.Configurations, calibrationBlocks, 1)
	require.NoError(t, err)
	exp.Trials = nil
	for _, scheduled := range exp.Schedule {
		exp.Trials = append(exp.Trials, schema.ProcessTrial{ScheduledTrial: scheduled, StartedAt: exp.CreatedAt,
			Outcome: schema.OutcomeOK, Metrics: map[string]schema.Measurement{
				"startup_ns": schema.Measured(1, "ns", "startup", schema.StartupProtocol)}})
	}
	exp.Runner = &schema.RunnerInfo{Provider: schema.HostedProvider, Image: "ubuntu24", ImageVersion: "one",
		RunID: "1", Attempt: "1", Job: "calibration", Repetition: "0", Protocol: schema.StartupProtocol}
	exp.Artifacts[0].SourceSHA = exp.SourceSHA
	exp.Configurations[0].ID, exp.Configurations[1].ID = "base-app", "head-app"
	for i := range exp.Trials {
		id := "base-app"
		if i%2 == 1 {
			id = "head-app"
		}
		exp.Trials[i].ConfigurationID, exp.Schedule[i].ConfigurationID = id, id
		m := exp.Trials[i].Metrics["startup_ns"]
		m.Source = schema.StartupProtocol
		exp.Trials[i].Metrics["startup_ns"] = m
	}
	bundle := filepath.Join(root, "bundle")
	require.NoError(t, report.WriteBundle(exp, bundle, nil))
	list := filepath.Join(root, "list.json")
	data, err := json.Marshal([]string{bundle})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(list, data, 0o600))
	policy := filepath.Join(root, "policy.json")
	require.NoError(t, os.WriteFile(policy, []byte(`{"version":"v1","classes":[]}`), 0o600))
	bad := filepath.Join(root, "bad.json")
	require.NoError(t, os.WriteFile(bad, []byte("bad"), 0o600))
	empty := filepath.Join(root, "empty.json")
	require.NoError(t, os.WriteFile(empty, []byte("[]"), 0o600))
	for _, test := range []struct {
		args      []string
		wantError bool
	}{
		{[]string{"calibrate", experimentInputFlag, list}, false},
		{[]string{"calibrate", experimentInputFlag, bad}, true},
		{[]string{"calibrate", experimentInputFlag, empty}, true},
		{[]string{"calibrate", experimentInputFlag, experimentMissing}, true},
		{[]string{experimentGate, experimentInputFlag, bundle, "--calibration", policy}, false},
		{[]string{experimentGate, experimentInputFlag, bundle, "--calibration", bad}, true},
		{[]string{experimentGate, experimentInputFlag, bundle, "--calibration", experimentMissing}, true},
		{[]string{"qualify", experimentInputFlag, bundle}, true},
		{[]string{"qualify", experimentInputFlag, experimentMissing}, true},
		{[]string{"qualify", "--policy", "invalid", experimentInputFlag, bundle}, true},
	} {
		cmd := newRootCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(append([]string{cmdBenchmark}, test.args...))
		err := cmd.Execute()
		if test.wantError {
			require.Error(t, err, test.args)
		} else {
			require.NoError(t, err, test.args)
			assert.NotEmpty(t, out.String())
		}
	}
}
