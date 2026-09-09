// Package ci applies coarse, explicitly calibrated regression thresholds to paired evidence.
package ci

import (
	"errors"
	"fmt"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

const (
	StartupThresholdPercent = 15.0
	SizeThresholdPercent    = 5.0
	percentageScale         = 100
)

type Policy struct {
	StartupPercent float64 `json:"startup_percent"`
	StartupFloorNS float64 `json:"startup_floor_ns"`
	SizePercent    float64 `json:"size_percent"`
	Calibrated     bool    `json:"calibrated"`
}

type Decision struct {
	Configuration   string   `json:"configuration"`
	Metric          string   `json:"metric"`
	Status          string   `json:"status"`
	Reason          string   `json:"reason"`
	Difference      float64  `json:"difference"`
	RelativePercent *float64 `json:"relative_percent"`
}

func DefaultPolicy() Policy {
	return Policy{StartupPercent: StartupThresholdPercent, SizePercent: SizeThresholdPercent}
}

func Evaluate(comparisons []schema.Comparison, policy Policy) ([]Decision, bool, error) {
	if !schema.Finite(policy.StartupPercent) || policy.StartupPercent <= 0 || !schema.Finite(policy.SizePercent) ||
		policy.SizePercent <= 0 || !schema.Finite(policy.StartupFloorNS) || policy.StartupFloorNS < 0 ||
		policy.Calibrated && policy.StartupFloorNS == 0 {
		return nil, false, errors.New("invalid coarse regression policy")
	}
	var decisions []Decision
	regression := false
	for _, c := range comparisons {
		d := Decision{Configuration: c.Candidate, Metric: c.Metric, Status: "report_only", Difference: c.MedianDifference,
			RelativePercent: c.RelativePercent, Reason: "metric has no calibrated PR gate"}
		if c.Status != "complete" {
			d.Status, d.Reason = "inconclusive", c.Reason
		} else if c.Metric == "artifact_bytes" || c.Metric == "startup_ns" {
			d = coarseDecision(c, policy, d)
		}
		regression = regression || d.Status == "regression"
		decisions = append(decisions, d)
	}
	return decisions, regression, nil
}

func coarseDecision(c schema.Comparison, policy Policy, d Decision) Decision {
	if c.RelativePercent == nil {
		d.Status, d.Reason = "inconclusive", "relative change unavailable for zero baseline"
		return d
	}
	threshold, floor := policy.SizePercent, 0.0
	if c.Metric == "startup_ns" {
		if !policy.Calibrated {
			d.Status, d.Reason = "report_only", "startup gate awaits repeated no-change calibration"
			return d
		}
		threshold, floor = policy.StartupPercent, policy.StartupFloorNS
	}
	d.Status, d.Reason = "pass", "within coarse regression threshold"
	if *c.RelativePercent > threshold && c.MedianDifference > floor {
		d.Status, d.Reason = "regression", fmt.Sprintf("exceeds %.2f%% and absolute floor %.0f", threshold, floor)
	}
	return d
}

func Summary(decisions []Decision) string {
	var output strings.Builder
	output.WriteString("## Benchmark regression evidence\n\n")
	output.WriteString("Coarse gates are distinct from statistical significance. Report-only and inconclusive results are not passes.\n\n")
	output.WriteString("| Configuration | Metric | Decision | Reason |\n| --- | --- | --- | --- |\n")
	clean := strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ")
	for _, d := range decisions {
		fmt.Fprintf(&output, "| %s | %s | %s | %s |\n", clean.Replace(d.Configuration), clean.Replace(d.Metric),
			d.Status, clean.Replace(d.Reason))
	}
	return output.String()
}
