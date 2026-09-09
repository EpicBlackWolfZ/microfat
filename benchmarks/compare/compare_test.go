package compare

import (
	"math"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/internal/testfixture"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCounterbalancedSchedule(t *testing.T) {
	t.Parallel()
	configs := testfixture.Experiment().Configurations
	a, err := Schedule(configs, testfixture.Blocks, 1)
	require.NoError(t, err)
	b, err := Schedule(configs, testfixture.Blocks, 1)
	require.NoError(t, err)
	assert.Equal(t, a, b)
	assert.NotEqual(t, a[0].ConfigurationID, a[len(configs)].ConfigurationID)
	_, err = Schedule(nil, 1, 1)
	require.Error(t, err)
	_, err = Schedule(configs, 0, 1)
	require.Error(t, err)
	_, err = Schedule(configs, schema.MaxTrials, 1)
	require.Error(t, err)
	configs[0].ID = configs[1].ID
	_, err = Schedule(configs, 1, 1)
	require.Error(t, err)
}

func revisionFixture() *schema.ExperimentV2 {
	exp := testfixture.Experiment()
	for i := range exp.Configurations {
		id := "base-app"
		if i == 1 {
			id = "head-app"
		}
		old := exp.Configurations[i].ID
		exp.Configurations[i].ID = id
		for j := range exp.Schedule {
			if exp.Schedule[j].ConfigurationID == old {
				exp.Schedule[j].ConfigurationID, exp.Trials[j].ConfigurationID = id, id
			}
		}
	}
	return exp
}

func TestRevisionComparisons(t *testing.T) {
	t.Parallel()
	exp := revisionFixture()
	result, err := Revisions(exp)
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, "base-app", result[0].Baseline)
	assert.Equal(t, "head-app", result[0].Candidate)
	exp.Comparisons = result
	require.NoError(t, schema.ValidateV2(exp))
	for _, mutate := range []func(*schema.Comparison){
		func(c *schema.Comparison) { c.Pairs[0].Baseline++ },
		func(c *schema.Comparison) { c.Pairs = append(c.Pairs, c.Pairs[0]) },
		func(c *schema.Comparison) { c.Status = "invalid" },
	} {
		e := revisionFixture()
		e.Comparisons, err = Revisions(e)
		require.NoError(t, err)
		mutate(&e.Comparisons[0])
		require.Error(t, schema.ValidateV2(e))
	}
	_, err = Revisions(nil)
	require.Error(t, err)
	_, err = Revisions(testfixture.Experiment())
	require.Error(t, err)
	exp = revisionFixture()
	exp.Trials[0].Metrics["startup_ns"] = schema.Measured(1, "s", "startup", "fixture")
	_, err = Revisions(exp)
	require.Error(t, err)
	_, err = All(exp, "base-app")
	require.Error(t, err)
	exp.Trials[0].Metrics["startup_ns"] = schema.Measured(-math.MaxFloat64, "ns", "startup", "fixture")
	exp.Trials[1].Metrics["startup_ns"] = schema.Measured(math.MaxFloat64, "ns", "startup", "fixture")
	_, err = Metric(exp, "base-app", "head-app", "startup_ns")
	require.Error(t, err)
}

func TestMatchedMetricComparison(t *testing.T) {
	t.Parallel()
	exp := testfixture.Experiment()
	comparisons, err := All(exp, "base")
	require.NoError(t, err)
	require.Len(t, comparisons, 2)
	for _, comparison := range comparisons {
		assert.Equal(t, "complete", comparison.Status)
		assert.False(t, comparison.Significant)
		assert.Len(t, comparison.Pairs, testfixture.Blocks)
	}
	exp.Comparisons = comparisons
	require.NoError(t, schema.ValidateV2(exp))
	exp.Comparisons[0].Pairs[0].CandidateTrial = "missing"
	require.Error(t, schema.ValidateV2(exp))
	exp.Comparisons = nil
	exp.Trials[0].Outcome, exp.Trials[0].Reason = schema.OutcomeFailed, "test failure"
	comparisons, err = All(exp, "base")
	require.NoError(t, err)
	assert.Equal(t, "inconclusive", comparisons[0].Status)
	_, err = All(exp, "missing")
	require.Error(t, err)
	_, err = All(nil, "base")
	require.Error(t, err)
	exp = testfixture.Experiment()
	m := exp.Trials[0].Metrics["startup_ns"]
	m.Unit = "seconds"
	exp.Trials[0].Metrics["startup_ns"] = m
	_, err = Metric(exp, "base", "candidate", "startup_ns")
	require.Error(t, err)
	comparison, err := Metric(exp, "base", "candidate", "unknown")
	require.NoError(t, err)
	assert.Equal(t, "inconclusive", comparison.Status)
}
