package schema

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Sentinel validation errors.
var (
	ErrNilExperiment        = errors.New("experiment cannot be nil")
	ErrInvalidSchemaVersion = errors.New("invalid schema version")
	ErrEmptyExperimentID    = errors.New("experiment id cannot be empty")
	ErrInvalidTimestamp     = errors.New("timestamp must be valid RFC3339 format")
	ErrEmptyScenarios       = errors.New("experiment must contain at least one scenario")
	ErrInvalidScenarioName  = errors.New("scenario name and workload cannot be empty")
	ErrEmptyObservations    = errors.New("scenario must contain at least one trial observation")
	ErrInvalidDuration      = errors.New("duration must be positive")
	ErrNegativeOperations   = errors.New("operations count cannot be negative")
	ErrNegativeSample       = errors.New("sample duration cannot be negative")
	ErrInvalidAnalysis      = errors.New("invalid scenario analysis")
	ErrInvalidLimitState    = errors.New("invalid resource limit state")
	ErrInvalidLimitValue    = errors.New("invalid resource limit value for state")
)

// ValidateResourceLimit validates the tri-state invariant for any ResourceLimit instance.
func ValidateResourceLimit[T any](r ResourceLimit[T]) error {
	switch r.State {
	case LimitStateFinite:
		if r.Value == nil {
			return fmt.Errorf("%w: state is finite but value is nil", ErrInvalidLimitValue)
		}
	case LimitStateUnlimited:
		if r.Value != nil {
			return fmt.Errorf("%w: state is unlimited but value is non-nil", ErrInvalidLimitValue)
		}
	case LimitStateUnavailable:
		if r.Value != nil {
			return fmt.Errorf("%w: state is unavailable but value is non-nil", ErrInvalidLimitValue)
		}
	default:
		return fmt.Errorf("%w: unknown state %q", ErrInvalidLimitState, r.State)
	}
	return nil
}

// ValidateExperiment verifies structural invariants and tri-state resource limit rules.
func ValidateExperiment(exp *Experiment) error {
	if exp == nil {
		return ErrNilExperiment
	}
	if exp.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: expected %q, got %q", ErrInvalidSchemaVersion, SchemaVersion, exp.SchemaVersion)
	}
	if strings.TrimSpace(exp.ID) == "" {
		return ErrEmptyExperimentID
	}
	if _, err := time.Parse(time.RFC3339Nano, exp.CreatedAt); err != nil {
		if _, errRFC := time.Parse(time.RFC3339, exp.CreatedAt); errRFC != nil {
			return fmt.Errorf("%w: created_at %q: %w", ErrInvalidTimestamp, exp.CreatedAt, err)
		}
	}

	if err := ValidateResourceLimit(exp.Environment.Host.Cgroup.MemoryMaxBytes); err != nil {
		return fmt.Errorf("cgroup memory limit validation: %w", err)
	}
	if exp.Environment.Host.Cgroup.MemoryMaxBytes.State == LimitStateFinite &&
		*exp.Environment.Host.Cgroup.MemoryMaxBytes.Value <= 0 {
		return fmt.Errorf("%w: finite cgroup memory max bytes must be positive", ErrInvalidLimitValue)
	}

	if err := ValidateResourceLimit(exp.Environment.Host.Cgroup.CPUQuotaUs); err != nil {
		return fmt.Errorf("cgroup cpu quota validation: %w", err)
	}
	if exp.Environment.Host.Cgroup.CPUQuotaUs.State == LimitStateFinite &&
		*exp.Environment.Host.Cgroup.CPUQuotaUs.Value <= 0 {
		return fmt.Errorf("%w: finite cgroup cpu quota must be positive", ErrInvalidLimitValue)
	}

	if exp.Environment.Host.Cgroup.CPUPeriodUs != nil && *exp.Environment.Host.Cgroup.CPUPeriodUs <= 0 {
		return fmt.Errorf("%w: cgroup cpu period must be positive (%d us)", ErrInvalidDuration, *exp.Environment.Host.Cgroup.CPUPeriodUs)
	}

	if len(exp.Scenarios) == 0 {
		return ErrEmptyScenarios
	}

	for i := range exp.Scenarios {
		if err := validateScenario(&exp.Scenarios[i]); err != nil {
			return fmt.Errorf("scenario[%d] %q: %w", i, exp.Scenarios[i].Name, err)
		}
	}

	return nil
}

func validateScenario(sc *ScenarioResult) error {
	if strings.TrimSpace(sc.Name) == "" || strings.TrimSpace(sc.Workload) == "" {
		return ErrInvalidScenarioName
	}
	if len(sc.Observations) == 0 {
		return ErrEmptyObservations
	}

	totalSamples := 0
	hasRawSamples := false
	seenTrials := make(map[int]struct{}, len(sc.Observations))

	for j := range sc.Observations {
		obs := &sc.Observations[j]
		if obs.TrialIndex < 0 {
			return fmt.Errorf("trial index cannot be negative: %d", obs.TrialIndex)
		}
		if _, seen := seenTrials[obs.TrialIndex]; seen {
			return fmt.Errorf("duplicate trial index %d", obs.TrialIndex)
		}
		seenTrials[obs.TrialIndex] = struct{}{}

		if obs.DurationNs <= 0 {
			return fmt.Errorf("trial %d: %w (%d ns)", obs.TrialIndex, ErrInvalidDuration, obs.DurationNs)
		}
		if obs.Operations < 0 {
			return fmt.Errorf("trial %d: %w (%d ops)", obs.TrialIndex, ErrNegativeOperations, obs.Operations)
		}

		var tStart, tEnd time.Time
		var hasStart, hasEnd bool
		if obs.StartTime != "" {
			var err error
			tStart, err = time.Parse(time.RFC3339Nano, obs.StartTime)
			if err != nil {
				tStart, err = time.Parse(time.RFC3339, obs.StartTime)
			}
			if err != nil {
				return fmt.Errorf("trial %d: start_time %w: %w", obs.TrialIndex, ErrInvalidTimestamp, err)
			}
			hasStart = true
		}
		if obs.EndTime != "" {
			var err error
			tEnd, err = time.Parse(time.RFC3339Nano, obs.EndTime)
			if err != nil {
				tEnd, err = time.Parse(time.RFC3339, obs.EndTime)
			}
			if err != nil {
				return fmt.Errorf("trial %d: end_time %w: %w", obs.TrialIndex, ErrInvalidTimestamp, err)
			}
			hasEnd = true
		}
		if hasStart && hasEnd && tEnd.Before(tStart) {
			return fmt.Errorf("trial %d: end_time (%s) cannot be before start_time (%s)",
				obs.TrialIndex, obs.EndTime, obs.StartTime)
		}

		if len(obs.RawSamplesNs) > 0 {
			hasRawSamples = true
			totalSamples += len(obs.RawSamplesNs)
			for _, s := range obs.RawSamplesNs {
				if s < 0 {
					return fmt.Errorf("trial %d: %w (%d ns)", obs.TrialIndex, ErrNegativeSample, s)
				}
			}
		}
	}

	if err := validateAnalysis(&sc.Analysis, hasRawSamples, totalSamples); err != nil {
		return err
	}

	return nil
}

func validateAnalysis(a *ScenarioAnalysis, hasRawSamples bool, totalSamples int) error {
	if a.AlgorithmVersion != AlgorithmVersionV1 {
		return fmt.Errorf("%w: unexpected algorithm_version %q", ErrInvalidAnalysis, a.AlgorithmVersion)
	}
	if a.PercentileMethod != PercentileMethodLinearR7 {
		return fmt.Errorf("%w: unexpected percentile_method %q", ErrInvalidAnalysis, a.PercentileMethod)
	}
	if a.SampleCount <= 0 {
		return fmt.Errorf("%w: sample_count must be > 0", ErrInvalidAnalysis)
	}
	if hasRawSamples && a.SampleCount != totalSamples {
		return fmt.Errorf("%w: sample_count (%d) does not match total raw samples (%d)", ErrInvalidAnalysis, a.SampleCount, totalSamples)
	}
	if a.MinNs < 0 || a.MaxNs < a.MinNs {
		return fmt.Errorf("%w: invalid min_ns (%d) or max_ns (%d)", ErrInvalidAnalysis, a.MinNs, a.MaxNs)
	}
	if math.IsNaN(a.MeanNs) || math.IsInf(a.MeanNs, 0) ||
		math.IsNaN(a.StdDevNs) || math.IsInf(a.StdDevNs, 0) ||
		math.IsNaN(a.ThroughputOpsPerSec) || math.IsInf(a.ThroughputOpsPerSec, 0) {
		return fmt.Errorf("%w: mean, stddev, or throughput contains NaN or Inf", ErrInvalidAnalysis)
	}
	if a.MeanNs < 0 || a.StdDevNs < 0 || a.ThroughputOpsPerSec < 0 {
		return fmt.Errorf("%w: negative mean, stddev, or throughput", ErrInvalidAnalysis)
	}
	if len(a.PercentilesNs) == 0 {
		return fmt.Errorf("%w: percentiles_ns cannot be empty", ErrInvalidAnalysis)
	}
	for k, pVal := range a.PercentilesNs {
		if math.IsNaN(pVal) || math.IsInf(pVal, 0) {
			return fmt.Errorf("%w: percentile %s is NaN or Inf", ErrInvalidAnalysis, k)
		}
		if pVal < float64(a.MinNs)-1.0 || pVal > float64(a.MaxNs)+1.0 {
			return fmt.Errorf("%w: percentile %s (%.2f) outside [min_ns, max_ns] range [%d, %d]",
				ErrInvalidAnalysis, k, pVal, a.MinNs, a.MaxNs)
		}
	}
	return nil
}
