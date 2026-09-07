// Package workloads defines the common lifecycle interface and configuration models
// for microfat benchmark workloads.
package workloads

import (
	"context"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

// Execution modes.
const (
	ModeDuration   = "duration"
	ModeIterations = "iterations"
)

// ExecutionPlan specifies the execution parameters for a benchmark trial or warmup.
type ExecutionPlan struct {
	Mode             string        `json:"mode"`
	TargetDuration   time.Duration `json:"target_duration"`
	TargetIterations int64         `json:"target_iterations"`
	BatchSize        int           `json:"batch_size"`
}

// ScenarioConfig provides workload parameters and execution naming.
type ScenarioConfig struct {
	Name       string            `json:"name"`
	Parameters map[string]string `json:"parameters,omitempty"`
}

// Workload defines the lifecycle interface for benchmark workloads.
type Workload interface {
	Name() string
	Description() string
	Setup(ctx context.Context, cfg ScenarioConfig) error
	Warmup(ctx context.Context, plan ExecutionPlan) error
	RunTrial(ctx context.Context, plan ExecutionPlan, trialIndex int) (*schema.TrialObservations, error)
	Teardown(ctx context.Context) error
}
