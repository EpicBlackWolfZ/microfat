package runner

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/internal/testfixture"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureConfiguration = "test"

func TestCheckpointJournal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	exp := testfixture.Experiment()
	exp.Complete = false
	require.NoError(t, persistCheckpoint(root, exp, map[string][]byte{"trials/first/output": []byte("retained")}))
	data, err := os.ReadFile(filepath.Join(root, "raw.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"complete":false`)
	data, err = os.ReadFile(filepath.Join(root, "trials/first/output"))
	require.NoError(t, err)
	assert.Equal(t, "retained", string(data))
	require.Error(t, persistCheckpoint(root, exp, map[string][]byte{"../escape": nil}))
	require.Error(t, persistCheckpoint(root, exp, map[string][]byte{"trials/first/output/child": nil}))
	require.NoError(t, os.Mkdir(filepath.Join(root, "trials/directory"), defaultDirPerm))
	require.Error(t, persistCheckpoint(root, exp, map[string][]byte{"trials/directory": nil}))
	exp.Trials[0].Metrics["invalid"] = schema.Measured(math.NaN(), "ns", "trial", "fixture")
	require.Error(t, persistCheckpoint(root, exp, nil))
	exp = testfixture.Experiment()
	require.NoError(t, os.Remove(filepath.Join(root, "raw.json")))
	require.NoError(t, os.Mkdir(filepath.Join(root, "raw.json"), defaultDirPerm))
	require.Error(t, persistCheckpoint(root, exp, nil))
}

func TestReleaseEligibilityAndDensity(t *testing.T) {
	t.Parallel()
	cfg := DefaultExperimentConfig()
	cfg.RunnerIdentity, cfg.Blocks = "synthetic-test-runner", 20
	cfg.Target.Affinity, cfg.Generator.Affinity = []int{1}, []int{2}
	cfg.Target.CgroupRoot, cfg.Generator.CgroupRoot = "/target", "/generator"
	exp := testfixture.Experiment()
	http := testfixture.HTTP()
	for i := range exp.Trials {
		exp.Trials[i].Controls = []schema.Control{{Name: "target.cpu.max", State: "applied"}}
		exp.Trials[i].SampleCount = 1
		exp.Trials[i].Load = &http
	}
	assert.True(t, releaseEligible(cfg, exp))
	exp.Warnings = []string{"noisy-environment: fixture"}
	assert.False(t, releaseEligible(cfg, exp))
	exp.Warnings = nil
	exp.Trials[0].Controls[0].State = "unavailable"
	assert.False(t, releaseEligible(cfg, exp))
	exp.Trials[0].Outcome = schema.OutcomeFailed
	assert.False(t, releaseEligible(cfg, exp))
	cfg.DisableObserver = true
	assert.False(t, releaseEligible(cfg, exp))
	trial := &schema.ProcessTrial{
		Metrics:  map[string]schema.Measurement{"throughput_qps": schema.Measured(100, "requests/s", "steady_state", "fortio")},
		Controls: []schema.Control{{Name: "target.cpu.max", State: "applied"}, {Name: "target.memory.max", State: "applied"}}}
	cfg.Target.CPUQuotaUS, cfg.Target.MemoryBytes = 2*system.DefaultPeriod, 1024*1024*1024
	addDensityMetrics(cfg, trial)
	assert.Equal(t, 50.0, *trial.Metrics["throughput_per_quota_vcpu"].Value)
	assert.Equal(t, 100.0, *trial.Metrics["throughput_per_limit_gib"].Value)
	trial.Controls = nil
	addDensityMetrics(cfg, trial)
	assert.Nil(t, trial.Metrics["throughput_per_quota_vcpu"].Value)
}

func TestTrialFailureEvidence(t *testing.T) {
	t.Parallel()
	cfg := DefaultExperimentConfig()
	helper, err := os.Executable()
	require.NoError(t, err)
	opts := RunOptions{Helper: helper, Fortio: missingExperimentPath, LogWriter: io.Discard}
	scheduled := schema.ScheduledTrial{ID: "trial-failure", ConfigurationID: fixtureConfiguration}
	built := &builtArtifacts{
		Configurations: []schema.Configuration{{ID: fixtureConfiguration, ArtifactID: "app", Mode: nativeMode, Level: "v1"}},
		Artifacts:      []schema.Artifact{{ID: "app", Path: missingExperimentPath, Bytes: 1}}}
	for name, alter := range map[string]func(*ExperimentConfig, *RunOptions, *builtArtifacts){
		"target controls":    func(c *ExperimentConfig, _ *RunOptions, _ *builtArtifacts) { c.Target.CPUPeriodUS = 0 },
		"generator controls": func(c *ExperimentConfig, _ *RunOptions, _ *builtArtifacts) { c.Generator.CPUPeriodUS = 0 },
		"missing helper":     func(_ *ExperimentConfig, o *RunOptions, _ *builtArtifacts) { o.Helper = missingExperimentPath },
		"target exits":       func(_ *ExperimentConfig, _ *RunOptions, b *builtArtifacts) { b.Artifacts[0].Path = "/bin/true" },
		"prewarm invalid":    func(_ *ExperimentConfig, _ *RunOptions, b *builtArtifacts) { b.Configurations[0].Cache = "warm" },
		"observer invalid": func(c *ExperimentConfig, _ *RunOptions, b *builtArtifacts) {
			c.SampleMS = 0
			b.Artifacts[0].Path = "/bin/sleep"
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, o := cfg, opts
			b := &builtArtifacts{Configurations: append([]schema.Configuration(nil), built.Configurations...),
				Artifacts: append([]schema.Artifact(nil), built.Artifacts...)}
			alter(&c, &o, b)
			trial, files := runProcessTrial(context.Background(), c, o, t.TempDir(), b, scheduled)
			assert.Equal(t, schema.OutcomeFailed, trial.Outcome)
			assert.NotEmpty(t, trial.Reason)
			assert.Contains(t, files, "trials/trial-failure/outcome.json")
		})
	}
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, scheduled.ID+"-cache"), defaultDirPerm))
	trial, _ := runProcessTrial(context.Background(), cfg, opts, root, built, scheduled)
	assert.Contains(t, trial.Reason, "exists")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	trial, _ = runProcessTrial(ctx, cfg, opts, t.TempDir(), built, scheduled)
	assert.Equal(t, schema.OutcomeCancelled, trial.Outcome)
	var absent *sampler
	absent.setPhase("startup")
	_, err = startSampler(context.Background(), os.Getpid(), &system.Sandbox{}, 1, helper, "/missing/phase")
	require.Error(t, err)
	_, err = startSampler(context.Background(), os.Getpid(), &system.Sandbox{}, 1, missingExperimentPath, filepath.Join(t.TempDir(), "phase"))
	require.Error(t, err)
	s := &sampler{phaseFile: "/missing/phase"}
	s.setPhase("startup")
	require.Error(t, s.phaseErr)
}

func TestMatchedRuntimeReadback(t *testing.T) {
	t.Parallel()
	c := schema.Configuration{Level: "v1", Mode: nativeMode, Tuning: tuningMatched, Environment: map[string]string{
		"GOMAXPROCS": "2", "GOMEMLIMIT": "1GiB", "GOGC": "off"}}
	d := map[string]string{"level": "v1", "gomaxprocs": "2", "gomemlimit": "1073741824", "gogc": "18446744073709551615"}
	require.NoError(t, validateIdentity(c, d))
	d["gogc"] = "100"
	require.Error(t, validateIdentity(c, d))
	d["gomemlimit"] = "10"
	require.Error(t, validateIdentity(c, d))
	for _, mutate := range []func(*ExperimentConfig){
		func(c *ExperimentConfig) { c.GOMEMLIMIT = "bad" }, func(c *ExperimentConfig) { c.GOGC = "bad" },
		func(c *ExperimentConfig) { c.GOGC = "-1" }, func(c *ExperimentConfig) { c.Generator.CPUPeriodUS = 0 },
		func(c *ExperimentConfig) { c.CacheStates = nil },
		func(c *ExperimentConfig) { c.Blocks = 100; c.DurationMS = int(time.Minute / time.Millisecond) },
	} {
		cfg := DefaultExperimentConfig()
		mutate(&cfg)
		require.Error(t, cfg.Validate())
	}
}

func TestLoadPhaseFailureRetention(t *testing.T) {
	t.Parallel()
	helper, err := os.Executable()
	require.NoError(t, err)
	for name, mutate := range map[string]func(*ExperimentConfig){
		"warmup config":       func(c *ExperimentConfig) { c.QPS = -1 },
		"warmup process":      func(c *ExperimentConfig) { c.WarmupMS = 1 },
		"measurement config":  func(c *ExperimentConfig) { c.WarmupMS = 0; c.QPS = -1 },
		"measurement process": func(c *ExperimentConfig) { c.WarmupMS = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultExperimentConfig()
			mutate(&cfg)
			trial := &schema.ProcessTrial{ScheduledTrial: schema.ScheduledTrial{ID: "failure"}, Metrics: make(map[string]schema.Measurement)}
			measureTrial(context.Background(), cfg, RunOptions{Helper: helper, Fortio: missingExperimentPath}, &system.Sandbox{}, nil,
				"http://127.0.0.1:1", trial, make(map[string][]byte))
			assert.NotEmpty(t, trial.Reason)
			assert.NotEqual(t, schema.OutcomeOK, trial.Outcome)
		})
	}
	trial := &schema.ProcessTrial{ScheduledTrial: schema.ScheduledTrial{ID: "failure"}, Metrics: make(map[string]schema.Measurement)}
	finalizeTrial(context.Background(), time.Now(), trial, make(map[string][]byte))
	assert.NotEmpty(t, trial.Reason)
	trial.Metrics["invalid"] = schema.Measured(math.NaN(), "s", "trial", "fixture")
	finalizeTrial(context.Background(), time.Now(), trial, make(map[string][]byte))
	assert.Equal(t, schema.OutcomeFailed, trial.Outcome)
	c := trialCleanup{ctx: context.Background(), trial: trial, files: make(map[string][]byte), target: &system.Sandbox{},
		opts: RunOptions{Helper: helper}}
	c.artifact.Path = "/missing/target"
	c.diagnose()
	assert.Contains(t, trial.Warnings[0], "cache")
	c.artifact.Path = filepath.Join(t.TempDir(), "missing")
	c.config.Cache = "warm"
	c.diagnose()
	assert.Contains(t, trial.Warnings[1], "prewarm")
	c.config.Cache = "cold"
	c.diagnose()
	assert.Contains(t, trial.Warnings[2], "exec diagnostic")
}

func TestTargetHandshakeFailures(t *testing.T) {
	t.Parallel()
	helper, err := os.Executable()
	require.NoError(t, err)
	for name, diagnostic := range map[string]string{
		"invalid diagnostic": "{",
		"wrong identity":     `{"level":"wrong"}`,
		"payload mismatch":   `{"level":"v1","MICROFAT_EXEC_MODE":"memfd","MICROFAT_SELECTED_VARIANT":"v1","MICROFAT_SELECTED_SHA256":"wrong"}`,
		"observer startup":   `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, diagnostic) }))
			defer server.Close()
			root := t.TempDir()
			target := filepath.Join(root, "target")
			script := "#!/bin/sh\ntrap 'exit 0' TERM\necho '{\"url\":\"" + server.URL + "\"}'\nwhile :; do sleep 1; done\n"
			require.NoError(t, testfixture.WriteExecutable(target, []byte(script)))
			built := &builtArtifacts{
				Configurations: []schema.Configuration{{ID: fixtureConfiguration, ArtifactID: "fat", Mode: "memfd", Level: "v1"}},
				Artifacts:      []schema.Artifact{{ID: "fat", Path: target, Bytes: 1}, {ID: "native-v1", SHA256: "expected", Bytes: 1}}}
			scheduled := schema.ScheduledTrial{ID: "handshake", ConfigurationID: fixtureConfiguration}
			if name == "observer startup" {
				require.NoError(t, os.Mkdir(filepath.Join(root, "handshake-phase"), defaultDirPerm))
			}
			trial, files := runProcessTrial(context.Background(), DefaultExperimentConfig(), RunOptions{Helper: helper}, root, built, scheduled)
			assert.Equal(t, schema.OutcomeFailed, trial.Outcome)
			assert.NotEmpty(t, trial.Reason)
			assert.NotEmpty(t, files)
		})
	}
}

func TestCancelledScheduleAndCheckpointFailure(t *testing.T) {
	t.Parallel()
	helper, err := os.Executable()
	require.NoError(t, err)
	built := &builtArtifacts{
		Configurations: []schema.Configuration{{ID: fixtureConfiguration, ArtifactID: "app", Mode: nativeMode, Level: "v1"}},
		Artifacts:      []schema.Artifact{{ID: "app", Path: "/bin/false", Bytes: 1}}}
	exp := &schema.ExperimentV2{Schedule: []schema.ScheduledTrial{{ID: "failed", ConfigurationID: fixtureConfiguration}}}
	cfg := DefaultExperimentConfig()
	opts := RunOptions{Helper: helper, LogWriter: io.Discard}
	require.Error(t, executeSchedule(context.Background(), cfg, opts, t.TempDir(), "/missing", built, exp, make(map[string][]byte)))
	exp.Trials = nil
	require.NoError(t, executeSchedule(context.Background(), cfg, opts, t.TempDir(), t.TempDir(), built, exp, make(map[string][]byte)))
	assert.Equal(t, schema.OutcomeFailed, exp.Trials[0].Outcome)
}

func TestHTTPFailuresRemainMeasurements(t *testing.T) {
	t.Parallel()
	// Synthetic adapter output exercises failure accounting, never performance claims.
	raw, err := os.ReadFile("../load/testdata/fortio-1.75.2.json")
	require.NoError(t, err)
	raw = bytes.ReplaceAll(raw, []byte(`"200"`), []byte(`"500"`))
	root := t.TempDir()
	fixture := filepath.Join(root, "errors.json")
	require.NoError(t, os.WriteFile(fixture, raw, evidenceFilePerm))
	tool := filepath.Join(root, "fortio")
	require.NoError(t, testfixture.WriteExecutable(tool, []byte("#!/bin/sh\ncat '"+fixture+"'\n")))
	helper, err := os.Executable()
	require.NoError(t, err)
	cfg := DefaultExperimentConfig()
	cfg.WarmupMS = 0
	trial := &schema.ProcessTrial{ScheduledTrial: schema.ScheduledTrial{ID: "http-errors"}, Metrics: make(map[string]schema.Measurement)}
	files := make(map[string][]byte)
	measureTrial(context.Background(), cfg, RunOptions{Helper: helper, Fortio: tool}, &system.Sandbox{}, nil,
		"http://127.0.0.1:1", trial, files)
	require.NotNil(t, trial.Load)
	assert.Zero(t, trial.Load.Successful)
	assert.Positive(t, *trial.Metrics["request_errors"].Value)
	assert.Equal(t, "HTTP or transport errors", trial.Reason)
	assert.Contains(t, files, "trials/http-errors/fortio.json")
}

func TestObserverOutputAndReadinessErrors(t *testing.T) {
	t.Parallel()
	for _, line := range []string{"invalid", `{"url":"http://elsewhere.test"}`} {
		child, err := process.Start(context.Background(), process.Spec{Path: "/bin/sh",
			Args: []string{"-c", "printf '%s\\n' \"$1\"; sleep 1", "fixture", line}})
		require.NoError(t, err)
		_, err = awaitReady(context.Background(), child)
		require.Error(t, err)
		_ = child.Wait()
	}
	child, err := process.Start(context.Background(), process.Spec{Path: "/bin/sleep", Args: []string{"0.05"}})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = awaitReady(ctx, child)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, child.Wait())
	child, err = process.Start(context.Background(), process.Spec{Path: "/bin/echo", Args: []string{"malformed"}})
	require.NoError(t, err)
	require.NoError(t, child.Wait())
	s := &sampler{child: child}
	_, err = s.stop()
	require.Error(t, err)
}

func TestRetainedEvidenceBudget(t *testing.T) {
	t.Parallel()
	files := make(map[string][]byte)
	total := 0
	require.NoError(t, retainRaw(files, map[string][]byte{"trials/one": []byte("one")}, &total))
	assert.Equal(t, 3, total)
	total = 256 * 1024 * 1024
	require.Error(t, retainRaw(files, map[string][]byte{"trials/two": []byte("two")}, &total))
	assert.NotContains(t, files, "trials/two")
}
