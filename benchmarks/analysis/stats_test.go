package analysis

import (
	"errors"
	"math"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

const (
	tolerance = 1e-6

	testOpsSmall        = int64(1000)
	testDurationOneSec  = int64(1000000000)
	testThroughputSmall = 1000.0

	handMin5  = int64(10)
	handMax5  = int64(50)
	handMean5 = 30.0

	handP50_5  = 30.0
	handP75_5  = 40.0
	handP90_5  = 46.0
	handP95_5  = 48.0
	handP99_5  = 49.6
	handP999_5 = 49.96

	handP50_4 = 25.0
)

func almostEqual(a, b, eps float64) bool {
	return math.Abs(a-b) <= eps
}

func TestComputeAnalysis(t *testing.T) {
	t.Parallel()

	t.Run("single sample", func(t *testing.T) {
		t.Parallel()
		samples := []int64{42}
		res, err := ComputeAnalysis(samples, testOpsSmall, testDurationOneSec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.AlgorithmVersion != schema.AlgorithmVersionV1 {
			t.Errorf("expected algorithm version %s, got %s", schema.AlgorithmVersionV1, res.AlgorithmVersion)
		}
		if res.PercentileMethod != schema.PercentileMethodLinearR7 {
			t.Errorf("expected percentile method %s, got %s", schema.PercentileMethodLinearR7, res.PercentileMethod)
		}
		if res.SampleCount != 1 {
			t.Errorf("expected sample count 1, got %d", res.SampleCount)
		}
		if res.MinNs != 42 || res.MaxNs != 42 {
			t.Errorf("expected min/max 42, got min=%d max=%d", res.MinNs, res.MaxNs)
		}
		if !almostEqual(res.MeanNs, 42.0, tolerance) {
			t.Errorf("expected mean 42.0, got %f", res.MeanNs)
		}
		if !almostEqual(res.StdDevNs, 0.0, tolerance) {
			t.Errorf("expected stddev 0.0, got %f", res.StdDevNs)
		}
		if !almostEqual(res.PercentilesNs["p50"], 42.0, tolerance) {
			t.Errorf("expected p50 42.0, got %f", res.PercentilesNs["p50"])
		}
		if !almostEqual(res.ThroughputOpsPerSec, testThroughputSmall, tolerance) {
			t.Errorf("expected throughput %f, got %f", testThroughputSmall, res.ThroughputOpsPerSec)
		}
	})

	t.Run("five samples hand-verified", func(t *testing.T) {
		t.Parallel()
		samples := []int64{50, 10, 40, 20, 30} // unsorted intentionally
		res, err := ComputeAnalysis(samples, testOpsSmall, testDurationOneSec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if res.MinNs != handMin5 {
			t.Errorf("expected min %d, got %d", handMin5, res.MinNs)
		}
		if res.MaxNs != handMax5 {
			t.Errorf("expected max %d, got %d", handMax5, res.MaxNs)
		}
		if !almostEqual(res.MeanNs, handMean5, tolerance) {
			t.Errorf("expected mean %f, got %f", handMean5, res.MeanNs)
		}

		expectedStdDev := math.Sqrt(200.0)
		if !almostEqual(res.StdDevNs, expectedStdDev, tolerance) {
			t.Errorf("expected stddev %f, got %f", expectedStdDev, res.StdDevNs)
		}

		if !almostEqual(res.PercentilesNs["p50"], handP50_5, tolerance) {
			t.Errorf("expected p50 %f, got %f", handP50_5, res.PercentilesNs["p50"])
		}
		if !almostEqual(res.PercentilesNs["p75"], handP75_5, tolerance) {
			t.Errorf("expected p75 %f, got %f", handP75_5, res.PercentilesNs["p75"])
		}
		if !almostEqual(res.PercentilesNs["p90"], handP90_5, tolerance) {
			t.Errorf("expected p90 %f, got %f", handP90_5, res.PercentilesNs["p90"])
		}
		if !almostEqual(res.PercentilesNs["p95"], handP95_5, tolerance) {
			t.Errorf("expected p95 %f, got %f", handP95_5, res.PercentilesNs["p95"])
		}
		if !almostEqual(res.PercentilesNs["p99"], handP99_5, tolerance) {
			t.Errorf("expected p99 %f, got %f", handP99_5, res.PercentilesNs["p99"])
		}
		if !almostEqual(res.PercentilesNs["p99.9"], handP999_5, tolerance) {
			t.Errorf("expected p99.9 %f, got %f", handP999_5, res.PercentilesNs["p99.9"])
		}
	})

	t.Run("four samples interpolation", func(t *testing.T) {
		t.Parallel()
		samples := []int64{10, 20, 30, 40}
		res, err := ComputeAnalysis(samples, 0, testDurationOneSec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !almostEqual(res.PercentilesNs["p50"], handP50_4, tolerance) {
			t.Errorf("expected p50 %f, got %f", handP50_4, res.PercentilesNs["p50"])
		}
		if !almostEqual(res.ThroughputOpsPerSec, 0.0, tolerance) {
			t.Errorf("expected zero throughput for zero operations, got %f", res.ThroughputOpsPerSec)
		}
	})

	t.Run("validation errors", func(t *testing.T) {
		t.Parallel()

		if _, err := ComputeAnalysis(nil, testOpsSmall, testDurationOneSec); !errors.Is(err, ErrEmptySamples) {
			t.Fatalf("expected ErrEmptySamples, got %v", err)
		}
		if _, err := ComputeAnalysis([]int64{}, testOpsSmall, testDurationOneSec); !errors.Is(err, ErrEmptySamples) {
			t.Fatalf("expected ErrEmptySamples, got %v", err)
		}

		if _, err := ComputeAnalysis([]int64{100}, testOpsSmall, 0); !errors.Is(err, ErrNegativeDuration) {
			t.Fatalf("expected ErrNegativeDuration, got %v", err)
		}
		if _, err := ComputeAnalysis([]int64{100}, testOpsSmall, -100); !errors.Is(err, ErrNegativeDuration) {
			t.Fatalf("expected ErrNegativeDuration, got %v", err)
		}

		if _, err := ComputeAnalysis([]int64{100}, -1, testDurationOneSec); !errors.Is(err, ErrNegativeOperations) {
			t.Fatalf("expected ErrNegativeOperations, got %v", err)
		}

		if _, err := ComputeAnalysis([]int64{100, -50}, testOpsSmall, testDurationOneSec); !errors.Is(err, ErrNegativeSample) {
			t.Fatalf("expected ErrNegativeSample, got %v", err)
		}
	})
}

func TestLinearInterpolationR7Boundaries(t *testing.T) {
	t.Parallel()

	if val := linearInterpolationR7(nil, 0.5); val != 0.0 {
		t.Errorf("expected 0.0 for nil slice, got %f", val)
	}

	single := []int64{100}
	if val := linearInterpolationR7(single, 0.5); val != 100.0 {
		t.Errorf("expected 100.0 for single element, got %f", val)
	}

	sorted := []int64{10, 20, 30}
	if val := linearInterpolationR7(sorted, -0.5); val != 10.0 {
		t.Errorf("expected 10.0 for negative percentile, got %f", val)
	}
	if val := linearInterpolationR7(sorted, 1.5); val != 30.0 {
		t.Errorf("expected 30.0 for percentile > 1.0, got %f", val)
	}
	if val := linearInterpolationR7(sorted, 1.0); val != 30.0 {
		t.Errorf("expected 30.0 for percentile == 1.0, got %f", val)
	}

	// Large values that would overflow int64 subtraction if performed before float64 conversion:
	largeSamples := []int64{0, math.MaxInt64}
	// At p=0.5: r = 0.5 * 1 = 0.5, i=0, f=0.5.
	// Expected: 0 + 0.5 * (float64(math.MaxInt64) - 0) = float64(math.MaxInt64)/2.
	expectedMid := float64(math.MaxInt64) / 2.0
	if val := linearInterpolationR7(largeSamples, 0.5); math.Abs(val-expectedMid) > 1.0 {
		t.Errorf("expected %f for large samples midpoint, got %f", expectedMid, val)
	}
}
