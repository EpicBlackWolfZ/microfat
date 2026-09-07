// Package schema defines the authoritative data models, tri-state resource limits,
// invariant validation rules, and canonical serialization format for microfat benchmarks.
package schema

// SchemaVersion defines the authoritative schema format version.
const SchemaVersion = "v1"

// Constants for statistical analysis metadata.
const (
	AlgorithmVersionV1       = "v1"
	PercentileMethodLinearR7 = "linear_interpolation_r7"
)

// LimitState represents the tri-state classification of a system resource limit.
type LimitState string

const (
	// LimitStateFinite indicates a specific numeric limit is applied (Value != nil).
	LimitStateFinite LimitState = "finite"

	// LimitStateUnlimited indicates an unconstrained or infinite resource limit (Value == nil).
	LimitStateUnlimited LimitState = "unlimited"

	// LimitStateUnavailable indicates the limit could not be probed or detected (Value == nil).
	LimitStateUnavailable LimitState = "unavailable"
)

// ResourceLimit models a system resource ceiling with explicit tri-state semantics.
type ResourceLimit[T any] struct {
	State LimitState `json:"state"`
	Value *T         `json:"value"`
}

// NewFiniteLimit creates a ResourceLimit in the finite state with the specified value.
func NewFiniteLimit[T any](val T) ResourceLimit[T] {
	v := val
	return ResourceLimit[T]{
		State: LimitStateFinite,
		Value: &v,
	}
}

// NewUnlimitedLimit creates a ResourceLimit in the unlimited state.
func NewUnlimitedLimit[T any]() ResourceLimit[T] {
	return ResourceLimit[T]{
		State: LimitStateUnlimited,
		Value: nil,
	}
}

// NewUnavailableLimit creates a ResourceLimit in the unavailable state.
func NewUnavailableLimit[T any]() ResourceLimit[T] {
	return ResourceLimit[T]{
		State: LimitStateUnavailable,
		Value: nil,
	}
}

// Experiment represents a fully self-contained benchmark experiment run.
type Experiment struct {
	SchemaVersion string              `json:"schema_version"`
	ID            string              `json:"id"`
	Title         string              `json:"title"`
	Description   string              `json:"description"`
	CreatedAt     string              `json:"created_at"`
	Environment   EnvironmentSnapshot `json:"environment"`
	Scenarios     []ScenarioResult    `json:"scenarios"`
}

// ScenarioResult captures the raw observations and computed statistical analysis for a scenario.
type ScenarioResult struct {
	Name         string              `json:"name"`
	Workload     string              `json:"workload"`
	Observations []TrialObservations `json:"observations"`
	Analysis     ScenarioAnalysis    `json:"analysis"`
}

// TrialObservations records lossless raw measurements collected during a single trial run.
type TrialObservations struct {
	TrialIndex   int              `json:"trial_index"`
	StartTime    string           `json:"start_time"`
	EndTime      string           `json:"end_time"`
	DurationNs   int64            `json:"duration_ns"`
	Operations   int64            `json:"operations"`
	RawSamplesNs []int64          `json:"raw_samples_ns"`
	Metrics      map[string]int64 `json:"metrics"`
}

// ScenarioAnalysis contains statistical summaries derived as a pure function of raw observations.
type ScenarioAnalysis struct {
	AlgorithmVersion    string             `json:"algorithm_version"`
	PercentileMethod    string             `json:"percentile_method"`
	SampleCount         int                `json:"sample_count"`
	MinNs               int64              `json:"min_ns"`
	MaxNs               int64              `json:"max_ns"`
	MeanNs              float64            `json:"mean_ns"`
	StdDevNs            float64            `json:"std_dev_ns"`
	PercentilesNs       map[string]float64 `json:"percentiles_ns"`
	ThroughputOpsPerSec float64            `json:"throughput_ops_per_sec"`
}
