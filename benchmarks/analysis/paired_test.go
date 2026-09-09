package analysis

import (
	"math"
	"sort"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPairedBootstrap(t *testing.T) {
	t.Parallel()
	const shift = 2.0
	pairs := make([]schema.Pair, MinimumPairs)
	for i := range pairs {
		pairs[i] = schema.Pair{Block: i, Baseline: float64(i + 1), Candidate: float64(i+1) + shift}
	}
	a, err := Paired(pairs, 1, BootstrapResamples)
	require.NoError(t, err)
	assert.Equal(t, shift, a.MedianDifference)
	assert.Equal(t, shift, a.Low)
	assert.Equal(t, shift, a.High)
	assert.True(t, a.Significant)
	b, err := Paired(pairs, 1, BootstrapResamples)
	require.NoError(t, err)
	assert.Equal(t, a, b)
	for i := range pairs {
		pairs[i].Candidate = pairs[i].Baseline
	}
	b, err = Paired(pairs, 1, BootstrapResamples)
	require.NoError(t, err)
	assert.False(t, b.Significant)
	assert.Zero(t, b.Low)
	pairs[0].Baseline = 0
	b, err = Paired(pairs, 1, BootstrapResamples)
	require.NoError(t, err)
	assert.Nil(t, b.RelativePercent)
	b, err = Paired(pairs[:1], 1, BootstrapResamples)
	require.NoError(t, err)
	assert.Equal(t, "descriptive", b.Status)
}

func TestPairedInvalidAndQuantile(t *testing.T) {
	t.Parallel()
	for name, pairs := range map[string][]schema.Pair{
		"empty": nil, "negative block": {{Block: -1}}, "duplicate": {{Block: 1}, {Block: 1}},
		"nan": {{Baseline: math.NaN()}}, "overflow": {{Baseline: -math.MaxFloat64, Candidate: math.MaxFloat64}},
	} {
		t.Run(name, func(t *testing.T) { _, err := Paired(pairs, 1, BootstrapResamples); require.Error(t, err) })
	}
	_, err := Paired([]schema.Pair{{}}, 1, 0)
	require.Error(t, err)
	assert.Zero(t, Quantile(nil, medianQuantile))
	assert.Equal(t, 1.0, Quantile([]float64{1}, medianQuantile))
	assert.Equal(t, 2.0, Quantile([]float64{1, 3}, medianQuantile))
	assert.Equal(t, math.MaxFloat64, Quantile([]float64{math.MaxFloat64, math.MaxFloat64}, medianQuantile))
}

func TestBootstrapAgainstExactEnumeration(t *testing.T) {
	t.Parallel()
	// Independent enumeration of all 6^6 resampled blocks. This reference uses sorted
	// middle elements rather than the implementation's quantile helper or random stream.
	differences := []float64{-8, -3, -1, 2, 6, 13}
	exact := make([]float64, 0, 46656)
	sample := make([]float64, len(differences))
	var enumerate func(int)
	enumerate = func(position int) {
		if position == len(sample) {
			ordered := append([]float64(nil), sample...)
			sort.Float64s(ordered)
			exact = append(exact, (ordered[2]+ordered[3])/2)
			return
		}
		for _, v := range differences {
			sample[position] = v
			enumerate(position + 1)
		}
	}
	enumerate(0)
	sort.Float64s(exact)
	pairs := make([]schema.Pair, len(differences))
	for i, v := range differences {
		pairs[i] = schema.Pair{Block: i, Baseline: 100, Candidate: 100 + v}
	}
	result, err := Paired(pairs, 42, maximumResamples)
	require.NoError(t, err)
	assert.Equal(t, exact[int(0.025*float64(len(exact)-1))], result.Low)
	assert.Equal(t, exact[int(0.975*float64(len(exact)-1))], result.High)
	assert.False(t, result.Significant)
}
