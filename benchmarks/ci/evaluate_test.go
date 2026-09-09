package ci

import (
	"math"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCoarseRegressionPolicy(t *testing.T) {
	t.Parallel()
	const change = 20.0
	percent := change
	comparisons := []schema.Comparison{{Candidate: "head", Metric: "startup_ns", Status: "complete",
		MedianDifference: change, RelativePercent: &percent},
		{Candidate: "head", Metric: "artifact_bytes", Status: "complete", MedianDifference: change, RelativePercent: &percent},
		{Candidate: "head", Metric: "throughput_qps", Status: "complete"},
		{Candidate: "head", Metric: "startup_ns", Status: "inconclusive", Reason: "missing pairs"}}
	policy := DefaultPolicy()
	decisions, failed, err := Evaluate(comparisons, policy)
	require.NoError(t, err)
	assert.True(t, failed)
	assert.Equal(t, "report_only", decisions[0].Status)
	assert.Equal(t, "regression", decisions[1].Status)
	assert.Equal(t, "report_only", decisions[2].Status)
	assert.Equal(t, "inconclusive", decisions[3].Status)
	assert.Contains(t, Summary(decisions), "missing pairs")
	policy.Calibrated, policy.StartupFloorNS = true, change/2
	decisions, _, err = Evaluate(comparisons[:1], policy)
	require.NoError(t, err)
	assert.Equal(t, "regression", decisions[0].Status)
	policy.StartupFloorNS = change
	decisions, failed, err = Evaluate(comparisons[:1], policy)
	require.NoError(t, err)
	assert.False(t, failed)
	assert.Equal(t, "pass", decisions[0].Status)
	comparisons[0].RelativePercent = nil
	decisions, _, err = Evaluate(comparisons[:1], policy)
	require.NoError(t, err)
	assert.Equal(t, "inconclusive", decisions[0].Status)
	for _, invalid := range []Policy{{}, {StartupPercent: math.NaN()}, {StartupPercent: 1, SizePercent: 1, Calibrated: true}} {
		_, _, err := Evaluate(nil, invalid)
		require.Error(t, err)
	}
}
