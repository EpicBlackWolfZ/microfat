// Package baseline implements a deterministic fixed-operation compute kernel
// with observable checksum verification for benchmark framework validation.
package baseline

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/workloads"
)

const (
	// WorkloadName identifies the baseline compute workload.
	WorkloadName = "baseline"

	// WorkloadDescription describes the baseline compute workload.
	WorkloadDescription = "Deterministic fixed-operation integer compute kernel with observable checksum"

	defaultBatchSize = 50000
	defaultSeed      = uint64(0x123456789ABCDEF0)

	initialSampleCapacity = 128

	// Knuth 64-bit LCG multiplier and addend for integer mix kernel.
	lcgMultiplier = uint64(6364136223846793005)
	lcgAddend     = uint64(1442695040888963407)
	shiftAmount   = 27
)

// Sentinel baseline errors.
var (
	ErrNotSetup             = errors.New("workload has not been initialized via Setup")
	ErrInvalidExecutionPlan = errors.New("invalid execution plan: target duration or iterations must be positive")
	ErrTrialAborted         = errors.New("trial execution aborted")
)

// Global sink to prevent dead-code elimination of compute kernel output across compiler optimizations.
var OutputSink uint64

// Workload implements workloads.Workload for the deterministic baseline kernel.
type Workload struct {
	seed         uint64
	configured   bool
	scenarioName string
}

// New creates an uninitialized baseline Workload instance.
func New() *Workload {
	return &Workload{
		seed: defaultSeed,
	}
}

// Name returns the canonical workload identifier.
func (w *Workload) Name() string {
	return WorkloadName
}

// Description returns a brief summary of the workload purpose.
func (w *Workload) Description() string {
	return WorkloadDescription
}

// Setup initializes workload configuration parameters.
func (w *Workload) Setup(ctx context.Context, cfg workloads.ScenarioConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	w.scenarioName = cfg.Name
	w.seed = defaultSeed

	if sStr, ok := cfg.Parameters["seed"]; ok {
		if parsedSeed, err := strconv.ParseUint(sStr, 10, 64); err == nil {
			w.seed = parsedSeed
		}
	}

	w.configured = true
	return nil
}

// Warmup runs a pre-trial execution pass to prime CPU caches and Go runtime scheduler.
func (w *Workload) Warmup(ctx context.Context, plan workloads.ExecutionPlan) error {
	if !w.configured {
		return ErrNotSetup
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	effectivePlan := normalizePlan(plan)
	if effectivePlan.TargetDuration <= 0 && effectivePlan.TargetIterations <= 0 {
		return nil
	}

	_, err := w.executeKernel(ctx, effectivePlan)
	return err
}

// RunTrial executes a timed benchmark trial, collecting raw sample latencies and operation totals.
func (w *Workload) RunTrial(ctx context.Context, plan workloads.ExecutionPlan, trialIndex int) (*schema.TrialObservations, error) {
	if !w.configured {
		return nil, ErrNotSetup
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	effectivePlan := normalizePlan(plan)
	if effectivePlan.Mode == workloads.ModeDuration && effectivePlan.TargetDuration <= 0 {
		return nil, fmt.Errorf("%w: target duration must be > 0", ErrInvalidExecutionPlan)
	}
	if effectivePlan.Mode == workloads.ModeIterations && effectivePlan.TargetIterations <= 0 {
		return nil, fmt.Errorf("%w: target iterations must be > 0", ErrInvalidExecutionPlan)
	}

	startTime := time.Now().UTC()
	kernelResult, err := w.executeKernel(ctx, effectivePlan)
	endTime := time.Now().UTC()

	if err != nil {
		return nil, err
	}

	durationNs := endTime.Sub(startTime).Nanoseconds()
	if durationNs <= 0 {
		durationNs = 1
	}

	return &schema.TrialObservations{
		TrialIndex:   trialIndex,
		StartTime:    startTime.Format(time.RFC3339Nano),
		EndTime:      endTime.Format(time.RFC3339Nano),
		DurationNs:   durationNs,
		Operations:   kernelResult.totalOps,
		RawSamplesNs: kernelResult.samplesNs,
		Metrics: map[string]int64{
			// #nosec G115 -- raw bitwise representation of deterministic checksum as int64 metric
			"checksum": int64(kernelResult.checksum),
			"batches":  int64(len(kernelResult.samplesNs)),
		},
	}, nil
}

// Teardown resets workload state and releases resources.
func (w *Workload) Teardown(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.configured = false
	return nil
}

type kernelResult struct {
	checksum  uint64
	totalOps  int64
	samplesNs []int64
}

func (w *Workload) executeKernel(ctx context.Context, plan workloads.ExecutionPlan) (*kernelResult, error) {
	batchSize := plan.BatchSize
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}

	state := w.seed
	var totalOps int64
	samples := make([]int64, 0, initialSampleCapacity)

	trialStart := time.Now()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		currentBatch := batchSize
		if plan.Mode == workloads.ModeIterations {
			remaining := plan.TargetIterations - totalOps
			if remaining <= 0 {
				break
			}
			if int64(currentBatch) > remaining {
				currentBatch = int(remaining)
			}
		}

		batchStart := time.Now()
		state = StepN(state, currentBatch)
		batchElapsed := time.Since(batchStart).Nanoseconds()
		if batchElapsed < 0 {
			batchElapsed = 0
		}

		samples = append(samples, batchElapsed)
		totalOps += int64(currentBatch)

		if plan.Mode == workloads.ModeIterations {
			if totalOps >= plan.TargetIterations {
				break
			}
		} else {
			if time.Since(trialStart) >= plan.TargetDuration {
				break
			}
		}
	}

	atomic.StoreUint64(&OutputSink, state)

	return &kernelResult{
		checksum:  state,
		totalOps:  totalOps,
		samplesNs: samples,
	}, nil
}

// Step computes a single deterministic arithmetic update on state.
func Step(state uint64) uint64 {
	next := state*lcgMultiplier + lcgAddend
	return next ^ (next >> shiftAmount)
}

// StepN runs N consecutive deterministic arithmetic updates starting from state.
func StepN(state uint64, n int) uint64 {
	cur := state
	for range n {
		cur = cur*lcgMultiplier + lcgAddend
		cur ^= (cur >> shiftAmount)
	}
	return cur
}

func normalizePlan(plan workloads.ExecutionPlan) workloads.ExecutionPlan {
	res := plan
	if res.BatchSize <= 0 {
		res.BatchSize = defaultBatchSize
	}
	if res.Mode == "" {
		if res.TargetIterations > 0 {
			res.Mode = workloads.ModeIterations
		} else {
			res.Mode = workloads.ModeDuration
		}
	}
	return res
}
