package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/analysis"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/env"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/workloads"
)

const (
	nanosecondsPerMillisecond    = 1e6
	initialSamplesPerObservation = 64
	defaultDirPerm               = 0o750
	evidenceFilePerm             = 0o600
)

// Runner orchestrates the end-to-end benchmark trial lifecycle.
type Runner struct {
	envDetector func() (*schema.EnvironmentSnapshot, error)
}

// New creates a new benchmark Runner using the default environment detector.
func New() *Runner {
	return &Runner{
		envDetector: env.Detect,
	}
}

// NewWithDetector creates a benchmark Runner using a custom environment detector function.
func NewWithDetector(detector func() (*schema.EnvironmentSnapshot, error)) *Runner {
	return &Runner{
		envDetector: detector,
	}
}

// Run executes the complete benchmark experiment according to the provided configuration.
func (r *Runner) Run(ctx context.Context, cfg Config) (*schema.Evidence, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid runner configuration: %w", err)
	}

	if cfg.ScenarioName == "" {
		cfg.ScenarioName = cfg.Workload.Name()
	}
	if cfg.LogWriter == nil {
		cfg.LogWriter = os.Stderr
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 1. Capture environment snapshot
	envSnapshot, err := r.envDetector()
	if err != nil {
		return nil, fmt.Errorf("capturing environment snapshot: %w", err)
	}

	// 2. Initialize workload
	scenarioConfig := workloads.ScenarioConfig{
		Name:       cfg.ScenarioName,
		Parameters: cfg.ScenarioParameters,
	}
	if err := cfg.Workload.Setup(ctx, scenarioConfig); err != nil {
		return nil, fmt.Errorf("workload setup failed: %w", err)
	}
	defer func() {
		// Guarantee cleanup via Teardown
		_ = cfg.Workload.Teardown(context.Background())
	}()

	// 3. Cache and runtime warmup
	if cfg.WarmupDuration > 0 {
		_, _ = fmt.Fprintf(cfg.LogWriter, "[microfat] Warming up for %v...\n", cfg.WarmupDuration)
		warmupPlan := workloads.ExecutionPlan{
			Mode:           workloads.ModeDuration,
			TargetDuration: cfg.WarmupDuration,
			BatchSize:      cfg.BatchSize,
		}
		if err := cfg.Workload.Warmup(ctx, warmupPlan); err != nil {
			return nil, fmt.Errorf("workload warmup failed: %w", err)
		}
	}

	// 4. Execute trials
	observations := make([]schema.TrialObservations, 0, cfg.Trials)
	trialPlan := workloads.ExecutionPlan{
		Mode:           workloads.ModeDuration,
		TargetDuration: cfg.TrialDuration,
		BatchSize:      cfg.BatchSize,
	}

	for i := range cfg.Trials {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		_, _ = fmt.Fprintf(cfg.LogWriter, "[microfat] Running trial %d/%d (%v)...\n", i+1, cfg.Trials, cfg.TrialDuration)
		trialObs, err := cfg.Workload.RunTrial(ctx, trialPlan, i)
		if err != nil {
			return nil, fmt.Errorf("trial %d failed: %w", i, err)
		}

		observations = append(observations, *trialObs)
		_, _ = fmt.Fprintf(cfg.LogWriter, "[microfat] Trial %d completed: %d operations (%.2fms)\n",
			i+1, trialObs.Operations, float64(trialObs.DurationNs)/nanosecondsPerMillisecond)
	}

	// 5. Statistical analysis
	allSamples := make([]int64, 0, len(observations)*initialSamplesPerObservation)
	var totalOps int64
	var totalDurationNs int64

	for j := range observations {
		obs := &observations[j]
		totalOps += obs.Operations
		totalDurationNs += obs.DurationNs
		allSamples = append(allSamples, obs.RawSamplesNs...)
	}

	if len(allSamples) == 0 {
		for j := range observations {
			allSamples = append(allSamples, observations[j].DurationNs)
		}
	}

	if totalDurationNs <= 0 {
		totalDurationNs = 1
	}

	statAnalysis, err := analysis.ComputeAnalysis(allSamples, totalOps, totalDurationNs)
	if err != nil {
		return nil, fmt.Errorf("computing statistical analysis: %w", err)
	}

	// 6. Assemble Experiment
	expID := cfg.ID
	if expID == "" {
		expID = fmt.Sprintf("exp-%d", time.Now().UnixNano())
	}

	createdAt := cfg.CreatedAt
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	exp := &schema.Experiment{
		SchemaVersion: schema.SchemaVersion,
		ID:            expID,
		Title:         cfg.Title,
		Description:   cfg.Description,
		CreatedAt:     createdAt,
		Environment:   *envSnapshot,
		Scenarios: []schema.ScenarioResult{
			{
				Name:         cfg.ScenarioName,
				Workload:     cfg.Workload.Name(),
				Observations: observations,
				Analysis:     *statAnalysis,
			},
		},
	}

	// 7. Build Evidence
	evidence, err := schema.BuildEvidence(exp)
	if err != nil {
		return nil, fmt.Errorf("building evidence: %w", err)
	}

	// 8. Write evidence file atomically if requested
	if cfg.OutputPath != "" {
		if err := writeEvidenceAtomically(evidence, cfg.OutputPath); err != nil {
			return nil, fmt.Errorf("saving evidence to %q: %w", cfg.OutputPath, err)
		}
		_, _ = fmt.Fprintf(cfg.LogWriter, "[microfat] Evidence saved to %s (SHA-256: %s)\n",
			cfg.OutputPath, evidence.DigestSHA256)
	}

	return evidence, nil
}

func writeEvidenceAtomically(evidence *schema.Evidence, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, defaultDirPerm); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	data, err := schema.SerializeEvidence(evidence)
	if err != nil {
		return fmt.Errorf("serializing evidence: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, "evidence-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temporary evidence file: %w", err)
	}
	tmpName := tmpFile.Name()
	_ = tmpFile.Close()

	if err := os.WriteFile(tmpName, data, evidenceFilePerm); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("writing temporary evidence file: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("moving temporary evidence file to destination: %w", err)
	}

	return nil
}
