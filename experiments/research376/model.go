// Package main implements an isolated, descriptive lifecycle-cost research POC.
package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
)

const (
	schemaVersion   = 1
	maxCandidates   = 32
	maxScenarios    = 128
	maxSamples      = 100000
	maxBytes        = 1 << 40
	maxDurationNS   = int64(3600000000000)
	maxLaunches     = 1000000000
	sha256HexLength = 64
	maxModelWork    = 4000000
	nsPerSecond     = 1e9
	median          = 0.5
	p99             = 0.99
)

type provenance struct {
	SourceRevision  string `json:"source_revision"`
	GoVersion       string `json:"go_version"`
	CorpusSHA256    string `json:"corpus_sha256"`
	HostClass       string `json:"host_class"`
	MeasurementKind string `json:"measurement_kind"`
	Scope           string `json:"scope"`
	ColdState       string `json:"cold_state,omitempty"`
	WarmState       string `json:"warm_state,omitempty"`
}

type candidate struct {
	Name         string  `json:"name"`
	BundleBytes  int64   `json:"bundle_bytes"`
	PackNS       int64   `json:"pack_ns"`
	ColdNS       []int64 `json:"cold_ns"`
	WarmNS       []int64 `json:"warm_ns"`
	ColdFailures int     `json:"cold_failures"`
	WarmFailures int     `json:"warm_failures"`
}

type scenario struct {
	Name                    string  `json:"name"`
	WarmFraction            float64 `json:"warm_fraction"`
	Launches                int64   `json:"launches"`
	Transfers               int64   `json:"transfers"`
	BandwidthBytesPerSecond float64 `json:"bandwidth_bytes_per_second"`
	IncludePack             bool    `json:"include_pack"`
}

type input struct {
	SchemaVersion int         `json:"schema_version"`
	Provenance    provenance  `json:"provenance"`
	Candidates    []candidate `json:"candidates"`
	Scenarios     []scenario  `json:"scenarios"`
}

type cost struct {
	Candidate            string   `json:"candidate"`
	Status               string   `json:"status"`
	BundleBytes          int64    `json:"bundle_bytes"`
	Failures             int      `json:"failures"`
	MeanStartupNS        *float64 `json:"mean_startup_ns"`
	MixtureP50NS         *int64   `json:"mixture_p50_ns"`
	MixtureP99NS         *int64   `json:"mixture_p99_ns"`
	TransferNS           *float64 `json:"transfer_ns"`
	UpfrontNS            *float64 `json:"upfront_ns"`
	MeanLifecycleTotalNS *float64 `json:"mean_lifecycle_total_ns"`
}

type crossing struct {
	Baseline         string   `json:"baseline"`
	Candidate        string   `json:"candidate"`
	Direction        string   `json:"direction"`
	CrossingLaunches *float64 `json:"crossing_launches"`
}

type scenarioResult struct {
	Scenario  scenario   `json:"scenario"`
	Costs     []cost     `json:"costs"`
	BreakEven []crossing `json:"break_even"`
}

type report struct {
	SchemaVersion int              `json:"schema_version"`
	Provenance    provenance       `json:"provenance"`
	Evidence      string           `json:"evidence"`
	Warnings      []string         `json:"warnings"`
	Results       []scenarioResult `json:"results"`
}

func analyze(in input) (report, error) {
	if err := validate(in); err != nil {
		return report{}, err
	}
	out := report{SchemaVersion: schemaVersion, Provenance: in.Provenance,
		Evidence: "descriptive-empirical-scenario-model; no confidence or causal performance claim",
		Warnings: []string{
			"Warm fraction, bandwidth and launch count are scenario inputs, not inferred deployment facts.",
			"Mixture quantiles invert conditional empirical CDFs; quantiles are never averaged.",
			"Lifecycle totals model means, not tail quantiles; transfer and launches are assumed additive.",
			"Samples and failures remain distinct; any failed candidate has no complete-distribution prediction.",
			"Small or dependent samples do not qualify population p99; use issue 377 independent confirmation.",
			"No stage-time sum, resource Pareto claim, automatic policy or production change is inferred.",
			"CDF comparisons use exact rational arithmetic for the parsed binary64 probabilities, without epsilon.",
		}}
	for _, s := range in.Scenarios {
		result := scenarioResult{Scenario: s, Costs: make([]cost, 0, len(in.Candidates)), BreakEven: []crossing{}}
		for _, c := range in.Candidates {
			result.Costs = append(result.Costs, modelCost(c, s))
		}
		for _, c := range result.Costs[1:] {
			if base := result.Costs[0]; base.Status == "descriptive" && c.Status == "descriptive" {
				result.BreakEven = append(result.BreakEven, breakEven(base, c))
			}
		}
		out.Results = append(out.Results, result)
	}
	return out, nil
}

func validate(in input) error {
	if in.SchemaVersion != schemaVersion || len(in.Candidates) == 0 || len(in.Candidates) > maxCandidates ||
		len(in.Scenarios) == 0 || len(in.Scenarios) > maxScenarios {
		return errors.New("invalid schema version or candidate/scenario bounds")
	}
	if err := validateProvenance(in.Provenance); err != nil {
		return err
	}
	if err := validateCandidates(in.Candidates); err != nil {
		return err
	}
	observations := 0
	for _, c := range in.Candidates {
		observations += len(c.ColdNS) + len(c.WarmNS)
	}
	if observations*len(in.Scenarios) > maxModelWork {
		return errors.New("observation/scenario product exceeds research computation budget")
	}
	return validateScenarios(in.Scenarios)
}

func validateProvenance(p provenance) error {
	if p.SourceRevision == "" || p.GoVersion != "go1.27.3" || len(p.CorpusSHA256) != sha256HexLength || p.HostClass == "" {
		return errors.New("source revision, Go 1.27.3, corpus SHA-256 and host class are required")
	}
	if _, err := hex.DecodeString(p.CorpusSHA256); err != nil {
		return errors.New("corpus SHA-256 must be hexadecimal")
	}
	if !slices.Contains([]string{"synthetic", "local-native-exploratory", "imported-measurements"}, p.Scope) ||
		!slices.Contains([]string{"full-process-exit", "application-ready", "codec-pipeline"}, p.MeasurementKind) {
		return errors.New("explicit supported evidence scope and measurement kind are required")
	}
	return nil
}

func validateCandidates(candidates []candidate) error {
	names := make(map[string]bool)
	for _, c := range candidates {
		if c.Name == "" || names[c.Name] || c.BundleBytes <= 0 || c.BundleBytes > maxBytes ||
			c.PackNS < 0 || c.PackNS > maxDurationNS || c.ColdFailures < 0 || c.WarmFailures < 0 ||
			c.ColdFailures > maxSamples || c.WarmFailures > maxSamples {
			return fmt.Errorf("invalid or duplicate candidate %q", c.Name)
		}
		names[c.Name] = true
		if err := validateSamples(c.ColdNS, c.ColdFailures); err != nil {
			return fmt.Errorf("candidate %s cold: %w", c.Name, err)
		}
		if err := validateSamples(c.WarmNS, c.WarmFailures); err != nil {
			return fmt.Errorf("candidate %s warm: %w", c.Name, err)
		}
	}
	return nil
}

func validateScenarios(scenarios []scenario) error {
	names := make(map[string]bool)
	for _, s := range scenarios {
		if s.Name == "" || names[s.Name] || !finite(s.WarmFraction) || s.WarmFraction < 0 || s.WarmFraction > 1 ||
			!finite(s.BandwidthBytesPerSecond) || s.BandwidthBytesPerSecond < 1 ||
			s.Launches < 0 || s.Launches > maxLaunches || s.Transfers < 0 || s.Transfers > maxLaunches {
			return errors.New("invalid or duplicate scenario")
		}
		names[s.Name] = true
	}
	return nil
}

func validateSamples(samples []int64, failures int) error {
	if len(samples)+failures == 0 || len(samples)+failures > maxSamples {
		return errors.New("observation count outside bounds")
	}
	for _, sample := range samples {
		if sample <= 0 || sample > maxDurationNS {
			return errors.New("duration outside positive bounded nanoseconds")
		}
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func modelCost(c candidate, s scenario) cost {
	result := cost{Candidate: c.Name, BundleBytes: c.BundleBytes, Failures: c.ColdFailures + c.WarmFailures,
		Status: "incomplete-failures; no complete-distribution prediction"}
	if result.Failures > 0 {
		return result
	}
	mean := s.WarmFraction*sampleMean(c.WarmNS) + (1-s.WarmFraction)*sampleMean(c.ColdNS)
	transfer := float64(c.BundleBytes) / s.BandwidthBytesPerSecond * nsPerSecond * float64(s.Transfers)
	upfront := transfer
	if s.IncludePack {
		upfront += float64(c.PackNS)
	}
	total := upfront + float64(s.Launches)*mean
	result.Status, result.MeanStartupNS, result.TransferNS = "descriptive", &mean, &transfer
	result.UpfrontNS, result.MeanLifecycleTotalNS = &upfront, &total
	q50 := mixtureQuantile(c.WarmNS, c.ColdNS, s.WarmFraction, median)
	q99 := mixtureQuantile(c.WarmNS, c.ColdNS, s.WarmFraction, p99)
	result.MixtureP50NS, result.MixtureP99NS = &q50, &q99
	return result
}

func sampleMean(samples []int64) float64 {
	var total float64
	for _, sample := range samples {
		total += float64(sample)
	}
	return total / float64(len(samples))
}

// mixtureQuantile uses the left inverse of the weighted empirical CDF, including atoms.
// Binary64 input probabilities are compared as exact rationals, without epsilon.
// This preserves both atoms and Nextafter-separated thresholds when float sums round down.
func mixtureQuantile(warm, cold []int64, h, q float64) int64 {
	w, c := slices.Clone(warm), slices.Clone(cold)
	slices.Sort(w)
	slices.Sort(c)
	ordered := append(slices.Clone(w), c...)
	slices.Sort(ordered)
	ordered = slices.Compact(ordered)
	warmWeight, threshold := new(big.Rat).SetFloat64(h), new(big.Rat).SetFloat64(q)
	coldWeight := new(big.Rat).Sub(big.NewRat(1, 1), warmWeight)
	var warmTerm, coldTerm, cdf big.Rat
	wi, ci := 0, 0
	for _, value := range ordered {
		for wi < len(w) && w[wi] <= value {
			wi++
		}
		for ci < len(c) && c[ci] <= value {
			ci++
		}
		warmTerm.SetFrac64(int64(wi), int64(len(w)))
		warmTerm.Mul(&warmTerm, warmWeight)
		coldTerm.SetFrac64(int64(ci), int64(len(c)))
		coldTerm.Mul(&coldTerm, coldWeight)
		cdf.Add(&warmTerm, &coldTerm)
		if cdf.Cmp(threshold) >= 0 {
			return value
		}
	}
	return ordered[len(ordered)-1]
}

func breakEven(base, other cost) crossing {
	u := *other.UpfrontNS - *base.UpfrontNS
	d := *other.MeanStartupNS - *base.MeanStartupNS
	result := crossing{Baseline: base.Candidate, Candidate: other.Candidate}
	switch {
	case u == 0 && d == 0:
		result.Direction = "equal"
	case u <= 0 && d <= 0:
		result.Direction = "candidate-always-cheaper"
	case u >= 0 && d >= 0:
		result.Direction = "baseline-always-cheaper"
	default:
		n := -u / d
		result.CrossingLaunches = &n
		result.Direction = "candidate-cheaper-below-crossing"
		if d < 0 {
			result.Direction = "candidate-cheaper-above-crossing"
		}
	}
	return result
}
