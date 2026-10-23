package research377

import (
	"errors"
	"math/rand/v2"
)

const (
	DefaultSamples      = 600
	DefaultReplications = 500
	DefaultAlpha        = 0.05
	DefaultMargin       = 0.01
	MaximumReplications = 5000
	maximumWork         = 5000000
	correlatedCluster   = 25
	commonRisk          = 0.01
	baselineEffectRisk  = 0.10
	candidateEffectRisk = 0.005
	mixtureRisk         = 0.03
	pcgStream           = uint64(0xda3e39cb94b95bdb)
)

type CalibrationConfig struct {
	Seed         uint64 `json:"seed"`
	Samples      int    `json:"samples_per_arm"`
	Replications int    `json:"replications"`
}

type Scenario struct {
	Name               string   `json:"name"`
	IID                bool     `json:"iid_assumption_satisfied"`
	ClusterSize        int      `json:"shared_latent_cluster_size"`
	BaselineRisk       float64  `json:"true_baseline_risk"`
	CandidateRisk      float64  `json:"true_candidate_risk"`
	BaselineCoverage   float64  `json:"baseline_interval_coverage"`
	CandidateCoverage  float64  `json:"candidate_interval_coverage"`
	DifferenceCoverage float64  `json:"difference_interval_coverage"`
	CoverageMC         Interval `json:"difference_coverage_monte_carlo_interval"`
	FalseDirection     float64  `json:"false_material_direction_rate"`
	ImprovementPower   float64  `json:"useful_improvement_rate"`
	EquivalentRate     float64  `json:"practical_equivalence_rate"`
	InconclusiveRate   float64  `json:"inconclusive_rate"`
}

type Calibration struct {
	SchemaVersion int               `json:"schema_version"`
	Method        string            `json:"method"`
	Config        CalibrationConfig `json:"config"`
	Alpha         float64           `json:"comparison_alpha"`
	Margin        float64           `json:"practical_margin_probability"`
	Scenarios     []Scenario        `json:"scenarios"`
	Limitations   []string          `json:"limitations"`
}

// Calibrate simulates binary SLO events from known generative models. The rare
// mixture represents arbitrary fast/slow conditional latency distributions: only
// their known mixture probability is relevant to the exceedance estimand.
func Calibrate(config CalibrationConfig) (Calibration, error) {
	if config.Samples < 1 || config.Samples > MaximumSamples || config.Replications < 1 ||
		config.Replications > MaximumReplications || config.Samples > maximumWork/config.Replications {
		return Calibration{}, errors.New("calibration exceeds sample, replication or total work cap")
	}
	rng := rand.New(rand.NewPCG(config.Seed, config.Seed^pcgStream)) // #nosec G404 -- reproducible simulation, not security.
	scenarios := []Scenario{
		{Name: "iid-no-change", IID: true, ClusterSize: 1, BaselineRisk: commonRisk, CandidateRisk: commonRisk},
		{Name: "iid-injected-benefit", IID: true, ClusterSize: 1,
			BaselineRisk: baselineEffectRisk, CandidateRisk: candidateEffectRisk},
		{Name: "iid-rare-slow-mixture", IID: true, ClusterSize: 1, BaselineRisk: mixtureRisk, CandidateRisk: mixtureRisk},
		{Name: "correlated-bursts-naive-iid-invalid", IID: false, ClusterSize: correlatedCluster,
			BaselineRisk: mixtureRisk, CandidateRisk: mixtureRisk},
	}
	for i := range scenarios {
		scenarios[i] = calibrateScenario(rng, config, scenarios[i])
	}
	return Calibration{
		SchemaVersion: SchemaVersion, Method: Method, Config: config,
		Alpha: DefaultAlpha, Margin: DefaultMargin, Scenarios: scenarios,
		Limitations: []string{
			"Fixed sample sizes and one predeclared comparison; repeated peeking is unsupported.",
			"Known binary event models test SLO-risk inference, not a fitted universal latency distribution.",
			"Mixture coverage is independent of the latency law inside each side of the SLO, including heavy tails.",
			"The correlated scenario deliberately violates IID; its nominal bounds must not be used for real bursts.",
			"Monte Carlo intervals describe finite simulation uncertainty, not proof for untested populations.",
		},
	}, nil
}

func calibrateScenario(rng *rand.Rand, config CalibrationConfig, scenario Scenario) Scenario {
	var baselineCovered, candidateCovered, differenceCovered, falseDirection, improvements, equivalents, inconclusive int
	// Cache each possible interval: calibration has a fixed n and alpha.
	cache := make(map[int]Interval)
	bound := func(events int) Interval {
		interval, ok := cache[events]
		if !ok {
			interval, _ = BinomialInterval(events, config.Samples, DefaultAlpha/armCount)
			cache[events] = interval
		}
		return interval
	}
	truth := scenario.CandidateRisk - scenario.BaselineRisk
	for range config.Replications {
		baseEvents := clusteredEvents(rng, config.Samples, scenario.ClusterSize, scenario.BaselineRisk)
		candidateEvents := clusteredEvents(rng, config.Samples, scenario.ClusterSize, scenario.CandidateRisk)
		baseline, candidate := bound(baseEvents), bound(candidateEvents)
		difference := Interval{Low: candidate.Low - baseline.High, High: candidate.High - baseline.Low}
		baselineCovered += indicator(contains(baseline, scenario.BaselineRisk))
		candidateCovered += indicator(contains(candidate, scenario.CandidateRisk))
		differenceCovered += indicator(contains(difference, truth))
		decision := Decide(difference, DefaultMargin)
		falseDirection += indicator((decision == Improvement && truth >= -DefaultMargin) ||
			(decision == Regression && truth <= DefaultMargin))
		improvements += indicator(decision == Improvement)
		equivalents += indicator(decision == Equivalent)
		inconclusive += indicator(decision == Inconclusive)
	}
	scale := float64(config.Replications)
	scenario.BaselineCoverage, scenario.CandidateCoverage = float64(baselineCovered)/scale, float64(candidateCovered)/scale
	scenario.DifferenceCoverage = float64(differenceCovered) / scale
	scenario.CoverageMC, _ = BinomialInterval(differenceCovered, config.Replications, DefaultAlpha)
	scenario.FalseDirection, scenario.ImprovementPower = float64(falseDirection)/scale, float64(improvements)/scale
	scenario.EquivalentRate, scenario.InconclusiveRate = float64(equivalents)/scale, float64(inconclusive)/scale
	return scenario
}

func clusteredEvents(rng *rand.Rand, samples, cluster int, risk float64) int {
	events := 0
	for start := 0; start < samples; start += cluster {
		if rng.Float64() < risk {
			events += min(cluster, samples-start)
		}
	}
	return events
}

func indicator(condition bool) int {
	if condition {
		return 1
	}
	return 0
}

func contains(interval Interval, value float64) bool {
	return interval.Low <= value && value <= interval.High
}
