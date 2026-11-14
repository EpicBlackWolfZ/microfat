package p99investigation

import (
	json "encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/report"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fixtureHarness = "harness"
	fixtureApplied = "applied"
)

func TestVerifyPlanAcceptsHistoricalSourcesAndUnchangedNativeControls(t *testing.T) {
	t.Parallel()
	plan, exp := summaryFixture(t)
	require.NoError(t, VerifyPlan(plan, exp))
	// Failed trials remain valid retained observations, never successful evidence.
	exp.Trials[0].Outcome, exp.Trials[0].Reason = schema.OutcomeFailed, "target timed out"
	exp.Trials[0].Controls = nil
	require.NoError(t, VerifyPlan(plan, exp))
}

func TestVerifyPlanRejectsIdentityAndScheduleDrift(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*schema.ExperimentV2){
		"invalid schema": func(exp *schema.ExperimentV2) { exp.SchemaVersion = "v1" },
		"harness source": func(exp *schema.ExperimentV2) { exp.SourceSHA = BaseSHA },
		"dirty source":   func(exp *schema.ExperimentV2) { exp.Dirty = true },
		"architecture":   func(exp *schema.ExperimentV2) { exp.Environment.Host.Arch = "arm64" },
		"bootstrap seed": func(exp *schema.ExperimentV2) { exp.Seed++ },
		"schedule position": func(exp *schema.ExperimentV2) {
			exp.Schedule[0].Position, exp.Schedule[1].Position = exp.Schedule[1].Position, exp.Schedule[0].Position
			exp.Trials[0].ScheduledTrial, exp.Trials[1].ScheduledTrial = exp.Schedule[0], exp.Schedule[1]
		},
		"valid different config": func(exp *schema.ExperimentV2) {
			exp.Config = []byte(`{"name":"different"}`)
			exp.ConfigSHA256, _ = schema.ConfigDigest(exp.Config)
		},
		"missing unused artifact": func(exp *schema.ExperimentV2) {
			exp.Artifacts = slices.DeleteFunc(exp.Artifacts, func(a schema.Artifact) bool { return a.ID == "base-stub" })
		},
		"extra configuration": func(exp *schema.ExperimentV2) {
			exp.Configurations = append(exp.Configurations, schema.Configuration{ID: "unplanned", ArtifactID: "native-v1"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			plan, exp := summaryFixture(t)
			mutate(exp)
			require.Error(t, VerifyPlan(plan, exp))
		})
	}
	for _, id := range []string{"base-stub", "base-packer", fixtureBaseFat, "stub", "head-packer", fixtureFat,
		"native-v1", "native-v3", fixtureHarness, fixtureFortio} {
		for _, field := range []string{"source", "toolchain", "dirty", "VCS revision"} {
			t.Run(id+"/"+field, func(t *testing.T) {
				t.Parallel()
				plan, exp := summaryFixture(t)
				for i := range exp.Artifacts {
					a := &exp.Artifacts[i]
					if a.ID != id {
						continue
					}
					switch field {
					case "source":
						a.SourceSHA = "unknown"
					case "toolchain":
						a.GoVersion = "go1.27.1"
					case "dirty":
						a.Settings["vcs.modified"] = "true"
					case "VCS revision":
						a.Settings["vcs.revision"] = "unknown"
					}
				}
				require.Error(t, VerifyPlan(plan, exp))
			})
		}
	}
}

func TestVerifyPlanRejectsArmAndRuntimeDrift(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*schema.Configuration){
		"artifact":    func(cfg *schema.Configuration) { cfg.ArtifactID = fixtureFat },
		"ISA level":   func(cfg *schema.Configuration) { cfg.Level = "v1" },
		"mode":        func(cfg *schema.Configuration) { cfg.Mode = "memfd" },
		"cache state": func(cfg *schema.Configuration) { cfg.Cache = "warm" },
		"tuning":      func(cfg *schema.Configuration) { cfg.Tuning = "on-off" },
		"runtime":     func(cfg *schema.Configuration) { cfg.Environment["GOMAXPROCS"] = "3" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			plan, exp := summaryFixture(t)
			for i := range exp.Configurations {
				if exp.Configurations[i].ID == "base-fat-cache-cold" {
					mutate(&exp.Configurations[i])
				}
			}
			require.Error(t, VerifyPlan(plan, exp))
		})
	}
	for _, id := range []string{"base-native-generic", "head-native-specialized"} {
		for _, field := range []string{"artifact", "level", "mode"} {
			t.Run(id+"/"+field, func(t *testing.T) {
				t.Parallel()
				plan, exp := summaryFixture(t)
				for i := range exp.Configurations {
					cfg := &exp.Configurations[i]
					if cfg.ID != id {
						continue
					}
					switch field {
					case "artifact":
						cfg.ArtifactID = fixtureFat
					case "level":
						cfg.Level = "v4"
					case "mode":
						cfg.Mode = "cache"
					}
				}
				require.Error(t, VerifyPlan(plan, exp))
			})
		}
	}
}

func TestVerifyPlanRejectsControlDriftAndContradictions(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"target.affinity", "generator.affinity", "target.cpu.max", "generator.cpu.max",
		"target.memory.max", "generator.memory.max"} {
		for _, field := range []string{"requested", "effective", "state", "duplicate", "missing"} {
			t.Run(name+"/"+field, func(t *testing.T) {
				t.Parallel()
				plan, exp := summaryFixture(t)
				controls := &exp.Trials[0].Controls
				for i := range *controls {
					if (*controls)[i].Name != name {
						continue
					}
					switch field {
					case "requested":
						(*controls)[i].Requested = "other"
					case "effective":
						(*controls)[i].Effective = "200000"
					case "state":
						(*controls)[i].State = "unavailable"
					case "duplicate":
						*controls = append(*controls, (*controls)[i])
					case "missing":
						*controls = slices.Delete(*controls, i, i+1)
					}
					break
				}
				require.Error(t, VerifyPlan(plan, exp))
			})
		}
	}
}

func TestVerifyPlanRejectsEffectiveRuntimeAndPayloadDrift(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"go_version", "level", "gomaxprocs", "gomemlimit", "gogc",
		"MICROFAT_EXEC_MODE", "MICROFAT_SELECTED_VARIANT", "MICROFAT_SELECTED_SHA256"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			plan, exp := summaryFixture(t)
			for i := range exp.Trials {
				trial := &exp.Trials[i]
				if trial.ConfigurationID == "head-fat-cache-cold" {
					trial.Target.Effective[name] = "contradictory"
					break
				}
			}
			require.Error(t, VerifyPlan(plan, exp))
		})
	}
}

func TestAnalyzeBundleRetainsFailureReasonsAndQualifiedSuccess(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, reason string
		mutate       func(*schema.ExperimentV2)
		tamper       bool
	}{
		{name: "qualified success"},
		{name: "checksum tampering", reason: "bundle verification", tamper: true},
		{name: "wrong architecture", reason: "protocol verification", mutate: func(exp *schema.ExperimentV2) {
			exp.Environment.Host.Arch = "arm64"
		}},
		{name: "unqualified provenance", reason: "hosted runner provenance", mutate: func(exp *schema.ExperimentV2) {
			exp.Runner = nil
		}},
		{name: "wrong measurement scope", reason: "analysis:", mutate: func(exp *schema.ExperimentV2) {
			m := exp.Trials[0].Metrics[analysisMetric]
			m.Phase = "startup"
			exp.Trials[0].Metrics[analysisMetric] = m
		}},
		{name: "missing service p99", reason: "missing service-p99", mutate: func(exp *schema.ExperimentV2) {
			delete(exp.Trials[0].Metrics, analysisMetric)
		}},
		{name: "partial retained bundle", reason: "release schedule", mutate: func(exp *schema.ExperimentV2) {
			exp.Complete = false
			exp.Trials = exp.Trials[:len(exp.Trials)-1]
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan, exp := summaryFixture(t)
			qualifySummaryFixture(exp)
			if test.mutate != nil {
				test.mutate(exp)
			}
			bundle := filepath.Join(t.TempDir(), "bundle")
			require.NoError(t, report.WriteBundle(exp, bundle, summaryEvidence(t, exp)))
			if test.tamper {
				require.NoError(t, os.WriteFile(filepath.Join(bundle, "raw.json"), []byte("{}"), fileMode))
			}
			summary := Summary{Plan: &plan, Bundle: bundle}
			analyzeBundle(&summary)
			if test.reason == "" {
				require.True(t, summary.Complete, summary.Reasons)
				require.NotNil(t, summary.Qualification)
				assert.True(t, summary.Qualification.Publishable)
				require.NotNil(t, summary.Analysis)
				assert.Equal(t, analysisEquivalent, summary.Analysis.Status)
			} else {
				assert.False(t, summary.Complete)
				assert.Contains(t, strings.Join(summary.Reasons, "\n"), test.reason)
			}
		})
	}
}

func TestSummarizePreservesMissingAndAmbiguousEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, reason string
		plan         bool
		bundles      int
	}{
		{name: "no plan or bundle", reason: "no valid frozen plan"},
		{name: "no completed bundle", plan: true, reason: "exactly one completed"},
		{name: "ambiguous completed bundles", plan: true, bundles: 2, reason: "exactly one completed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			output := t.TempDir()
			if test.plan {
				plan, _ := summaryFixture(t)
				require.NoError(t, writeJSON(filepath.Join(output, planName), plan))
			}
			for i := range test.bundles {
				path := filepath.Join(output, "evidence", fmt.Sprintf("bundle-%d", i))
				require.NoError(t, os.MkdirAll(path, directoryMode))
				require.NoError(t, os.WriteFile(filepath.Join(path, "SHA256SUMS"), nil, fileMode))
			}
			require.Error(t, Summarize(output))
			data, err := os.ReadFile(filepath.Join(output, "summary.json"))
			require.NoError(t, err)
			var summary Summary
			require.NoError(t, json.Unmarshal(data, &summary))
			assert.False(t, summary.Complete)
			assert.Contains(t, strings.Join(summary.Reasons, "\n"), test.reason)
			assert.Contains(t, summary.Disposition, "Maintainer review required")
			text, err := os.ReadFile(filepath.Join(output, "summary.md"))
			require.NoError(t, err)
			assert.Contains(t, string(text), "Complete qualified cohort: false")
		})
	}
}

func TestSummarizeCompleteEvidenceDoesNotHideInconclusiveAttribution(t *testing.T) {
	t.Parallel()
	output := t.TempDir()
	plan, exp := summaryFixture(t)
	qualifySummaryFixture(exp)
	shiftHeadMetric(exp, "native-generic", 0.001)
	require.NoError(t, writeJSON(filepath.Join(output, planName), plan))
	bundle := filepath.Join(output, "evidence", "retained")
	require.NoError(t, report.WriteBundle(exp, bundle, summaryEvidence(t, exp)))
	require.NoError(t, Summarize(output), "complete measurements may support an inconclusive statistical disposition")
	data, err := os.ReadFile(filepath.Join(output, "summary.json"))
	require.NoError(t, err)
	var summary Summary
	require.NoError(t, json.Unmarshal(data, &summary))
	assert.True(t, summary.Complete)
	require.NotNil(t, summary.Analysis)
	assert.False(t, summary.Analysis.NativeControlsEquivalent)
	assert.Equal(t, analysisInconclusive, summary.Analysis.AttributionStatus)
	assert.Contains(t, summary.Disposition, "Maintainer review required")
}

func summaryFixture(t *testing.T) (Plan, *schema.ExperimentV2) {
	t.Helper()
	plan := investigationPlan(t)
	exp := investigationExperiment(t, plan)
	exp.Complete = true
	for i := range exp.Artifacts {
		exp.Artifacts[i].Settings["vcs.revision"] = exp.Artifacts[i].SourceSHA
	}
	for _, scheduled := range exp.Schedule {
		var cfg schema.Configuration
		for _, configuration := range exp.Configurations {
			if configuration.ID == scheduled.ConfigurationID {
				cfg = configuration
			}
		}
		trial := schema.ProcessTrial{ScheduledTrial: scheduled, Outcome: schema.OutcomeOK, StartedAt: exp.CreatedAt,
			Metrics: map[string]schema.Measurement{analysisMetric: schema.Measured(float64(scheduled.Block+1)/1000,
				"s", "steady_state", "fortio-histogram")}}
		trial.Target.Effective = map[string]string{"go_version": "go1.27.3", "level": cfg.Level,
			"gomaxprocs": "2", "gomemlimit": "1073741824", "gogc": "100"}
		if cfg.Mode != "native" {
			trial.Target.Effective["MICROFAT_EXEC_MODE"] = cfg.Mode
			trial.Target.Effective["MICROFAT_SELECTED_VARIANT"] = cfg.Level
			trial.Target.Effective["MICROFAT_SELECTED_SHA256"] = schema.Digest([]byte("native-" + cfg.Level))
		}
		trial.Controls = []schema.Control{
			{Name: "target.affinity", Requested: "0", Effective: "0", State: fixtureApplied},
			{Name: "generator.affinity", Requested: "3", Effective: "3", State: fixtureApplied},
			{Name: "target.cpu.max", Requested: "100000 100000", Effective: "100000 100000", State: fixtureApplied},
			{Name: "generator.cpu.max", Requested: "100000 100000", Effective: "100000 100000", State: fixtureApplied},
			{Name: "target.memory.max", Requested: "2147483648", Effective: "2147483648", State: fixtureApplied},
			{Name: "generator.memory.max", Requested: "2147483648", Effective: "2147483648", State: fixtureApplied},
		}
		exp.Trials = append(exp.Trials, trial)
	}
	require.NoError(t, schema.ValidateV2(exp))
	return plan, exp
}

func qualifySummaryFixture(exp *schema.ExperimentV2) {
	exp.Runner = &schema.RunnerInfo{Provider: schema.HostedProvider, Image: "ubuntu24", ImageVersion: "synthetic",
		RunID: "1", Attempt: "1", Repository: "owner/repo", Protocol: schema.StartupProtocol}
	for i := range exp.Trials {
		trial := &exp.Trials[i]
		trial.DurationNS, trial.SampleIntervalMS = int64(42*time.Second), sampleIntervalMS
		trial.TelemetryPath = "trials/" + trial.ID + "/telemetry.json"
		for _, name := range []string{"artifact_bytes", "throughput_qps", "peak_rss_bytes", "user_cpu_seconds", "system_cpu_seconds"} {
			trial.Metrics[name] = schema.Measured(1, "count", "synthetic", "fixture")
		}
		trial.Metrics["startup_ns"] = schema.Measured(1, "ns", "startup", schema.StartupProtocol)
		for elapsed := time.Duration(0); elapsed < 41*time.Second; elapsed += sampleIntervalMS * time.Millisecond {
			phase := "startup"
			if elapsed >= time.Second {
				phase = "warmup"
			}
			if elapsed >= 11*time.Second {
				phase = "steady_state"
			}
			trial.Samples = append(trial.Samples, schema.ResourceSample{ElapsedNS: int64(elapsed), Phase: phase})
		}
		trial.SampleCount = len(trial.Samples)
		trial.Load = &schema.HTTPResult{Requests: 1, Successful: 1, StatusCodes: map[string]int64{"200": 1},
			DurationSeconds: 30, ActualQPS: 1, ResolutionSeconds: 0.00001, RawPath: "trials/" + trial.ID + "/fortio.json",
			Buckets: []schema.HistogramBucket{{Start: 0, End: 1, Count: 1}}, PercentilesSeconds: map[string]float64{"p99": 1}}
		warmup := *trial.Load
		warmup.DurationSeconds, warmup.RawPath = 10, "trials/"+trial.ID+"/warmup.json"
		warmup.StatusCodes = maps.Clone(trial.Load.StatusCodes)
		trial.Warmup = &warmup
	}
}

// These synthetic bytes exercise bundle completeness and replay, never performance.
func summaryEvidence(t *testing.T, exp *schema.ExperimentV2) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	for _, trial := range exp.Trials {
		data, err := schema.CanonicalV2(trial.Samples)
		require.NoError(t, err)
		files[trial.TelemetryPath] = data
		files[trial.Load.RawPath], files[trial.Warmup.RawPath] = []byte("{}"), []byte("{}")
	}
	return files
}
