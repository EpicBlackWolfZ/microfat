package baseline

import (
	"context"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/workloads"
)

const (
	testBatchSize    = 100
	testTargetIter   = int64(1000)
	testDurationWarm = 50 * time.Millisecond
	testDurationRun  = 100 * time.Millisecond
	testStepCount    = 5
)

func TestBaselineWorkload_Lifecycle(t *testing.T) {
	t.Parallel()

	w := New()
	if w.Name() != WorkloadName {
		t.Errorf("expected name %s, got %s", WorkloadName, w.Name())
	}
	if w.Description() != WorkloadDescription {
		t.Errorf("expected description %s, got %s", WorkloadDescription, w.Description())
	}

	ctx := context.Background()

	// Calling Warmup or RunTrial before Setup should return ErrNotSetup
	plan := workloads.ExecutionPlan{
		Mode:             workloads.ModeIterations,
		TargetIterations: testTargetIter,
		BatchSize:        testBatchSize,
	}
	if err := w.Warmup(ctx, plan); err != ErrNotSetup {
		t.Errorf("expected ErrNotSetup on Warmup, got %v", err)
	}
	if _, err := w.RunTrial(ctx, plan, 0); err != ErrNotSetup {
		t.Errorf("expected ErrNotSetup on RunTrial, got %v", err)
	}

	// Setup with custom seed
	cfg := workloads.ScenarioConfig{
		Name: "test_scenario",
		Parameters: map[string]string{
			"seed": "987654321",
		},
	}
	if err := w.Setup(ctx, cfg); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Warmup
	warmupPlan := workloads.ExecutionPlan{
		Mode:           workloads.ModeDuration,
		TargetDuration: testDurationWarm,
		BatchSize:      testBatchSize,
	}
	if err := w.Warmup(ctx, warmupPlan); err != nil {
		t.Fatalf("Warmup failed: %v", err)
	}

	// RunTrial (iterations mode)
	obs, err := w.RunTrial(ctx, plan, 1)
	if err != nil {
		t.Fatalf("RunTrial failed: %v", err)
	}

	if obs.TrialIndex != 1 {
		t.Errorf("expected trial index 1, got %d", obs.TrialIndex)
	}
	if obs.Operations < testTargetIter {
		t.Errorf("expected at least %d operations, got %d", testTargetIter, obs.Operations)
	}
	if len(obs.RawSamplesNs) == 0 {
		t.Error("expected non-empty RawSamplesNs")
	}
	if obs.DurationNs <= 0 {
		t.Errorf("expected positive DurationNs, got %d", obs.DurationNs)
	}
	if obs.StartTime == "" || obs.EndTime == "" {
		t.Error("expected non-empty StartTime and EndTime")
	}
	if obs.Metrics["checksum"] == 0 {
		t.Error("expected non-zero checksum metric")
	}

	// Teardown
	if err := w.Teardown(ctx); err != nil {
		t.Fatalf("Teardown failed: %v", err)
	}

	// After Teardown, workload should no longer be configured
	if err := w.Warmup(ctx, plan); err != ErrNotSetup {
		t.Errorf("expected ErrNotSetup after Teardown, got %v", err)
	}
}

func TestBaselineWorkload_Determinism(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	plan := workloads.ExecutionPlan{
		Mode:             workloads.ModeIterations,
		TargetIterations: 5000,
		BatchSize:        500,
	}

	w1 := New()
	if err := w1.Setup(ctx, workloads.ScenarioConfig{Name: "det1"}); err != nil {
		t.Fatalf("w1 Setup failed: %v", err)
	}
	obs1, err := w1.RunTrial(ctx, plan, 0)
	if err != nil {
		t.Fatalf("w1 RunTrial failed: %v", err)
	}

	w2 := New()
	if err := w2.Setup(ctx, workloads.ScenarioConfig{Name: "det2"}); err != nil {
		t.Fatalf("w2 Setup failed: %v", err)
	}
	obs2, err := w2.RunTrial(ctx, plan, 0)
	if err != nil {
		t.Fatalf("w2 RunTrial failed: %v", err)
	}

	if obs1.Operations != obs2.Operations {
		t.Errorf("operations mismatch: %d vs %d", obs1.Operations, obs2.Operations)
	}
	if obs1.Metrics["checksum"] != obs2.Metrics["checksum"] {
		t.Errorf("checksum mismatch: %d vs %d", obs1.Metrics["checksum"], obs2.Metrics["checksum"])
	}
}

func TestBaselineWorkload_DurationMode(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	w := New()
	if err := w.Setup(ctx, workloads.ScenarioConfig{Name: "dur_test"}); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	plan := workloads.ExecutionPlan{
		Mode:           workloads.ModeDuration,
		TargetDuration: testDurationRun,
		BatchSize:      testBatchSize,
	}

	obs, err := w.RunTrial(ctx, plan, 0)
	if err != nil {
		t.Fatalf("RunTrial failed: %v", err)
	}

	if obs.Operations <= 0 {
		t.Errorf("expected positive operations in duration mode, got %d", obs.Operations)
	}
	if len(obs.RawSamplesNs) == 0 {
		t.Error("expected collected batch samples")
	}
}

func TestBaselineWorkload_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	w := New()
	if err := w.Setup(ctx, workloads.ScenarioConfig{Name: "cancel_test"}); err == nil {
		t.Error("expected error on Setup with canceled context")
	}

	// Normal setup with active context
	activeCtx := context.Background()
	if err := w.Setup(activeCtx, workloads.ScenarioConfig{Name: "cancel_test"}); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	plan := workloads.ExecutionPlan{
		Mode:           workloads.ModeDuration,
		TargetDuration: 5 * time.Second,
		BatchSize:      testBatchSize,
	}

	// Warmup with canceled context
	if err := w.Warmup(ctx, plan); err == nil {
		t.Error("expected error on Warmup with canceled context")
	}

	// RunTrial with canceled context
	if _, err := w.RunTrial(ctx, plan, 0); err == nil {
		t.Error("expected error on RunTrial with canceled context")
	}

	// Teardown with canceled context
	if err := w.Teardown(ctx); err == nil {
		t.Error("expected error on Teardown with canceled context")
	}
}

func TestBaselineWorkload_InvalidPlans(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	w := New()
	if err := w.Setup(ctx, workloads.ScenarioConfig{Name: "invalid_plans"}); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Zero duration in duration mode
	durPlan := workloads.ExecutionPlan{
		Mode:           workloads.ModeDuration,
		TargetDuration: 0,
	}
	if _, err := w.RunTrial(ctx, durPlan, 0); err == nil {
		t.Error("expected error on zero duration")
	}

	// Zero iterations in iteration mode
	iterPlan := workloads.ExecutionPlan{
		Mode:             workloads.ModeIterations,
		TargetIterations: 0,
	}
	if _, err := w.RunTrial(ctx, iterPlan, 0); err == nil {
		t.Error("expected error on zero iterations")
	}

	// Zero batch size should normalize to defaultBatchSize
	zeroBatchPlan := workloads.ExecutionPlan{
		Mode:             workloads.ModeIterations,
		TargetIterations: 100,
		BatchSize:        0,
	}
	if obs, err := w.RunTrial(ctx, zeroBatchPlan, 0); err != nil || obs.Operations != 100 {
		t.Errorf("expected success with default batch size fallback, got obs=%v, err=%v", obs, err)
	}

	// Warmup with zero duration should be a no-op success
	if err := w.Warmup(ctx, durPlan); err != nil {
		t.Errorf("expected no-op success for zero warmup, got %v", err)
	}

	// Setup with invalid seed string falls back to default seed
	badSeedCfg := workloads.ScenarioConfig{
		Name: "bad_seed",
		Parameters: map[string]string{
			"seed": "not-a-number",
		},
	}
	if err := w.Setup(ctx, badSeedCfg); err != nil {
		t.Fatalf("setup with bad seed should succeed: %v", err)
	}
	if w.seed != defaultSeed {
		t.Errorf("expected default seed, got %x", w.seed)
	}

	// Mid-trial cancellation during multi-batch iteration
	cancelCtx, cancelFn := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancelFn()
	}()
	longPlan := workloads.ExecutionPlan{
		Mode:           workloads.ModeDuration,
		TargetDuration: 5 * time.Second,
		BatchSize:      10,
	}
	if _, err := w.RunTrial(cancelCtx, longPlan, 0); err == nil {
		t.Error("expected error on canceled context during execution")
	}
}

func TestStepN_Equivalence(t *testing.T) {
	t.Parallel()

	seed := uint64(0xCAFEBABEDEADBEEF)
	iterative := seed
	for range testStepCount {
		iterative = Step(iterative)
	}

	batched := StepN(seed, testStepCount)
	if iterative != batched {
		t.Errorf("StepN mismatch: iterative=%d batched=%d", iterative, batched)
	}
}

func TestNormalizePlan(t *testing.T) {
	t.Parallel()

	p1 := normalizePlan(workloads.ExecutionPlan{
		TargetIterations: 100,
	})
	if p1.Mode != workloads.ModeIterations || p1.BatchSize != defaultBatchSize {
		t.Errorf("unexpected normalized plan: %+v", p1)
	}

	p2 := normalizePlan(workloads.ExecutionPlan{
		TargetDuration: 10 * time.Millisecond,
	})
	if p2.Mode != workloads.ModeDuration || p2.BatchSize != defaultBatchSize {
		t.Errorf("unexpected normalized plan: %+v", p2)
	}
}

func TestBaselineWorkload_VaryingBatchSizeDeterminism(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	targetIterations := int64(3500)

	w1 := New()
	if err := w1.Setup(ctx, workloads.ScenarioConfig{Name: "batch_1000"}); err != nil {
		t.Fatalf("w1 Setup failed: %v", err)
	}
	obs1, err := w1.RunTrial(ctx, workloads.ExecutionPlan{
		Mode:             workloads.ModeIterations,
		TargetIterations: targetIterations,
		BatchSize:        1000,
	}, 0)
	if err != nil {
		t.Fatalf("w1 RunTrial failed: %v", err)
	}

	w2 := New()
	if err := w2.Setup(ctx, workloads.ScenarioConfig{Name: "batch_250"}); err != nil {
		t.Fatalf("w2 Setup failed: %v", err)
	}
	obs2, err := w2.RunTrial(ctx, workloads.ExecutionPlan{
		Mode:             workloads.ModeIterations,
		TargetIterations: targetIterations,
		BatchSize:        250,
	}, 0)
	if err != nil {
		t.Fatalf("w2 RunTrial failed: %v", err)
	}

	if obs1.Operations != targetIterations {
		t.Errorf("expected w1 operations %d, got %d", targetIterations, obs1.Operations)
	}
	if obs2.Operations != targetIterations {
		t.Errorf("expected w2 operations %d, got %d", targetIterations, obs2.Operations)
	}
	if obs1.Metrics["checksum"] != obs2.Metrics["checksum"] {
		t.Errorf("checksum mismatch across batch sizes: %d vs %d", obs1.Metrics["checksum"], obs2.Metrics["checksum"])
	}
}

