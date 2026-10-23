package research377

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBinomialIntervalPublishedAndBoundaryReferences(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		events, n int
		alpha     float64
		low, high float64
		tolerance float64
	}{
		// NIST Engineering Statistics Handbook, 7.2.4.1 exact-binomial example.
		{"NIST four defects in twenty", 4, 20, 0.10, 0.071354, 0.401029, 1e-6},
		{"one success", 0, 1, 0.05, 0, 0.975, 1e-12},
		{"one violation", 1, 1, 0.05, 0.025, 1, 1e-12},
		{"zero violations analytic", 0, 1000, 0.05, 0, -math.Expm1(math.Log(0.025) / 1000), 1e-12},
		{"all violations analytic", 1000, 1000, 0.05, math.Exp(math.Log(0.025) / 1000), 1, 1e-12},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			interval, err := BinomialInterval(test.events, test.n, test.alpha)
			require.NoError(t, err)
			assert.InDelta(t, test.low, interval.Low, test.tolerance)
			assert.InDelta(t, test.high, interval.High, test.tolerance)
		})
	}
}

func TestExactFiniteSampleCoverageByEnumeration(t *testing.T) {
	t.Parallel()
	const alpha = 0.05
	for _, n := range []int{1, 2, 5, 20, 50} {
		for _, probability := range []float64{0, 0.001, 0.01, 0.1, 0.5, 0.9, 0.99, 0.999, 1} {
			coverage := 0.0
			for k := 0; k <= n; k++ {
				interval, err := BinomialInterval(k, n, alpha)
				require.NoError(t, err)
				if contains(interval, probability) {
					coverage += referenceMass(n, k, probability)
				}
			}
			assert.GreaterOrEqual(t, coverage, 1-alpha-1e-12, "n=%d p=%g", n, probability)
		}
	}
}

func TestBinomialCDFIndependentSmallPolynomialReference(t *testing.T) {
	t.Parallel()
	for n := 1; n <= 30; n++ {
		for _, probability := range []float64{0, 0.001, 0.1, 0.5, 0.9, 0.999, 1} {
			cumulative := 0.0
			assert.Zero(t, binomialCDF(n, -1, probability))
			for k := 0; k <= n; k++ {
				cumulative += referenceMass(n, k, probability)
				assert.InDelta(t, cumulative, binomialCDF(n, k, probability), 1e-12)
			}
		}
	}
}

func referenceMass(n, k int, probability float64) float64 {
	// Direct small-n polynomial, independent of the implementation's log sums.
	choose := 1.0
	for j := 1; j <= k; j++ {
		choose *= float64(n-j+1) / float64(j)
	}
	return choose * math.Pow(probability, float64(k)) * math.Pow(1-probability, float64(n-k))
}

func TestBinomialIntervalCapAndTailSampleCost(t *testing.T) {
	t.Parallel()
	// The sample maximum bounds a continuous population p99 with one-sided
	// 95% confidence only once .99^n <= .05. Zero SLO violations at that
	// same threshold give the corresponding binomial bound (90% two-sided).
	below, err := BinomialInterval(0, 298, 0.10)
	require.NoError(t, err)
	above, err := BinomialInterval(0, 299, 0.10)
	require.NoError(t, err)
	assert.Greater(t, below.High, 0.01)
	assert.Less(t, above.High, 0.01)
	large, err := BinomialInterval(MaximumSamples/2, MaximumSamples, 1e-9)
	require.NoError(t, err)
	assert.InDelta(t, 1-large.High, large.Low, 1e-12)
	assert.Less(t, large.Low, 0.5)
	assert.Greater(t, large.High, 0.5)
	for _, test := range []struct {
		events, n int
		alpha     float64
	}{
		{0, 0, DefaultAlpha}, {0, MaximumSamples + 1, DefaultAlpha}, {-1, 1, DefaultAlpha},
		{2, 1, DefaultAlpha}, {0, 1, math.NaN()}, {0, 1, 0}, {0, 1, 1}, {0, 1, 1e-10},
	} {
		_, err := BinomialInterval(test.events, test.n, test.alpha)
		require.Error(t, err)
	}
}
