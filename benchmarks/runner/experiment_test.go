package runner

import (
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/internal/testfixture"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/load"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/report"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const unknownConfig = "unknown"

func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "benchmark" && os.Args[2] == "observe" {
		var cfg system.ObserverConfig
		err := json.Unmarshal([]byte(os.Args[3]), &cfg)
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		if err == nil {
			err = system.Observe(ctx, cfg, os.Stdout)
		}
		cancel()
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	if len(os.Args) == 4 && os.Args[1] == "benchmark" && os.Args[2] == "child" {
		cfg, err := system.ParseChild([]byte(os.Args[3]))
		if err == nil {
			err = system.ExecChild(cfg)
		}
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestSuiteConfiguration(t *testing.T) {
	t.Parallel()
	cfg := DefaultExperimentConfig()
	require.NoError(t, cfg.Validate())
	for name, mutate := range map[string]func(*ExperimentConfig){
		"version":     func(c *ExperimentConfig) { c.Version = "v2" },
		"name":        func(c *ExperimentConfig) { c.Name = "../x" },
		"workload":    func(c *ExperimentConfig) { c.Workload = unknownConfig },
		"blocks":      func(c *ExperimentConfig) { c.Blocks = 0 },
		"duration":    func(c *ExperimentConfig) { c.DurationMS = 0 },
		"budget":      func(c *ExperimentConfig) { c.Blocks = 1000; c.DurationMS = 120000 },
		"format":      func(c *ExperimentConfig) { c.Format = 0 },
		"profile":     func(c *ExperimentConfig) { c.Profile = unknownConfig },
		"codec":       func(c *ExperimentConfig) { c.Codec = unknownConfig },
		"dictionary":  func(c *ExperimentConfig) { c.Codec = "none"; c.Dictionary = true },
		"tuning":      func(c *ExperimentConfig) { c.Tuning = unknownConfig },
		"runtime":     func(c *ExperimentConfig) { c.GOMAXPROCS = 0 },
		"cache":       func(c *ExperimentConfig) { c.CacheStates = []string{unknownConfig} },
		"empty cache": func(c *ExperimentConfig) { c.CacheStates = nil },
		"target":      func(c *ExperimentConfig) { c.Target.CPUPeriodUS = 0 },
		"generator":   func(c *ExperimentConfig) { c.Generator.CPUPeriodUS = 0 },
		"overlap":     func(c *ExperimentConfig) { c.Target.Affinity = []int{1}; c.Generator.Affinity = []int{1} },
		"intensity":   func(c *ExperimentConfig) { c.PayloadBytes = 0 },
		"load":        func(c *ExperimentConfig) { c.QPS = -1 },
	} {
		t.Run(name, func(t *testing.T) { c := cfg; mutate(&c); require.Error(t, c.Validate()) })
	}
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"name":"test",unknownConfig:true}`), evidenceFilePerm))
	_, err := ReadExperimentConfig(path)
	require.Error(t, err)
	require.NoError(t, os.WriteFile(path, []byte(`{"name":"test"}`), evidenceFilePerm))
	read, err := ReadExperimentConfig(path)
	require.NoError(t, err)
	assert.Equal(t, "test", read.Name)
	_, err = ReadExperimentConfig(missingExperimentPath)
	require.Error(t, err)
	options := RunOptions{}
	require.Error(t, options.resolve())
	options.Fortio = "fortio"
	require.Error(t, options.resolve())
	options.OutputDir = t.TempDir()
	require.NoError(t, options.resolve())
	assert.True(t, filepath.IsAbs(options.Helper))
}

func TestExperimentEndToEnd(t *testing.T) {
	// Real target build/exec with a saved tool-output fixture exercises orchestration, not performance.
	repository, err := filepath.Abs("../..")
	require.NoError(t, err)
	fixture, err := filepath.Abs("../load/testdata/fortio-1.75.2.json")
	require.NoError(t, err)
	tool := filepath.Join(t.TempDir(), "fortio")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo " + load.Version + "; else cat '" + fixture + "'; fi\n"
	require.NoError(t, testfixture.WriteExecutable(tool, []byte(script)))
	cfg := DefaultExperimentConfig()
	cfg.Blocks = 1
	options := RunOptions{Repository: repository, Fortio: tool, OutputDir: t.TempDir(), LogWriter: io.Discard}
	result, err := RunExperiment(context.Background(), cfg, options)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Experiment.Complete)
	assert.False(t, result.Experiment.ReleaseEligible)
	verified, err := report.ReadBundle(result.Bundle)
	require.NoError(t, err)
	assert.Len(t, verified.Trials, len(verified.Configurations))
	for _, trial := range verified.Trials {
		assert.Equal(t, schema.OutcomeOK, trial.Outcome, trial.Reason)
		assert.Positive(t, trial.Target.PID)
		assert.NotEqual(t, trial.Target.PID, trial.Generator.PID)
		assert.Positive(t, trial.SampleCount)
		assert.NotEmpty(t, trial.TelemetryPath)
	}
	// Exercise base/head with the same source as a no-change orchestration check.
	cfg.Profile, cfg.Dictionary, cfg.ExecDiagnostics = minimalProfile, true, true
	options.BaseRepository = repository
	result, err = RunExperiment(context.Background(), cfg, options)
	require.NoError(t, err)
	assert.NotEmpty(t, result.Experiment.Comparisons)
	assert.Contains(t, result.Experiment.Comparisons[0].Baseline, "base-")
	cfg.SpecializedLevel = result.Experiment.Configurations[0].Level
	cfg.Dictionary = false // A single distinct ISA payload cannot train an inter-variant dictionary.
	cfg.Tuning, cfg.DisableObserver, cfg.ExecDiagnostics = "on-off", true, false
	options.BaseRepository = ""
	result, err = RunExperiment(context.Background(), cfg, options)
	require.NoError(t, err)
	assert.Len(t, result.Experiment.Configurations, 2)
	assert.Empty(t, result.Experiment.Trials[0].Samples)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = RunExperiment(ctx, cfg, options)
	require.Error(t, err)
	_, err = RunExperiment(context.Background(), ExperimentConfig{}, options)
	require.Error(t, err)
	_, err = RunExperiment(context.Background(), cfg, RunOptions{})
	require.Error(t, err)
	options.Go = missingExperimentPath
	_, err = RunExperiment(context.Background(), cfg, options)
	require.Error(t, err)
}

func TestTrialHelpers(t *testing.T) {
	t.Parallel()
	cfg := schema.Configuration{Level: "v1", Mode: "native", Tuning: tuningMatched,
		Environment: map[string]string{"GOMAXPROCS": "1"}}
	diagnostic := map[string]string{"level": "v1", "gomaxprocs": "1"}
	require.NoError(t, validateIdentity(cfg, diagnostic))
	diagnostic["level"] = "v2"
	require.Error(t, validateIdentity(cfg, diagnostic))
	diagnostic["level"] = "v1"
	diagnostic["gomaxprocs"] = "2"
	require.Error(t, validateIdentity(cfg, diagnostic))
	cfg.Mode = "memfd"
	require.Error(t, validateIdentity(cfg, diagnostic))
	assert.Contains(t, targetEnvironment(cfg, "/cache"), "MICROFAT_DISPATCH_MODE=memfd")
	require.Error(t, prewarm(missingExperimentPath, "v1", t.TempDir()))
	file := filepath.Join(t.TempDir(), "invalid")
	require.NoError(t, os.WriteFile(file, []byte("invalid"), evidenceFilePerm))
	require.Error(t, prewarm(file, "v1", t.TempDir()))
	_, err := identify(missingExperimentPath, "x", "source")
	require.Error(t, err)
	a, err := identify(file, "x", "source")
	require.NoError(t, err)
	assert.NotEmpty(t, a.SHA256)
	files := make(map[string][]byte)
	require.Error(t, encodeExtra(files, "x", make(chan int)))
	require.Error(t, persistCheckpoint(missingExperimentPath, nil, nil))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"level":"v1"}`)
	}))
	defer server.Close()
	d, err := diagnostics(context.Background(), server.URL)
	require.NoError(t, err)
	assert.Equal(t, "v1", d["level"])
	_, err = diagnostics(context.Background(), "invalid://%zz")
	require.Error(t, err)
	_, err = diagnostics(context.Background(), "http://127.0.0.1:1")
	require.Error(t, err)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadRequest) }))
	defer bad.Close()
	_, err = diagnostics(context.Background(), bad.URL)
	require.Error(t, err)
	helper, err := os.Executable()
	require.NoError(t, err)
	s, err := startSampler(context.Background(), os.Getpid(), &system.Sandbox{}, 1, helper, filepath.Join(t.TempDir(), "phase"))
	require.NoError(t, err)
	s.setPhase("steady_state")
	require.Eventually(t, func() bool { return len(s.child.Stdout()) > 0 }, time.Second, time.Millisecond)
	samples, err := s.stop()
	require.NoError(t, err)
	assert.NotEmpty(t, samples)

}

func TestReadinessFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, output := range []string{"invalid", `{"url":"http://evil.test"}`} {
		child, err := process.Start(ctx, process.Spec{Path: "/bin/echo", Args: []string{output}})
		require.NoError(t, err)
		_, err = awaitReady(ctx, child)
		require.Error(t, err)
		_ = child.Wait()
	}
	child, err := process.Start(ctx, process.Spec{Path: "/bin/true"})
	require.NoError(t, err)
	_, err = awaitReady(ctx, child)
	require.Error(t, err)
	assert.NotEmpty(t, strings.Join(cleanEnvironment(), "\n"))
}
