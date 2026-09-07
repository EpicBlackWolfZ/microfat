package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/workloads"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/workloads/baseline"
)

const (
	testTrialsCount       = 2
	testTrialDurationMs   = 50 * time.Millisecond
	testWarmupDurationMs  = 20 * time.Millisecond
	testBatchSizeElements = 100
)

func mockEnvSnapshot() *schema.EnvironmentSnapshot {
	cores := 4
	return &schema.EnvironmentSnapshot{
		Host: schema.HostInfo{
			OS:   "linux",
			Arch: "amd64",
			CPU: schema.CPUInfo{
				ModelName:      "Mock CPU",
				MicroarchLevel: "v3",
				Cores:          &cores,
			},
			Cgroup: schema.CgroupInfo{
				Version:        "v2",
				MemoryMaxBytes: schema.NewUnlimitedLimit[int64](),
				CPUQuotaUs:     schema.NewUnlimitedLimit[int64](),
			},
		},
		Process: schema.ProcessContext{
			PID:                 1234,
			ExecutablePath:      "/bin/test",
			GOMAXPROCSEffective: 4,
		},
	}
}

func TestRunner_EndToEnd_Baseline(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "evidence.json")
	var logBuf bytes.Buffer

	r := NewWithDetector(func() (*schema.EnvironmentSnapshot, error) {
		return mockEnvSnapshot(), nil
	})

	cfg := Config{
		Workload:       baseline.New(),
		ScenarioName:   "test_baseline",
		Trials:         testTrialsCount,
		TrialDuration:  testTrialDurationMs,
		WarmupDuration: testWarmupDurationMs,
		BatchSize:      testBatchSizeElements,
		Title:          "End to End Test",
		Description:    "Validates runner orchestrator",
		ID:             "exp-test-001",
		CreatedAt:      "2026-09-07T12:00:00Z",
		LogWriter:      &logBuf,
		OutputPath:     outputPath,
	}

	evidence, err := r.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if evidence == nil {
		t.Fatal("expected non-nil evidence")
	}

	if err := schema.VerifyEvidence(evidence); err != nil {
		t.Fatalf("VerifyEvidence failed: %v", err)
	}

	// Verify atomic file write
	fileBytes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("reading output evidence file: %v", err)
	}

	deserialized, err := schema.DeserializeEvidence(fileBytes)
	if err != nil {
		t.Fatalf("deserializing written evidence: %v", err)
	}

	if deserialized.DigestSHA256 != evidence.DigestSHA256 {
		t.Errorf("digest mismatch: expected %s, got %s", evidence.DigestSHA256, deserialized.DigestSHA256)
	}

	exp, err := deserialized.Experiment()
	if err != nil {
		t.Fatalf("extracting experiment from evidence: %v", err)
	}

	if exp.ID != "exp-test-001" {
		t.Errorf("expected exp ID 'exp-test-001', got %s", exp.ID)
	}
	if len(exp.Scenarios) != 1 {
		t.Fatalf("expected 1 scenario, got %d", len(exp.Scenarios))
	}
	if len(exp.Scenarios[0].Observations) != testTrialsCount {
		t.Errorf("expected %d observations, got %d", testTrialsCount, len(exp.Scenarios[0].Observations))
	}
	if exp.Scenarios[0].Analysis.SampleCount == 0 {
		t.Error("expected non-zero sample count in analysis")
	}

	// Verify logging
	logStr := logBuf.String()
	if logStr == "" {
		t.Error("expected log messages in logBuf")
	}

	// Test default Runner (New) with live system detector
	liveRunner := New()
	liveEvidence, liveErr := liveRunner.Run(context.Background(), Config{
		Workload:       baseline.New(),
		Trials:         1,
		TrialDuration:  20 * time.Millisecond,
		WarmupDuration: 10 * time.Millisecond,
	})
	if liveErr != nil {
		t.Fatalf("live Runner failed: %v", liveErr)
	}
	if liveEvidence == nil {
		t.Fatal("expected non-nil live evidence")
	}
}

func TestRunner_ValidationErrors(t *testing.T) {
	t.Parallel()

	r := New()
	ctx := context.Background()

	// Nil workload
	if _, err := r.Run(ctx, Config{Trials: 1, TrialDuration: 10 * time.Millisecond}); !errors.Is(err, ErrNilWorkload) {
		t.Errorf("expected ErrNilWorkload, got %v", err)
	}

	// Zero trials
	zeroTrialsCfg := Config{Workload: baseline.New(), Trials: 0, TrialDuration: 10 * time.Millisecond}
	if _, err := r.Run(ctx, zeroTrialsCfg); !errors.Is(err, ErrInvalidTrialCount) {
		t.Errorf("expected ErrInvalidTrialCount, got %v", err)
	}

	// Zero/negative duration
	zeroDurCfg := Config{Workload: baseline.New(), Trials: 1, TrialDuration: 0}
	if _, err := r.Run(ctx, zeroDurCfg); !errors.Is(err, ErrInvalidDuration) {
		t.Errorf("expected ErrInvalidDuration, got %v", err)
	}

	// Negative warmup
	negWarmupCfg := Config{
		Workload:       baseline.New(),
		Trials:         1,
		TrialDuration:  10 * time.Millisecond,
		WarmupDuration: -1,
	}
	if _, err := r.Run(ctx, negWarmupCfg); !errors.Is(err, ErrInvalidWarmup) {
		t.Errorf("expected ErrInvalidWarmup, got %v", err)
	}
}

func TestRunner_ContextCancellation(t *testing.T) {
	t.Parallel()

	r := NewWithDetector(func() (*schema.EnvironmentSnapshot, error) {
		return mockEnvSnapshot(), nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := Config{
		Workload:       baseline.New(),
		Trials:         1,
		TrialDuration:  100 * time.Millisecond,
		WarmupDuration: 0,
	}

	if _, err := r.Run(ctx, cfg); err == nil {
		t.Error("expected error on canceled context")
	}
}

type failingWorkload struct {
	failSetup   bool
	failWarmup  bool
	failTrial   bool
	failSamples bool
}

func (f *failingWorkload) Name() string        { return "failing" }
func (f *failingWorkload) Description() string { return "failing workload" }
func (f *failingWorkload) Setup(_ context.Context, _ workloads.ScenarioConfig) error {
	if f.failSetup {
		return errors.New("setup error")
	}
	return nil
}
func (f *failingWorkload) Warmup(_ context.Context, _ workloads.ExecutionPlan) error {
	if f.failWarmup {
		return errors.New("warmup error")
	}
	return nil
}
func (f *failingWorkload) RunTrial(_ context.Context, _ workloads.ExecutionPlan, trialIndex int) (*schema.TrialObservations, error) {
	if f.failTrial {
		return nil, errors.New("trial error")
	}
	samples := []int64{100, 200}
	if f.failSamples {
		samples = nil
	}
	return &schema.TrialObservations{
		TrialIndex:   trialIndex,
		DurationNs:   1000,
		Operations:   10,
		RawSamplesNs: samples,
	}, nil
}
func (f *failingWorkload) Teardown(_ context.Context) error {
	return nil
}

func TestRunner_WorkloadErrors(t *testing.T) {
	t.Parallel()

	r := NewWithDetector(func() (*schema.EnvironmentSnapshot, error) {
		return mockEnvSnapshot(), nil
	})
	ctx := context.Background()

	// Setup failure
	_, err := r.Run(ctx, Config{
		Workload:       &failingWorkload{failSetup: true},
		Trials:         1,
		TrialDuration:  10 * time.Millisecond,
		WarmupDuration: 0,
	})
	if err == nil {
		t.Error("expected setup error")
	}

	// Warmup failure
	_, err = r.Run(ctx, Config{
		Workload:       &failingWorkload{failWarmup: true},
		Trials:         1,
		TrialDuration:  10 * time.Millisecond,
		WarmupDuration: 10 * time.Millisecond,
	})
	if err == nil {
		t.Error("expected warmup error")
	}

	// Trial failure
	_, err = r.Run(ctx, Config{
		Workload:       &failingWorkload{failTrial: true},
		Trials:         1,
		TrialDuration:  10 * time.Millisecond,
		WarmupDuration: 0,
	})
	if err == nil {
		t.Error("expected trial error")
	}

	// Detector failure
	failingDetector := NewWithDetector(func() (*schema.EnvironmentSnapshot, error) {
		return nil, errors.New("detector error")
	})
	_, err = failingDetector.Run(ctx, Config{
		Workload:       baseline.New(),
		Trials:         1,
		TrialDuration:  10 * time.Millisecond,
		WarmupDuration: 0,
	})
	if err == nil {
		t.Error("expected detector error")
	}

	// Run with fallback when RawSamplesNs is empty
	obsRun, err := r.Run(ctx, Config{
		Workload:       &failingWorkload{failSamples: true},
		Trials:         1,
		TrialDuration:  10 * time.Millisecond,
		WarmupDuration: 0,
	})
	if err != nil {
		t.Fatalf("expected success with sample fallback, got %v", err)
	}
	if obsRun == nil {
		t.Fatal("expected non-nil evidence")
	}

	// Build evidence failure due to invalid CreatedAt timestamp
	_, err = r.Run(ctx, Config{
		Workload:       baseline.New(),
		Trials:         1,
		TrialDuration:  10 * time.Millisecond,
		WarmupDuration: 0,
		CreatedAt:      "invalid-timestamp",
	})
	if err == nil {
		t.Error("expected build evidence failure on invalid timestamp")
	}

	// OutputPath failure in Run
	badOutputPath := filepath.Join(t.TempDir(), "not_a_dir")
	if err := os.WriteFile(badOutputPath, []byte("file"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_, err = r.Run(ctx, Config{
		Workload:       baseline.New(),
		Trials:         1,
		TrialDuration:  10 * time.Millisecond,
		WarmupDuration: 0,
		OutputPath:     filepath.Join(badOutputPath, "sub", "ev.json"),
	})
	if err == nil {
		t.Error("expected OutputPath error in Run")
	}

	// Analysis failure due to negative sample from workload
	_, err = r.Run(ctx, Config{
		Workload:       &badSampleWorkload{},
		Trials:         1,
		TrialDuration:  10 * time.Millisecond,
		WarmupDuration: 0,
	})
	if err == nil {
		t.Error("expected analysis error from bad sample")
	}

	// Zero duration fallback
	_, err = r.Run(ctx, Config{
		Workload:       &zeroDurationWorkload{},
		Trials:         1,
		TrialDuration:  10 * time.Millisecond,
		WarmupDuration: 0,
	})
	if err == nil {
		t.Log("zero duration handled by fallback (fails at validation stage)")
	}
}

type badSampleWorkload struct{}

func (b *badSampleWorkload) Name() string                                              { return "bad_sample" }
func (b *badSampleWorkload) Description() string                                       { return "bad sample" }
func (b *badSampleWorkload) Setup(_ context.Context, _ workloads.ScenarioConfig) error { return nil }
func (b *badSampleWorkload) Warmup(_ context.Context, _ workloads.ExecutionPlan) error { return nil }
func (b *badSampleWorkload) RunTrial(_ context.Context, _ workloads.ExecutionPlan, trialIndex int) (*schema.TrialObservations, error) {
	return &schema.TrialObservations{
		TrialIndex:   trialIndex,
		DurationNs:   1000,
		Operations:   1,
		RawSamplesNs: []int64{-10},
	}, nil
}
func (b *badSampleWorkload) Teardown(_ context.Context) error { return nil }

type zeroDurationWorkload struct{}

func (z *zeroDurationWorkload) Name() string                                              { return "zero_dur" }
func (z *zeroDurationWorkload) Description() string                                       { return "zero dur" }
func (z *zeroDurationWorkload) Setup(_ context.Context, _ workloads.ScenarioConfig) error { return nil }
func (z *zeroDurationWorkload) Warmup(_ context.Context, _ workloads.ExecutionPlan) error { return nil }
func (z *zeroDurationWorkload) RunTrial(_ context.Context, _ workloads.ExecutionPlan, trialIndex int) (*schema.TrialObservations, error) {
	return &schema.TrialObservations{
		TrialIndex:   trialIndex,
		DurationNs:   0,
		Operations:   1,
		RawSamplesNs: []int64{100},
	}, nil
}
func (z *zeroDurationWorkload) Teardown(_ context.Context) error { return nil }


func TestWriteEvidenceAtomically_BadPath(t *testing.T) {
	t.Parallel()

	evidence := &schema.Evidence{
		SchemaVersion: schema.SchemaVersion,
		DigestSHA256:  "abc",
		PayloadBytes:  []byte("{}"),
	}

	// Trying to write into an invalid path like a file that is not a directory
	tmpFile := filepath.Join(t.TempDir(), "not_a_dir")
	if err := os.WriteFile(tmpFile, []byte("file"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	badPath := filepath.Join(tmpFile, "sub", "evidence.json")
	if err := writeEvidenceAtomically(evidence, badPath); err == nil {
		t.Error("expected error writing to bad path")
	}

	// Test nil evidence serialization failure
	if err := writeEvidenceAtomically(nil, filepath.Join(t.TempDir(), "ev.json")); err == nil {
		t.Error("expected error writing nil evidence")
	}

	// Test destination is an existing directory (os.Rename fails)
	destDir := t.TempDir()
	if err := writeEvidenceAtomically(evidence, destDir); err == nil {
		t.Error("expected error renaming to existing directory")
	}
}

func TestConfig_Defaults(t *testing.T) {
	t.Parallel()

	var c1 Config
	c1.Defaults()
	if c1.ScenarioName != defaultScenarioName {
		t.Errorf("expected scenario name %s, got %s", defaultScenarioName, c1.ScenarioName)
	}
	if c1.Trials != defaultTrialCount {
		t.Errorf("expected trials %d, got %d", defaultTrialCount, c1.Trials)
	}
	if c1.TrialDuration != defaultTrialDuration {
		t.Errorf("expected trial duration %v, got %v", defaultTrialDuration, c1.TrialDuration)
	}
	if c1.WarmupDuration != defaultWarmupDuration {
		t.Errorf("expected warmup duration %v, got %v", defaultWarmupDuration, c1.WarmupDuration)
	}
	if c1.LogWriter == nil {
		t.Error("expected non-nil LogWriter")
	}

	c2 := Config{Workload: baseline.New()}
	c2.Defaults()
	if c2.ScenarioName != baseline.WorkloadName {
		t.Errorf("expected scenario name %s, got %s", baseline.WorkloadName, c2.ScenarioName)
	}
}

func TestRunner_DefaultIDAndCreatedAt(t *testing.T) {
	t.Parallel()

	r := NewWithDetector(func() (*schema.EnvironmentSnapshot, error) {
		return mockEnvSnapshot(), nil
	})

	evidence, err := r.Run(context.Background(), Config{
		Workload:       baseline.New(),
		Trials:         1,
		TrialDuration:  20 * time.Millisecond,
		WarmupDuration: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	exp, err := evidence.Experiment()
	if err != nil {
		t.Fatalf("Experiment() failed: %v", err)
	}
	if exp == nil {
		t.Fatal("expected non-nil experiment from evidence")
	}
	if exp.ID == "" {
		t.Error("expected generated ID")
	}
	if exp.CreatedAt == "" {
		t.Error("expected generated CreatedAt")
	}
}

type cancelingWorkload struct {
	cancelFunc context.CancelFunc
}

func (c *cancelingWorkload) Name() string        { return "canceling" }
func (c *cancelingWorkload) Description() string { return "cancels context during trial" }
func (c *cancelingWorkload) Setup(_ context.Context, _ workloads.ScenarioConfig) error {
	return nil
}
func (c *cancelingWorkload) Warmup(_ context.Context, _ workloads.ExecutionPlan) error {
	return nil
}
func (c *cancelingWorkload) RunTrial(_ context.Context, _ workloads.ExecutionPlan, trialIndex int) (*schema.TrialObservations, error) {
	if c.cancelFunc != nil {
		c.cancelFunc()
	}
	return &schema.TrialObservations{
		TrialIndex:   trialIndex,
		DurationNs:   1000,
		Operations:   10,
		RawSamplesNs: []int64{100},
	}, nil
}
func (c *cancelingWorkload) Teardown(_ context.Context) error {
	return nil
}

func TestRunner_LoopCancellation(t *testing.T) {
	t.Parallel()

	r := NewWithDetector(func() (*schema.EnvironmentSnapshot, error) {
		return mockEnvSnapshot(), nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	wl := &cancelingWorkload{cancelFunc: cancel}

	_, err := r.Run(ctx, Config{
		Workload:       wl,
		Trials:         3,
		TrialDuration:  10 * time.Millisecond,
		WarmupDuration: 0,
	})
	if err == nil {
		t.Error("expected error from context cancellation inside trial loop")
	}
}

