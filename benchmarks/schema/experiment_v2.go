package schema

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"path"
	"strings"
	"time"
	"unicode"
)

const (
	VersionV2        = "v2"
	MaxTrials        = 10000
	MaxMeasurements  = 100000
	MaxEvidenceBytes = 64 * 1024 * 1024
	OutcomeOK        = "ok"
	OutcomeFailed    = "failed"
	OutcomeSkipped   = "skipped"
	OutcomeCancelled = "cancelled"
)

// Measurement keeps missing values distinct from zero and names the observation scope.
type Measurement struct {
	Value  *float64 `json:"value"`
	Unit   string   `json:"unit"`
	Phase  string   `json:"phase"`
	Source string   `json:"source"`
	Kind   string   `json:"kind"`
	Reason string   `json:"reason,omitempty"`
}

func Measured(value float64, unit, phase, source string) Measurement {
	return Measurement{Value: &value, Unit: unit, Phase: phase, Source: source, Kind: "measured"}
}

func Unavailable(unit, phase, source, reason string) Measurement {
	return Measurement{Unit: unit, Phase: phase, Source: source, Kind: "measured", Reason: reason}
}

type Artifact struct {
	ID           string            `json:"id"`
	Path         string            `json:"path"`
	SHA256       string            `json:"sha256"`
	Bytes        int64             `json:"bytes"`
	SourceSHA    string            `json:"source_sha"`
	GoVersion    string            `json:"go_version"`
	Settings     map[string]string `json:"settings"`
	ModuleDigest string            `json:"module_digest"`
}

type Configuration struct {
	ID          string            `json:"id"`
	ArtifactID  string            `json:"artifact_id"`
	Level       string            `json:"level"`
	Mode        string            `json:"mode"`
	Cache       string            `json:"cache"`
	Tuning      string            `json:"tuning"`
	Environment map[string]string `json:"environment"`
}

type ScheduledTrial struct {
	ID              string `json:"id"`
	Block           int    `json:"block"`
	Position        int    `json:"position"`
	ConfigurationID string `json:"configuration_id"`
}

type Control struct {
	Name      string `json:"name"`
	Requested string `json:"requested"`
	Effective string `json:"effective"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
}

type HistogramBucket struct {
	Start float64 `json:"start_seconds"`
	End   float64 `json:"end_seconds"`
	Count int64   `json:"count"`
}

type HTTPResult struct {
	Requests            int64              `json:"requests"`
	Successful          int64              `json:"successful"`
	StatusCodes         map[string]int64   `json:"status_codes"`
	DurationSeconds     float64            `json:"duration_seconds"`
	TargetQPS           float64            `json:"target_qps"`
	ActualQPS           float64            `json:"actual_qps"`
	PercentilesSeconds  map[string]float64 `json:"percentiles_seconds"`
	Buckets             []HistogramBucket  `json:"buckets"`
	HistogramType       string             `json:"histogram_type"`
	ResolutionSeconds   float64            `json:"resolution_seconds"`
	CoordinatedOmission string             `json:"coordinated_omission"`
	RawPath             string             `json:"raw_path"`
	Warnings            []string           `json:"warnings"`
}

type ResourceSample struct {
	ElapsedNS int64                  `json:"elapsed_ns"`
	Phase     string                 `json:"phase"`
	Metrics   map[string]Measurement `json:"metrics"`
}

type ProcessIdentity struct {
	PID        int               `json:"pid"`
	Executable string            `json:"executable"`
	Arguments  []string          `json:"arguments"`
	Effective  map[string]string `json:"effective"`
}

type ProcessTrial struct {
	ScheduledTrial
	Outcome          string                 `json:"outcome"`
	Reason           string                 `json:"reason,omitempty"`
	StartedAt        string                 `json:"started_at"`
	DurationNS       int64                  `json:"duration_ns"`
	Target           ProcessIdentity        `json:"target"`
	Generator        ProcessIdentity        `json:"generator"`
	Observer         ProcessIdentity        `json:"observer"`
	Controls         []Control              `json:"controls"`
	Metrics          map[string]Measurement `json:"metrics"`
	Samples          []ResourceSample       `json:"samples"`
	TelemetryPath    string                 `json:"telemetry_path,omitempty"`
	SampleCount      int                    `json:"sample_count"`
	SampleIntervalMS int                    `json:"sample_interval_ms"`
	Load             *HTTPResult            `json:"load,omitempty"`
	Warnings         []string               `json:"warnings"`
}

type Pair struct {
	Block          int     `json:"block"`
	BaselineTrial  string  `json:"baseline_trial"`
	CandidateTrial string  `json:"candidate_trial"`
	Baseline       float64 `json:"baseline"`
	Candidate      float64 `json:"candidate"`
}

type Comparison struct {
	Baseline         string   `json:"baseline"`
	Candidate        string   `json:"candidate"`
	Metric           string   `json:"metric"`
	Unit             string   `json:"unit"`
	Direction        string   `json:"direction"`
	Pairs            []Pair   `json:"pairs"`
	Status           string   `json:"status"`
	Reason           string   `json:"reason,omitempty"`
	MedianDifference float64  `json:"median_difference"`
	RelativePercent  *float64 `json:"median_paired_percent,omitempty"`
	Low              float64  `json:"ci_low"`
	High             float64  `json:"ci_high"`
	Significant      bool     `json:"significant"`
	Seed             uint64   `json:"seed"`
	Resamples        int      `json:"resamples"`
	Algorithm        string   `json:"algorithm"`
}

// ExperimentV2 does not reuse v1 raw-sample semantics for concurrent HTTP histograms.
type ExperimentV2 struct {
	SchemaVersion   string              `json:"schema_version"`
	ID              string              `json:"id"`
	CreatedAt       string              `json:"created_at"`
	SourceSHA       string              `json:"source_sha"`
	Dirty           bool                `json:"dirty"`
	ConfigSHA256    string              `json:"config_sha256"`
	Config          jsontext.Value      `json:"config"`
	Environment     EnvironmentSnapshot `json:"environment"`
	HostTelemetry   map[string]string   `json:"host_telemetry"`
	Artifacts       []Artifact          `json:"artifacts"`
	Configurations  []Configuration     `json:"configurations"`
	Schedule        []ScheduledTrial    `json:"schedule"`
	Trials          []ProcessTrial      `json:"trials"`
	Comparisons     []Comparison        `json:"comparisons"`
	Seed            uint64              `json:"seed"`
	Complete        bool                `json:"complete"`
	ReleaseEligible bool                `json:"release_eligible"`
	Warnings        []string            `json:"warnings"`
	Runner          *RunnerInfo         `json:"runner,omitempty"`
}

func Digest(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func CanonicalV2(value any) ([]byte, error) {
	return json.Marshal(value, json.Deterministic(true), jsontext.WithIndent("  "))
}

func ConfigDigest(data []byte) (string, error) {
	var config any
	if err := json.Unmarshal(data, &config); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(config, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	return Digest(canonical), nil
}

func ValidateMeasurement(m Measurement) error {
	if m.Unit == "" || m.Phase == "" || m.Source == "" {
		return errors.New("measurement requires unit, phase and source")
	}
	if m.Kind != "measured" && m.Kind != "derived" && m.Kind != "model_assumption" {
		return errors.New("invalid measurement provenance")
	}
	if m.Value == nil {
		if m.Reason == "" {
			return errors.New("unavailable measurement requires a reason")
		}
	} else if !Finite(*m.Value) || m.Reason != "" {
		return errors.New("measurement must be finite and cannot have an unavailable reason")
	}
	return nil
}

func Finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func ValidDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func SafeReference(value string) bool {
	return value != "" && value != "." && !strings.Contains(value, "\\") &&
		strings.IndexFunc(value, unicode.IsControl) < 0 && !path.IsAbs(value) && path.Clean(value) == value &&
		value != ".." && !strings.HasPrefix(value, "../")
}

func ValidateV2(exp *ExperimentV2) error {
	if exp == nil || exp.SchemaVersion != VersionV2 {
		return errors.New("expected benchmark schema v2")
	}
	if !SafeReference(exp.ID) || strings.Contains(exp.ID, "/") {
		return errors.New("invalid experiment id")
	}
	if _, err := time.Parse(time.RFC3339Nano, exp.CreatedAt); err != nil {
		return fmt.Errorf("created_at: %w", err)
	}
	configDigest, err := ConfigDigest(exp.Config)
	if err != nil || !ValidDigest(exp.ConfigSHA256) || configDigest != exp.ConfigSHA256 {
		return errors.New("configuration digest mismatch")
	}
	if len(exp.Configurations) == 0 || len(exp.Schedule) == 0 || len(exp.Schedule) > MaxTrials {
		return errors.New("invalid configuration or schedule count")
	}
	schedule, err := validateReferences(exp)
	if err != nil {
		return err
	}
	if err := validateProcessTrials(exp.Trials, schedule); err != nil {
		return err
	}
	if exp.Complete && len(exp.Trials) != len(exp.Schedule) {
		return errors.New("complete experiment has missing trials")
	}
	if exp.ReleaseEligible && (!exp.Complete || exp.Dirty) {
		return errors.New("release eligibility contradicts experiment state")
	}
	configs := make(map[string]bool)
	for _, cfg := range exp.Configurations {
		configs[cfg.ID] = true
	}
	return validateComparisons(exp.Comparisons, exp.Trials, configs)
}

func validateProcessTrials(trials []ProcessTrial, schedule map[string]ScheduledTrial) error {
	seen := make(map[string]bool)
	for _, trial := range trials {
		if expected, ok := schedule[trial.ID]; !ok || expected != trial.ScheduledTrial || seen[trial.ID] {
			return errors.New("trial does not match schedule or is duplicated")
		}
		seen[trial.ID] = true
		if trial.Outcome != OutcomeOK && trial.Outcome != OutcomeFailed &&
			trial.Outcome != OutcomeSkipped && trial.Outcome != OutcomeCancelled {
			return errors.New("invalid trial outcome")
		}
		if trial.Outcome != OutcomeOK && trial.Reason == "" {
			return errors.New("unsuccessful trial requires a reason")
		}
		if trial.DurationNS < 0 || len(trial.Samples) > MaxMeasurements {
			return errors.New("invalid trial bounds")
		}
		if trial.SampleCount < 0 || trial.SampleCount > MaxMeasurements ||
			trial.TelemetryPath != "" && (!SafeReference(trial.TelemetryPath) || trial.SampleIntervalMS <= 0) {
			return errors.New("invalid telemetry reference or bounds")
		}
		if _, err := time.Parse(time.RFC3339Nano, trial.StartedAt); err != nil {
			return err
		}
		for _, metric := range trial.Metrics {
			if err := ValidateMeasurement(metric); err != nil {
				return err
			}
		}
		if err := ValidateResourceSamples(trial.Samples); err != nil {
			return err
		}
		if trial.Load != nil {
			if err := ValidateHTTP(trial.Load); err != nil {
				return err
			}
		}
	}
	return nil
}

func ValidateHTTP(result *HTTPResult) error {
	if err := validateHTTPScalars(result); err != nil {
		return err
	}
	var counts int64
	for _, count := range result.StatusCodes {
		if count < 0 || count > result.Requests-counts {
			return errors.New("invalid HTTP status counts")
		}
		counts += count
	}
	if counts != result.Requests {
		return errors.New("HTTP status count mismatch")
	}
	var bucketCounts int64
	var previous float64
	for _, bucket := range result.Buckets {
		if !Finite(bucket.Start) || !Finite(bucket.End) || bucket.Start < previous || bucket.End < bucket.Start ||
			bucket.Count < 0 || bucket.Count > result.Requests-bucketCounts {
			return errors.New("invalid histogram bucket")
		}
		previous = bucket.End
		bucketCounts += bucket.Count
	}
	if bucketCounts != result.Requests {
		return errors.New("histogram count mismatch")
	}
	for _, value := range result.PercentilesSeconds {
		if !Finite(value) || value < 0 {
			return errors.New("invalid HTTP percentile")
		}
	}
	return nil
}

func validateComparisons(comparisons []Comparison, trials []ProcessTrial, configs map[string]bool) error {
	byID := make(map[string]ProcessTrial)
	for _, trial := range trials {
		byID[trial.ID] = trial
	}
	for _, c := range comparisons {
		if !configs[c.Baseline] || !configs[c.Candidate] || c.Baseline == c.Candidate || c.Metric == "" || c.Unit == "" ||
			!Finite(c.MedianDifference) || !Finite(c.Low) || !Finite(c.High) || c.Low > c.High ||
			(c.RelativePercent != nil && !Finite(*c.RelativePercent)) {
			return errors.New("invalid comparison")
		}
		if c.Status != "complete" && c.Status != "descriptive" && c.Status != "inconclusive" ||
			c.Direction != "lower" && c.Direction != "higher" || c.Significant && c.Status != "complete" {
			return errors.New("invalid comparison status or direction")
		}
		if err := validatePairs(c, byID); err != nil {
			return err
		}
	}
	return nil
}

func validateReferences(exp *ExperimentV2) (map[string]ScheduledTrial, error) {
	artifacts := make(map[string]bool)
	for _, a := range exp.Artifacts {
		if a.ID == "" || artifacts[a.ID] || !ValidDigest(a.SHA256) || a.Bytes <= 0 {
			return nil, errors.New("invalid or duplicate artifact")
		}
		artifacts[a.ID] = true
	}
	configs := make(map[string]bool)
	for _, c := range exp.Configurations {
		if c.ID == "" || configs[c.ID] || !artifacts[c.ArtifactID] {
			return nil, errors.New("invalid configuration reference")
		}
		configs[c.ID] = true
	}
	schedule := make(map[string]ScheduledTrial)
	positions := make(map[int]bool)
	blocks := make(map[string]bool)
	for _, trial := range exp.Schedule {
		key := fmt.Sprintf("%d/%s", trial.Block, trial.ConfigurationID)
		if _, exists := schedule[trial.ID]; exists || !SafeReference(trial.ID) || strings.Contains(trial.ID, "/") ||
			trial.Block < 0 || trial.Position < 0 || positions[trial.Position] || blocks[key] || !configs[trial.ConfigurationID] {
			return nil, errors.New("invalid or duplicate scheduled trial")
		}
		schedule[trial.ID], positions[trial.Position], blocks[key] = trial, true, true
	}
	return schedule, nil
}

func validateHTTPScalars(result *HTTPResult) error {
	if result.Requests <= 0 || result.Successful < 0 || result.Successful > result.Requests ||
		!Finite(result.DurationSeconds) || result.DurationSeconds <= 0 ||
		!Finite(result.ActualQPS) || result.ActualQPS < 0 || !Finite(result.TargetQPS) || result.TargetQPS < 0 ||
		!Finite(result.ResolutionSeconds) || result.ResolutionSeconds <= 0 || !SafeReference(result.RawPath) {
		return errors.New("invalid HTTP measurements")
	}
	return nil
}

func validatePairs(c Comparison, byID map[string]ProcessTrial) error {
	seen := make(map[int]bool)
	for _, pair := range c.Pairs {
		a, aOK := byID[pair.BaselineTrial]
		b, bOK := byID[pair.CandidateTrial]
		if !aOK || !bOK || a.ConfigurationID != c.Baseline || b.ConfigurationID != c.Candidate ||
			a.Block != pair.Block || b.Block != pair.Block || !Finite(pair.Baseline) || !Finite(pair.Candidate) || seen[pair.Block] {
			return errors.New("invalid comparison pair")
		}
		seen[pair.Block] = true
		av, bv := a.Metrics[c.Metric], b.Metrics[c.Metric]
		if c.Metric != "trial_success" && (a.Outcome != OutcomeOK || b.Outcome != OutcomeOK) || av.Value == nil || bv.Value == nil ||
			*av.Value != pair.Baseline || *bv.Value != pair.Candidate || av.Unit != c.Unit || bv.Unit != c.Unit ||
			av.Phase != bv.Phase || av.Source != bv.Source {
			return errors.New("comparison values differ from referenced observations")
		}
	}
	return nil
}

func ValidateResourceSamples(samples []ResourceSample) error {
	if len(samples) > MaxMeasurements {
		return errors.New("too many resource samples")
	}
	for _, sample := range samples {
		if sample.ElapsedNS < 0 || sample.Phase == "" {
			return errors.New("invalid resource sample")
		}
		for _, metric := range sample.Metrics {
			if err := ValidateMeasurement(metric); err != nil {
				return err
			}
		}
	}
	return nil
}
