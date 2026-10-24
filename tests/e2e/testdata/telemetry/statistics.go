//go:build linux

package main

import (
	"math/rand/v2"
	"slices"
)

const (
	seed                    = 41
	resamples               = 2000
	armCount                = 2
	bootstrapTailDivisor    = 40
	equivalenceLower        = 0.95
	equivalenceUpper        = 1.05
	usefulRatio             = 0.99
	usefulSavingNS          = 5000
	cpuUpper                = 1.10
	rssRelativeAllowance    = 0.10
	rssAbsoluteAllowanceKiB = 2048
)

type interval struct{ Median, Lower95, Upper95 float64 }
type decision struct {
	cell
	PlannedPairs, CompletePairs                           int
	WallRatio, SavingNS, CPURatio, RSSIncreaseKiB         interval
	BaselineRSSMedianKiB                                  float64
	WallClassification                                    string
	UsefulImprovement, ResourcePass, ConditionalOnSuccess bool
}

func evaluate(observations []observation) ([]decision, bool, bool, bool) {
	plan := schedule()
	correct := len(observations) == len(plan)
	for i, obs := range observations {
		if !validObservation(obs) || i >= len(plan) || obs.scheduled != plan[i] {
			correct = false
		}
	}
	resource, equivalent := true, correct
	var decisions []decision
	for _, target := range cells() {
		byRound := map[int]map[string]observation{}
		for _, obs := range observations {
			if obs.cell != target {
				continue
			}
			if byRound[obs.Round] == nil {
				byRound[obs.Round] = map[string]observation{}
			}
			byRound[obs.Round][obs.Arm] = obs
		}
		var wall, saving, cpu, rss, baseRSS []float64
		for round := range pairs {
			a, b := byRound[round][baseline], byRound[round][candidateArm]
			if validObservation(a) && validObservation(b) {
				wall = append(wall, float64(b.WallNS)/float64(a.WallNS))
				saving = append(saving, float64(a.WallNS-b.WallNS))
				cpu = append(cpu, float64(b.CPUNS)/float64(a.CPUNS))
				rss = append(rss, float64(b.MaxRSSKiB-a.MaxRSSKiB))
				baseRSS = append(baseRSS, float64(a.MaxRSSKiB))
			}
		}
		d := decision{cell: target, PlannedPairs: pairs, CompletePairs: len(wall), ConditionalOnSuccess: !correct,
			WallClassification: "incomplete"}
		if len(wall) > 0 {
			d.WallRatio, d.SavingNS = bootstrap(wall), bootstrap(saving)
			d.CPURatio, d.RSSIncreaseKiB = bootstrap(cpu), bootstrap(rss)
			d.BaselineRSSMedianKiB = median(baseRSS)
			d.ResourcePass = len(wall) == pairs && d.CPURatio.Upper95 <= cpuUpper &&
				d.RSSIncreaseKiB.Upper95 <= rssRelativeAllowance*d.BaselineRSSMedianKiB+rssAbsoluteAllowanceKiB
			if len(wall) == pairs {
				d.WallClassification = classify(d.WallRatio)
			}
			d.UsefulImprovement = len(wall) == pairs && target.Profile != native && target.Logging == off &&
				d.WallRatio.Upper95 <= usefulRatio && d.SavingNS.Lower95 >= usefulSavingNS
		}
		resource = resource && d.ResourcePass
		equivalent = equivalent && d.WallClassification == "equivalent"
		decisions = append(decisions, d)
	}
	return decisions, correct, resource, equivalent
}

func validObservation(obs observation) bool {
	return obs.Success && obs.Error == "" && obs.CacheError == "" && obs.CleanupError == "" &&
		obs.WallNS > 0 && obs.CPUNS > 0 && obs.MaxRSSKiB > 0 && obs.MaxRSSKiB <= maxRSSKiB
}

func classify(value interval) string {
	if value.Lower95 >= equivalenceLower && value.Upper95 <= equivalenceUpper {
		return "equivalent"
	}
	if value.Lower95 > equivalenceUpper {
		return "regression"
	}
	if value.Upper95 < equivalenceLower {
		return "improvement_outside_equivalence"
	}
	return "inconclusive"
}

func bootstrap(values []float64) interval {
	if len(values) == 0 {
		return interval{}
	}
	rng := rand.New(rand.NewPCG(seed, seed)) // #nosec G404 -- fixed statistical resampling, no security purpose.
	sample, estimates := make([]float64, len(values)), make([]float64, resamples)
	for n := range estimates {
		for i := range sample {
			sample[i] = values[rng.IntN(len(values))]
		}
		estimates[n] = median(sample)
	}
	slices.Sort(estimates)
	tail := resamples / bootstrapTailDivisor
	return interval{median(values), estimates[tail], estimates[resamples-tail-1]}
}

func median(values []float64) float64 {
	v := slices.Clone(values)
	slices.Sort(v)
	if len(v)%2 == 1 {
		return v[len(v)/2]
	}
	return (v[len(v)/armCount-1] + v[len(v)/armCount]) / armCount
}
