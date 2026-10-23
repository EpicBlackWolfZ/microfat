package research377

import (
	"errors"
	"math"
)

const (
	Method        = "fixed-sample-clopper-pearson-bonferroni-v1"
	Improvement   = "useful-improvement"
	Regression    = "material-regression"
	Equivalent    = "practical-equivalence"
	Inconclusive  = "inconclusive"
	MaximumInput  = 64 << 20
	minimumAlpha  = 1e-9
	armCount      = 2
	SchemaVersion = 1
)

// Plan must be fixed before observing the confirmation data. Alpha is the total
// error budget over FamilySize comparisons, including candidates not reported.
type Plan struct {
	Estimand         string  `json:"estimand"`
	IndependentUnit  string  `json:"independent_unit"`
	Cohort           string  `json:"cohort"`
	Confirmation     string  `json:"confirmation"`
	IID              bool    `json:"iid_within_each_arm"`
	FixedSample      bool    `json:"fixed_sample"`
	BaselineSamples  int     `json:"baseline_samples"`
	CandidateSamples int     `json:"candidate_samples"`
	SLONanoseconds   int64   `json:"slo_ns"`
	PracticalMargin  float64 `json:"practical_margin_probability"`
	Alpha            float64 `json:"family_alpha"`
	FamilySize       int     `json:"comparison_family_size"`
}

// Observation represents one independent unit, not one correlated request inside
// a unit. Failed and timed-out units always violate the SLO, regardless of time.
type Observation struct {
	UnitID   string `json:"unit_id"`
	Duration int64  `json:"duration_ns"`
	Failed   bool   `json:"failed"`
	TimedOut bool   `json:"timed_out"`
}

type Input struct {
	Provenance Provenance    `json:"provenance"`
	Plan       Plan          `json:"plan"`
	Baseline   []Observation `json:"baseline"`
	Candidate  []Observation `json:"candidate"`
}

// Provenance is supplied by the experiment owner and is retained verbatim.
// Recording a host, revision or IID declaration does not establish independence.
type Provenance struct {
	SourceRevision  string `json:"source_revision"`
	GoVersion       string `json:"go_version"`
	Host            string `json:"host"`
	MeasurementKind string `json:"measurement_kind"`
	DatasetSHA256   string `json:"dataset_sha256"`
}

type Arm struct {
	Samples    int      `json:"samples"`
	Violations int      `json:"slo_violations"`
	Failures   int      `json:"failures"`
	Timeouts   int      `json:"timeouts"`
	Risk       float64  `json:"observed_violation_probability"`
	Interval   Interval `json:"interval"`
}

type Report struct {
	SchemaVersion int        `json:"schema_version"`
	Method        string     `json:"method"`
	Provenance    Provenance `json:"provenance"`
	Plan          Plan       `json:"plan"`
	ArmAlpha      float64    `json:"per_arm_alpha"`
	Baseline      Arm        `json:"baseline"`
	Candidate     Arm        `json:"candidate"`
	Difference    float64    `json:"candidate_minus_baseline_probability"`
	Interval      Interval   `json:"difference_interval"`
	Decision      string     `json:"decision"`
}

// Analyze refuses to call ordinary repeated looks or dependent requests IID.
// Attestation records assumptions; it cannot verify independence from the data.
func Analyze(input Input) (Report, error) {
	plan := input.Plan
	provenance := input.Provenance
	if provenance.SourceRevision == "" || provenance.GoVersion == "" || provenance.Host == "" ||
		provenance.MeasurementKind == "" || provenance.DatasetSHA256 == "" {
		return Report{}, errors.New("declare source revision, Go version, host, measurement kind and dataset digest")
	}
	if err := validatePlan(plan); err != nil {
		return Report{}, err
	}
	alpha := plan.Alpha / float64(armCount*plan.FamilySize)
	baseline, err := summarize(input.Baseline, plan.BaselineSamples, plan.SLONanoseconds, alpha)
	if err != nil {
		return Report{}, err
	}
	candidate, err := summarize(input.Candidate, plan.CandidateSamples, plan.SLONanoseconds, alpha)
	if err != nil {
		return Report{}, err
	}
	interval := Interval{Low: candidate.Interval.Low - baseline.Interval.High, High: candidate.Interval.High - baseline.Interval.Low}
	return Report{
		SchemaVersion: SchemaVersion, Method: Method, Provenance: provenance, Plan: plan, ArmAlpha: alpha,
		Baseline: baseline, Candidate: candidate, Difference: candidate.Risk - baseline.Risk,
		Interval: interval, Decision: Decide(interval, plan.PracticalMargin),
	}, nil
}

func validatePlan(plan Plan) error {
	if plan.Estimand == "" || plan.IndependentUnit == "" || plan.Cohort == "" || plan.Confirmation == "" {
		return errors.New("declare the estimand, independent unit, cohort and confirmation split")
	}
	if !plan.IID || !plan.FixedSample {
		return errors.New("formal bounds require fixed-sample IID units within each arm; use descriptive analysis otherwise")
	}
	if plan.SLONanoseconds < 0 || math.IsNaN(plan.PracticalMargin) || plan.PracticalMargin <= 0 || plan.PracticalMargin >= 1 {
		return errors.New("invalid SLO or practical margin")
	}
	if !validAlpha(plan.Alpha) || plan.FamilySize < 1 || plan.FamilySize > MaximumFamily ||
		plan.Alpha/float64(armCount*plan.FamilySize) < minimumAlpha {
		return errors.New("invalid family error budget or comparison count")
	}
	return nil
}

func summarize(observations []Observation, planned int, slo int64, alpha float64) (Arm, error) {
	if planned < 1 || planned > MaximumSamples || len(observations) != planned {
		return Arm{}, errors.New("observations must match the predeclared fixed sample size within the cap")
	}
	arm := Arm{Samples: planned}
	seen := make(map[string]bool, planned)
	for _, observation := range observations {
		if observation.UnitID == "" || seen[observation.UnitID] || observation.Duration < 0 {
			return Arm{}, errors.New("unit IDs must be nonempty and unique per arm, with nonnegative observed durations")
		}
		seen[observation.UnitID] = true
		if observation.Failed {
			arm.Failures++
		}
		if observation.TimedOut {
			arm.Timeouts++
		}
		if observation.Failed || observation.TimedOut || observation.Duration > slo {
			arm.Violations++
		}
	}
	interval, err := BinomialInterval(arm.Violations, planned, alpha)
	if err != nil {
		return Arm{}, err
	}
	arm.Risk, arm.Interval = float64(arm.Violations)/float64(planned), interval
	return arm, nil
}

// Decide applies a predeclared absolute probability margin to candidate-baseline.
// Exhausting the sample budget and failing to reject are never equivalence.
func Decide(interval Interval, margin float64) string {
	switch {
	case interval.High < -margin:
		return Improvement
	case interval.Low > margin:
		return Regression
	case interval.Low >= -margin && interval.High <= margin:
		return Equivalent
	default:
		return Inconclusive
	}
}
