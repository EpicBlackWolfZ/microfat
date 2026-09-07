// Package analysis provides deterministic statistical calculations over raw benchmark sample distributions
// using canonical Linear Interpolation (Method R7) for percentiles.
package analysis

import (
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

const (
	percentileP50  = 0.50
	percentileP75  = 0.75
	percentileP90  = 0.90
	percentileP95  = 0.95
	percentileP99  = 0.99
	percentileP999 = 0.999

	nanosecondsPerSecond = 1e9
)

var (
	ErrEmptySamples       = errors.New("raw samples slice cannot be empty")
	ErrNegativeDuration   = errors.New("total duration must be greater than zero")
	ErrNegativeOperations = errors.New("total operations cannot be negative")
	ErrNegativeSample     = errors.New("sample duration cannot be negative")
)

// ComputeAnalysis derives statistical metrics from raw nanosecond duration samples.
func ComputeAnalysis(samples []int64, totalOps int64, totalDurationNs int64) (*schema.ScenarioAnalysis, error) {
	if len(samples) == 0 {
		return nil, ErrEmptySamples
	}
	if totalDurationNs <= 0 {
		return nil, fmt.Errorf("%w: %d ns", ErrNegativeDuration, totalDurationNs)
	}
	if totalOps < 0 {
		return nil, fmt.Errorf("%w: %d", ErrNegativeOperations, totalOps)
	}

	for _, s := range samples {
		if s < 0 {
			return nil, fmt.Errorf("%w: %d ns", ErrNegativeSample, s)
		}
	}

	sorted := slices.Clone(samples)
	slices.Sort(sorted)

	sampleCount := len(sorted)
	minNs := sorted[0]
	maxNs := sorted[sampleCount-1]

	// Numerically stable single-pass mean and variance accumulation via Welford's algorithm.
	// This avoids precision loss and catastrophic cancellation with large nanosecond durations.
	var mean float64
	var m2 float64
	for k, s := range sorted {
		x := float64(s)
		count := float64(k + 1)
		delta := x - mean
		mean += delta / count
		delta2 := x - mean
		m2 += delta * delta2
	}
	meanNs := mean
	stdDevNs := math.Sqrt(m2 / float64(sampleCount))

	percentiles := map[string]float64{
		"p50":   linearInterpolationR7(sorted, percentileP50),
		"p75":   linearInterpolationR7(sorted, percentileP75),
		"p90":   linearInterpolationR7(sorted, percentileP90),
		"p95":   linearInterpolationR7(sorted, percentileP95),
		"p99":   linearInterpolationR7(sorted, percentileP99),
		"p99.9": linearInterpolationR7(sorted, percentileP999),
	}

	throughput := 0.0
	if totalOps > 0 {
		throughput = (float64(totalOps) * nanosecondsPerSecond) / float64(totalDurationNs)
	}

	return &schema.ScenarioAnalysis{
		AlgorithmVersion:    schema.AlgorithmVersionV1,
		PercentileMethod:    schema.PercentileMethodLinearR7,
		SampleCount:         sampleCount,
		MinNs:               minNs,
		MaxNs:               maxNs,
		MeanNs:              meanNs,
		StdDevNs:            stdDevNs,
		PercentilesNs:       percentiles,
		ThroughputOpsPerSec: throughput,
	}, nil
}

// linearInterpolationR7 computes percentiles according to Hyndman and Fan Method 7 (Excel, R, NumPy default).
// Formula: r = p * (N - 1), i = floor(r), f = r - i, P = x_i + f * (x_{i+1} - x_i).
func linearInterpolationR7(sorted []int64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0.0
	}
	if n == 1 {
		return float64(sorted[0])
	}
	if p <= 0.0 {
		return float64(sorted[0])
	}
	if p >= 1.0 {
		return float64(sorted[n-1])
	}

	r := p * float64(n-1)
	i := int(math.Floor(r))
	if i >= n-1 {
		return float64(sorted[n-1])
	}

	f := r - float64(i)
	// Convert both terms to float64 before subtracting to prevent int64 arithmetic overflow with large sample values.
	return float64(sorted[i]) + f*(float64(sorted[i+1])-float64(sorted[i]))
}
