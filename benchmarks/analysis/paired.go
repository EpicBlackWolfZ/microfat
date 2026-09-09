package analysis

import (
	"errors"
	"math"
	"math/rand/v2"
	"slices"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

const (
	BootstrapResamples = 10000
	MinimumPairs       = 5
	maximumResamples   = 100000
	maximumPairs       = 10000
	pairedLowQuantile  = 0.025
	pairedHighQuantile = 0.975
	medianQuantile     = 0.5
	percentageScale    = 100
	pcgStream          = uint64(0xda3e39cb94b95bdb)
	PairedAlgorithm    = "paired-median-percentile-bootstrap-pcg-v1"
)

// Paired bootstraps trial blocks, never requests pooled from within those blocks.
func Paired(pairs []schema.Pair, seed uint64, resamples int) (schema.Comparison, error) {
	result := schema.Comparison{Pairs: slices.Clone(pairs), Seed: seed, Resamples: resamples, Algorithm: PairedAlgorithm}
	if len(pairs) == 0 || len(pairs) > maximumPairs || resamples <= 0 || resamples > maximumResamples {
		return result, errors.New("invalid paired bootstrap bounds")
	}
	differences := make([]float64, len(pairs))
	percentages := make([]float64, 0, len(pairs))
	seen := make(map[int]bool)
	for i, pair := range pairs {
		if pair.Block < 0 || seen[pair.Block] || !schema.Finite(pair.Baseline) || !schema.Finite(pair.Candidate) {
			return result, errors.New("invalid or duplicate trial pair")
		}
		seen[pair.Block] = true
		differences[i] = pair.Candidate - pair.Baseline
		if !schema.Finite(differences[i]) {
			return result, errors.New("paired difference overflow")
		}
		if pair.Baseline != 0 {
			value := differences[i] / pair.Baseline * percentageScale
			if schema.Finite(value) {
				percentages = append(percentages, value)
			}
		}
	}
	result.MedianDifference = Quantile(differences, medianQuantile)
	if len(percentages) == len(pairs) {
		value := Quantile(percentages, medianQuantile)
		result.RelativePercent = &value
	}
	if len(pairs) < MinimumPairs {
		result.Status, result.Reason = "descriptive", "fewer than five complete trial pairs"
		return result, nil
	}
	rng := rand.New(rand.NewPCG(seed, seed^pcgStream)) // #nosec G404 -- reproducible statistical sampling, not security.
	bootstrap := make([]float64, resamples)
	sample := make([]float64, len(pairs))
	for i := range resamples {
		for j := range sample {
			sample[j] = differences[rng.IntN(len(differences))]
		}
		bootstrap[i] = Quantile(sample, medianQuantile)
	}
	result.Low, result.High = Quantile(bootstrap, pairedLowQuantile), Quantile(bootstrap, pairedHighQuantile)
	result.Significant = result.Low > 0 || result.High < 0
	result.Status = "complete"
	return result, nil
}

// Quantile uses R7 interpolation without mutating its finite input values.
func Quantile(values []float64, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	q := math.Max(0, math.Min(1, quantile)) * float64(len(ordered)-1)
	index := int(q)
	if index == len(ordered)-1 {
		return ordered[index]
	}
	// A convex combination avoids subtracting opposite extreme finite values.
	weight := q - float64(index)
	return (1-weight)*ordered[index] + weight*ordered[index+1]
}
