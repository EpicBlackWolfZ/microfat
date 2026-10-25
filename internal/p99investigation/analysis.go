package p99investigation

import (
	"errors"
	"fmt"
	"math"
	"slices"

	benchmarkanalysis "github.com/EpicBlackWolfZ/microfat/benchmarks/analysis"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/compare"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

const (
	analysisBlocks            = 20
	analysisConfigurations    = 10
	analysisFamilyComparisons = 12
	analysisFamilyAlpha       = 0.05
	analysisMemoryMargin      = 0.000250
	analysisMixedMargin       = 0.000010
	analysisMetric            = "latency_p99"
	analysisMemoryScenario    = "memory32"
	analysisMixedScenario     = "mixed4"
	analysisPrimaryRole       = "primary"
	analysisControlRole       = "native_control"
	analysisDescriptiveRole   = "descriptive"
	analysisInconclusive      = "inconclusive"
	analysisEquivalent        = "practical_equivalence"
	analysisRegression        = "regression"
	analysisImprovement       = "improvement"
	analysisBinomialBase      = 0.5
	analysisIntervalTails     = 2
)

// Analysis retains every configuration and the original bootstrap diagnostic.
// Exact intervals concern medians of paired per-trial p99 differences, never a
// pooled request percentile or a complete-process startup percentile.
type Analysis struct {
	Scenario                 string               `json:"scenario"`
	PrimaryConfiguration     string               `json:"primary_configuration"`
	MarginSeconds            float64              `json:"margin_seconds"`
	ExpectedPairs            int                  `json:"expected_pairs"`
	FamilyComparisons        int                  `json:"family_comparisons"`
	FamilyAlpha              float64              `json:"family_alpha"`
	Complete                 bool                 `json:"complete"`
	Status                   string               `json:"status"`
	Reasons                  []string             `json:"reasons"`
	Comparisons              []ComparisonAnalysis `json:"comparisons"`
	NativeControlsEquivalent bool                 `json:"native_controls_equivalent"`
	FollowupWarranted        bool                 `json:"followup_warranted"`
	Attribution              string               `json:"attribution"`
	AttributionStatus        string               `json:"attribution_status"`
	Assumptions              []string             `json:"assumptions"`
}

type ComparisonAnalysis struct {
	Configuration string            `json:"configuration"`
	Role          string            `json:"role"`
	Status        string            `json:"status"`
	Reasons       []string          `json:"reasons"`
	Bootstrap     schema.Comparison `json:"bootstrap_95_diagnostic"`
	Interval      *MedianInterval   `json:"simultaneous_median_difference_interval,omitempty"`
	Drift         DescriptiveDrift  `json:"descriptive_host_order_drift"`
}

// DescriptiveDrift retains planned block and trial-order strata without adding
// inferential comparisons or independent observations to the frozen family.
type DescriptiveDrift struct {
	FirstTenBlocks DriftGroup `json:"first_ten_blocks"`
	LastTenBlocks  DriftGroup `json:"last_ten_blocks"`
	BaseBeforeHead DriftGroup `json:"base_before_head"`
	HeadBeforeBase DriftGroup `json:"head_before_base"`
}

type DriftGroup struct {
	Pairs            int      `json:"pairs"`
	MedianDifference *float64 `json:"median_difference_seconds"`
}

// MedianInterval is an exact symmetric order-statistic interval. Coverage is a
// lower bound under independent stationary blocks; ties can make it conservative.
type MedianInterval struct {
	Low              float64 `json:"low_seconds"`
	High             float64 `json:"high_seconds"`
	LowerRank        int     `json:"lower_rank_one_based"`
	UpperRank        int     `json:"upper_rank_one_based"`
	Pairs            int     `json:"pairs"`
	Alpha            float64 `json:"alpha"`
	NominalCoverage  float64 `json:"nominal_coverage"`
	AchievedCoverage float64 `json:"achieved_coverage"`
	Algorithm        string  `json:"algorithm"`
}

// Analyze applies the frozen #230 decision rule to one of the original scenarios.
// Incomplete cohorts retain all available diagnostics without inferring from a
// favorable surviving subset. The experiment's existing comparisons are not used.
func Analyze(exp *schema.ExperimentV2, scenario string) (Analysis, error) {
	result, err := analysisScenario(scenario)
	if err != nil {
		return result, err
	}
	if err := schema.ValidateV2(exp); err != nil {
		return result, err
	}
	if err := analysisScopes(exp); err != nil {
		return result, err
	}
	result.Reasons = analysisCohortReasons(exp)
	result.Complete = len(result.Reasons) == 0
	for _, suffix := range analysisSuffixes() {
		comparison, err := analyzeConfiguration(exp, suffix, result)
		if err != nil {
			return result, err
		}
		result.Comparisons = append(result.Comparisons, comparison)
		if comparison.Role == analysisPrimaryRole {
			result.Status = comparison.Status
		}
	}
	result.NativeControlsEquivalent = result.Complete
	for _, comparison := range result.Comparisons {
		if comparison.Role == analysisControlRole && comparison.Status != analysisEquivalent {
			result.NativeControlsEquivalent = false
		}
	}
	result.FollowupWarranted = result.NativeControlsEquivalent &&
		(result.Status == analysisImprovement || result.Status == analysisRegression)
	result.AttributionStatus = analysisInconclusive
	if result.NativeControlsEquivalent && result.Status == analysisEquivalent {
		result.AttributionStatus = analysisEquivalent
	} else if result.FollowupWarranted {
		result.AttributionStatus = "requires_independent_confirmation"
	}
	result.Attribution = "source causality is not established by a shared-host cohort; original hosted signals remain unresolved"
	if !result.NativeControlsEquivalent {
		result.Attribution += "; unchanged native controls did not both establish practical equivalence"
	}
	return result, nil
}

func analysisScenario(scenario string) (Analysis, error) {
	result := Analysis{Scenario: scenario, ExpectedPairs: analysisBlocks, FamilyComparisons: analysisFamilyComparisons,
		FamilyAlpha: analysisFamilyAlpha, Status: analysisInconclusive,
		Assumptions: []string{
			"independent stationary blocks with a common population median of paired per-trial p99 differences; not proved by scheduling",
			"Bonferroni family contains 12 prespecified primary/native comparisons across four cohorts",
			"bootstrap 95% intervals are retained diagnostics without a multiplicity-adjusted decision role",
			"all outcomes are retained; requests within a load trial do not add independent observations",
		}}
	switch scenario {
	case analysisMemoryScenario:
		result.PrimaryConfiguration, result.MarginSeconds = "fat-cache-cold", analysisMemoryMargin
	case analysisMixedScenario:
		result.PrimaryConfiguration, result.MarginSeconds = "fat-memfd", analysisMixedMargin
	default:
		return result, errors.New("unknown historical p99 scenario")
	}
	return result, nil
}

func analysisSuffixes() []string {
	return []string{"native-generic", "native-specialized", "fat-memfd", "fat-cache-cold", "fat-cache-warm"}
}

func analysisScopes(exp *schema.ExperimentV2) error {
	for _, trial := range exp.Trials {
		metric, ok := trial.Metrics[analysisMetric]
		if !ok {
			continue
		}
		if metric.Unit != "s" || metric.Phase != "steady_state" || metric.Source != "fortio-histogram" ||
			metric.Kind != "measured" || metric.Value != nil && *metric.Value < 0 {
			return fmt.Errorf("trial %s has an incompatible service-p99 measurement scope or value", trial.ID)
		}
	}
	return nil
}

func analysisCohortReasons(exp *schema.ExperimentV2) []string {
	var reasons []string
	if !exp.Complete {
		reasons = append(reasons, "experiment is incomplete")
	}
	if len(exp.Configurations) != analysisConfigurations || len(exp.Schedule) != analysisConfigurations*analysisBlocks {
		reasons = append(reasons, "cohort does not contain the frozen ten-configuration, twenty-block schedule")
	}
	trials := make(map[string]schema.ProcessTrial, len(exp.Trials))
	for _, trial := range exp.Trials {
		trials[trial.ID] = trial
	}
	for _, suffix := range analysisSuffixes() {
		for _, prefix := range []string{"base-", "head-"} {
			configuration := prefix + suffix
			count := 0
			for _, scheduled := range exp.Schedule {
				if scheduled.ConfigurationID != configuration {
					continue
				}
				count++
				if scheduled.Block >= analysisBlocks {
					reasons = append(reasons, fmt.Sprintf("%s: block %d is outside the frozen range", configuration, scheduled.Block))
				}
				trial, ok := trials[scheduled.ID]
				if !ok {
					reasons = append(reasons, fmt.Sprintf("%s block %d: missing trial %s", configuration, scheduled.Block, scheduled.ID))
					continue
				}
				if trial.Outcome != schema.OutcomeOK {
					reasons = append(reasons, fmt.Sprintf("%s block %d: %s: %s", configuration, trial.Block, trial.Outcome, trial.Reason))
				} else if metric := trial.Metrics[analysisMetric]; metric.Value == nil {
					reason := metric.Reason
					if reason == "" {
						reason = "missing service-p99 measurement"
					}
					reasons = append(reasons, fmt.Sprintf("%s block %d: %s", configuration, trial.Block, reason))
				}
			}
			if count != analysisBlocks {
				reasons = append(reasons, fmt.Sprintf("%s: scheduled %d blocks; expected %d", configuration, count, analysisBlocks))
			}
		}
	}
	return reasons
}

func analyzeConfiguration(exp *schema.ExperimentV2, suffix string, result Analysis) (ComparisonAnalysis, error) {
	bootstrap, err := compare.Metric(exp, "base-"+suffix, "head-"+suffix, analysisMetric)
	if err != nil {
		return ComparisonAnalysis{}, err
	}
	comparison := ComparisonAnalysis{Configuration: suffix, Bootstrap: bootstrap, Role: analysisDescriptiveRole,
		Status: analysisDescriptiveRole, Drift: descriptiveDrift(exp, bootstrap.Pairs)}
	// Empty cohorts have no bootstrap estimate, but still retain the frozen method.
	comparison.Bootstrap.Seed, comparison.Bootstrap.Resamples = exp.Seed, benchmarkanalysis.BootstrapResamples
	comparison.Bootstrap.Algorithm = benchmarkanalysis.PairedAlgorithm
	if suffix == result.PrimaryConfiguration {
		comparison.Role = analysisPrimaryRole
	} else if suffix == "native-generic" || suffix == "native-specialized" {
		comparison.Role = analysisControlRole
	}
	if comparison.Role == analysisDescriptiveRole {
		return comparison, nil
	}
	comparison.Status = analysisInconclusive
	if !result.Complete || len(bootstrap.Pairs) != analysisBlocks {
		comparison.Reasons = slices.Clone(result.Reasons)
		if bootstrap.Reason != "" {
			comparison.Reasons = append(comparison.Reasons, bootstrap.Reason)
		}
		return comparison, nil
	}
	differences := make([]float64, len(bootstrap.Pairs))
	for i, pair := range bootstrap.Pairs {
		differences[i] = pair.Candidate - pair.Baseline
	}
	interval, err := exactMedianInterval(differences, analysisFamilyAlpha/analysisFamilyComparisons)
	if err != nil {
		return comparison, err
	}
	comparison.Interval = &interval
	comparison.Status = classifyInterval(interval, result.MarginSeconds)
	return comparison, nil
}

func descriptiveDrift(exp *schema.ExperimentV2, pairs []schema.Pair) DescriptiveDrift {
	positions := make(map[string]int, len(exp.Trials))
	for _, trial := range exp.Trials {
		positions[trial.ID] = trial.Position
	}
	var first, last, baseFirst, headFirst []float64
	for _, pair := range pairs {
		difference := pair.Candidate - pair.Baseline
		if pair.Block < analysisBlocks/2 {
			first = append(first, difference)
		} else if pair.Block < analysisBlocks {
			last = append(last, difference)
		}
		if positions[pair.BaselineTrial] < positions[pair.CandidateTrial] {
			baseFirst = append(baseFirst, difference)
		} else {
			headFirst = append(headFirst, difference)
		}
	}
	return DescriptiveDrift{FirstTenBlocks: driftGroup(first), LastTenBlocks: driftGroup(last),
		BaseBeforeHead: driftGroup(baseFirst), HeadBeforeBase: driftGroup(headFirst)}
}

func driftGroup(differences []float64) DriftGroup {
	group := DriftGroup{Pairs: len(differences)}
	if len(differences) > 0 {
		median := benchmarkanalysis.Quantile(differences, analysisBinomialBase)
		group.MedianDifference = &median
	}
	return group
}

// exactMedianInterval chooses the narrowest symmetric finite order-statistic
// bounds whose binomial coverage reaches the requested level. No asymptotic or
// bootstrap approximation is involved. Ranks and coverage are retained for audit.
func exactMedianInterval(differences []float64, alpha float64) (MedianInterval, error) {
	if len(differences) == 0 || len(differences) > analysisBlocks || !schema.Finite(alpha) || alpha <= 0 || alpha >= 1 {
		return MedianInterval{}, errors.New("invalid exact median interval bounds")
	}
	for _, value := range differences {
		if !schema.Finite(value) {
			return MedianInterval{}, errors.New("non-finite paired difference")
		}
	}
	ordered := slices.Clone(differences)
	slices.Sort(ordered)
	n := len(ordered)
	probability := math.Pow(analysisBinomialBase, float64(n))
	tail := 0.0
	rank, achieved := 0, 0.0
	for k := 1; k <= (n+1)/2; k++ {
		tail += probability
		coverage := 1 - analysisIntervalTails*tail
		if coverage < 1-alpha {
			break
		}
		rank, achieved = k, coverage
		probability *= float64(n-k+1) / float64(k)
	}
	if rank == 0 {
		return MedianInterval{}, errors.New("too few pairs for finite exact bounds at the requested coverage")
	}
	return MedianInterval{Low: ordered[rank-1], High: ordered[n-rank], LowerRank: rank, UpperRank: n - rank + 1,
		Pairs: n, Alpha: alpha, NominalCoverage: 1 - alpha, AchievedCoverage: achieved,
		Algorithm: "symmetric-median-order-statistic-binomial-v1"}, nil
}

func classifyInterval(interval MedianInterval, margin float64) string {
	switch {
	case interval.Low > margin:
		return analysisRegression
	case interval.High < -margin:
		return analysisImprovement
	case interval.Low >= -margin && interval.High <= margin:
		return analysisEquivalent
	default:
		return analysisInconclusive
	}
}
