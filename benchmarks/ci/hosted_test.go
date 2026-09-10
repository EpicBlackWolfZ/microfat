package ci

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/internal/testfixture"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func hostedFixture() *schema.ExperimentV2 {
	exp := testfixture.Experiment()
	exp.Config = []byte(`{"blocks":20,"duration_ms":30000,"warmup_ms":10000,"sample_ms":250,
		"name":"fixture","target":{"cgroup_root":"/one","memory_root":"/two"}}`)
	exp.ConfigSHA256, _ = schema.ConfigDigest(exp.Config)
	exp.Environment.Host.Arch, exp.Environment.Host.CPU.ModelName = "amd64", "fixture CPU"
	exp.Environment.Host.KernelRelease, exp.Environment.Process.GoVersion = "fixture kernel", "go1.27.1"
	exp.Runner = &schema.RunnerInfo{Provider: schema.HostedProvider, Image: "ubuntu24", ImageVersion: "image1",
		RunID: "1", Attempt: "1", Job: "calibration", Repetition: "0", Repository: "owner/repo", Protocol: schema.StartupProtocol}
	exp.Artifacts[0].SourceSHA = exp.SourceSHA
	harness := exp.Artifacts[0]
	harness.ID = "harness"
	harness.Settings = map[string]string{"vcs.modified": "false"}
	exp.Artifacts = append(exp.Artifacts, harness)
	exp.Configurations[0].ID, exp.Configurations[1].ID = "base-native", "head-native"
	exp.Schedule, exp.Trials = nil, nil
	for block := range ReleaseBlocks {
		for _, cfg := range exp.Configurations {
			scheduled := schema.ScheduledTrial{ID: fmt.Sprintf("trial-%d", len(exp.Schedule)), Block: block,
				Position: len(exp.Schedule), ConfigurationID: cfg.ID}
			exp.Schedule = append(exp.Schedule, scheduled)
			trial := schema.ProcessTrial{ScheduledTrial: scheduled, Outcome: schema.OutcomeOK, StartedAt: exp.CreatedAt,
				SampleCount: 2, SampleIntervalMS: 25, TelemetryPath: "trials/" + scheduled.ID + "/telemetry.json",
				Metrics: make(map[string]schema.Measurement)}
			for _, name := range coreMetrics() {
				trial.Metrics[name] = schema.Measured(100, "count", "test", "fixture")
			}
			trial.Metrics["startup_ns"] = schema.Measured(100, "ns", "startup", schema.StartupProtocol)
			for _, role := range []string{"target", "generator"} {
				for _, name := range []string{"affinity", "cpu.max", "memory.max"} {
					trial.Controls = append(trial.Controls, schema.Control{Name: role + "." + name, State: "applied", Effective: "100000"})
				}
			}
			load := testfixture.HTTP()
			trial.Load = &load
			exp.Trials = append(exp.Trials, trial)
		}
	}
	return exp
}

func TestHostedQualification(t *testing.T) {
	t.Parallel()
	exp := hostedFixture()
	require.NoError(t, schema.ValidateV2(exp))
	require.True(t, QualifyHosted(exp).Publishable, QualifyHosted(exp))
	for name, change := range map[string]func(*schema.ExperimentV2){
		"schema":       func(e *schema.ExperimentV2) { e.SchemaVersion = "bad" },
		"runner":       func(e *schema.ExperimentV2) { e.Runner = nil },
		"strict claim": func(e *schema.ExperimentV2) { e.ReleaseEligible = true },
		"dirty":        func(e *schema.ExperimentV2) { e.Dirty = true },
		"blocks": func(e *schema.ExperimentV2) {
			e.Config = []byte(`{"blocks":1}`)
			e.ConfigSHA256, _ = schema.ConfigDigest(e.Config)
		},
		"short window": func(e *schema.ExperimentV2) {
			e.Config = []byte(strings.ReplaceAll(string(e.Config), `"duration_ms":30000`, `"duration_ms":1000`))
			e.ConfigSHA256, _ = schema.ConfigDigest(e.Config)
		},
		"no warmup": func(e *schema.ExperimentV2) {
			e.Config = []byte(strings.ReplaceAll(string(e.Config), `"warmup_ms":10000`, `"warmup_ms":0`))
			e.ConfigSHA256, _ = schema.ConfigDigest(e.Config)
		},
		"slow sampling": func(e *schema.ExperimentV2) {
			e.Config = []byte(strings.ReplaceAll(string(e.Config), `"sample_ms":250`, `"sample_ms":1000`))
			e.ConfigSHA256, _ = schema.ConfigDigest(e.Config)
		},
		"artifact":  func(e *schema.ExperimentV2) { e.Artifacts[0].SourceSHA = "unknown" },
		"harness":   func(e *schema.ExperimentV2) { e.Artifacts[1].Settings["vcs.modified"] = "true" },
		"telemetry": func(e *schema.ExperimentV2) { e.Trials[0].SampleCount = 0 },
		"metric":    func(e *schema.ExperimentV2) { delete(e.Trials[0].Metrics, "peak_rss_bytes") },
		"control":   func(e *schema.ExperimentV2) { e.Trials[0].Controls[0].State = "unavailable" },
		"unlimited": func(e *schema.ExperimentV2) { e.Trials[0].Controls[1].Effective = "max 100000" },
		"no pairs": func(e *schema.ExperimentV2) {
			e.Configurations[1].ID = "other"
			for i := range e.Trials {
				if e.Trials[i].ConfigurationID == "head-native" {
					e.Trials[i].ConfigurationID = "other"
					e.Schedule[i].ConfigurationID = "other"
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) { e := hostedFixture(); change(e); assert.False(t, QualifyHosted(e).Publishable) })
	}
	exp.Warnings = []string{"noisy-environment: frequency unavailable", "exec diagnostic: partial"}
	assert.True(t, QualifyHosted(exp).Publishable)
}

func calibrationFixtures() []*schema.ExperimentV2 {
	var exps []*schema.ExperimentV2
	for i := range TrainingRuns + HoldoutRuns {
		e := hostedFixture()
		e.Runner.Repetition = strconv.Itoa(i)
		for j := range e.Trials {
			if e.Trials[j].ConfigurationID == "head-native" {
				m := e.Trials[j].Metrics["startup_ns"]
				v := 101.0
				m.Value = &v
				e.Trials[j].Metrics["startup_ns"] = m
			}
		}
		exps = append(exps, e)
	}
	return exps
}

func TestCalibration(t *testing.T) {
	t.Parallel()
	exps := calibrationFixtures()
	c, err := Calibrate(exps)
	require.NoError(t, err)
	require.Len(t, c.Classes, 1)
	assert.Equal(t, "validated", c.Classes[0].Status)
	p, reason := c.Policy(exps[0])
	assert.True(t, p.Calibrated, reason)
	assert.Equal(t, 2.0, p.StartupFloorNS)
	_, err = Calibrate(append(exps, exps[0]))
	require.ErrorContains(t, err, "duplicate")
	small, err := Calibrate(exps[:1])
	require.NoError(t, err)
	p, _ = small.Policy(exps[0])
	assert.False(t, p.Calibrated)
	for _, mutate := range []func(*schema.ExperimentV2){
		func(e *schema.ExperimentV2) { e.Runner = nil }, func(e *schema.ExperimentV2) { e.Config = []byte("bad") },
		func(e *schema.ExperimentV2) { e.Runner.Repetition = "bad" }, func(e *schema.ExperimentV2) { e.Dirty = true },
		func(e *schema.ExperimentV2) { e.Environment.Host.CPU.ModelName = "" },
		func(e *schema.ExperimentV2) { e.Artifacts[0].SourceSHA = "other" },
		func(e *schema.ExperimentV2) {
			e.Trials[0].Metrics["startup_ns"] = schema.Measured(1, "ns", "startup", "old")
		},
		func(e *schema.ExperimentV2) {
			m := e.Trials[0].Metrics["startup_ns"]
			m.Value = nil
			m.Reason = "missing"
			e.Trials[0].Metrics["startup_ns"] = m
		},
	} {
		e := hostedFixture()
		mutate(e)
		_, err := Calibrate([]*schema.ExperimentV2{e})
		require.Error(t, err)
	}
	for _, mutate := range []func(*Calibration){func(c *Calibration) { c.Version = "bad" }, func(c *Calibration) { c.Classes = nil },
		func(c *Calibration) { c.Classes[0].FloorNS = math.NaN() }} {
		copy, err := Calibrate(exps)
		require.NoError(t, err)
		mutate(&copy)
		p, _ := copy.Policy(exps[0])
		assert.False(t, p.Calibrated)
	}
	exps[TrainingRuns].Trials[1].Metrics["startup_ns"] = schema.Measured(1000, "ns", "startup", schema.StartupProtocol)
	for j := range exps[TrainingRuns].Trials {
		if exps[TrainingRuns].Trials[j].ConfigurationID == "head-native" {
			exps[TrainingRuns].Trials[j].Metrics["startup_ns"] = schema.Measured(1000, "ns", "startup", schema.StartupProtocol)
		}
	}
	c, err = Calibrate(exps)
	require.NoError(t, err)
	assert.Equal(t, "report_only", c.Classes[0].Status)
}
