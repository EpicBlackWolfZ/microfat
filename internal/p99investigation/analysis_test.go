package p99investigation

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExactMedianInterval(t *testing.T) {
	t.Parallel()
	differences := make([]float64, analysisBlocks)
	for i := range differences {
		differences[i] = float64(analysisBlocks - i)
	}
	interval, err := exactMedianInterval(differences, analysisFamilyAlpha/analysisFamilyComparisons)
	require.NoError(t, err)
	assert.Equal(t, 4, interval.LowerRank)
	assert.Equal(t, 17, interval.UpperRank)
	assert.Equal(t, 4.0, interval.Low)
	assert.Equal(t, 17.0, interval.High)
	assert.Equal(t, 20.0, differences[0], "analysis must not mutate observations")
	assert.Equal(t, 1.0, differences[len(differences)-1])
	// Independently count all equiprobable 20-sign sequences, without the
	// implementation's binomial recurrence or any normal approximation.
	outside := 0
	for signs := range 1 << analysisBlocks {
		positives := 0
		for position := range analysisBlocks {
			if signs&(1<<position) != 0 {
				positives++
			}
		}
		if positives < interval.LowerRank || positives > interval.UpperRank-1 {
			outside++
		}
	}
	expected := 1 - float64(outside)/float64(1<<analysisBlocks)
	assert.Equal(t, 0.9974231719970703, expected)
	assert.Equal(t, expected, interval.AchievedCoverage)
	assert.GreaterOrEqual(t, interval.AchievedCoverage, interval.NominalCoverage)
	// The next narrower symmetric interval would miss the requested coverage.
	assert.Less(t, 1-2*float64(6196)/float64(1<<analysisBlocks), interval.NominalCoverage)
	for i := range differences {
		differences[i] = 0
	}
	tied, err := exactMedianInterval(differences, interval.Alpha)
	require.NoError(t, err)
	assert.Zero(t, tied.Low)
	assert.Zero(t, tied.High)
	assert.Equal(t, interval.AchievedCoverage, tied.AchievedCoverage)
}

func TestExactMedianIntervalRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	for name, values := range map[string][]float64{
		"empty": nil, "too few for finite bounds": {1}, "too many": make([]float64, analysisBlocks+1),
		"nan": {math.NaN()}, "infinite": {math.Inf(1)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := exactMedianInterval(values, analysisFamilyAlpha/analysisFamilyComparisons)
			require.Error(t, err)
		})
	}
	for _, alpha := range []float64{-1, 0, 1, math.NaN(), math.Inf(1)} {
		_, err := exactMedianInterval(make([]float64, analysisBlocks), alpha)
		require.Error(t, err)
	}
}

func TestClassifyIntervalFixedMargins(t *testing.T) {
	t.Parallel()
	const margin = 10.0
	for _, test := range []struct {
		name       string
		low, high  float64
		wantStatus string
	}{
		{"positive regression", 11, 20, analysisRegression},
		{"negative improvement", -20, -11, analysisImprovement},
		{"equivalence including boundaries", -margin, margin, analysisEquivalent},
		{"positive within margin", 1, 9, analysisEquivalent},
		{"negative within margin", -9, -1, analysisEquivalent},
		{"positive touching margin", margin, 20, analysisInconclusive},
		{"negative touching margin", -20, -margin, analysisInconclusive},
		{"uncertain direction", -20, 20, analysisInconclusive},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.wantStatus, classifyInterval(MedianInterval{Low: test.low, High: test.high}, margin))
		})
	}
}

func TestAnalyzeOriginalScenariosAndControls(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		scenario, primary string
		margin            float64
	}{
		{analysisMemoryScenario, "fat-cache-cold", analysisMemoryMargin},
		{analysisMixedScenario, "fat-memfd", analysisMixedMargin},
	} {
		t.Run(test.scenario, func(t *testing.T) {
			t.Parallel()
			exp := analysisFixture()
			shiftHeadMetric(exp, test.primary, 2*test.margin)
			result, err := Analyze(exp, test.scenario)
			require.NoError(t, err)
			assert.True(t, result.Complete)
			assert.Equal(t, analysisRegression, result.Status)
			assert.Equal(t, test.margin, result.MarginSeconds)
			assert.Equal(t, test.primary, result.PrimaryConfiguration)
			assert.True(t, result.NativeControlsEquivalent)
			assert.True(t, result.FollowupWarranted)
			assert.Equal(t, "requires_independent_confirmation", result.AttributionStatus)
			assert.Contains(t, result.Attribution, "original hosted signals remain unresolved")
			require.Len(t, result.Comparisons, 5)
			for _, comparison := range result.Comparisons {
				assert.Len(t, comparison.Bootstrap.Pairs, analysisBlocks)
				assert.Equal(t, 10000, comparison.Bootstrap.Resamples)
				assert.Equal(t, "paired-median-percentile-bootstrap-pcg-v1", comparison.Bootstrap.Algorithm)
				assert.Equal(t, analysisBlocks/2, comparison.Drift.FirstTenBlocks.Pairs)
				assert.Equal(t, analysisBlocks/2, comparison.Drift.LastTenBlocks.Pairs)
				assert.Equal(t, analysisBlocks, comparison.Drift.BaseBeforeHead.Pairs)
				assert.Zero(t, comparison.Drift.HeadBeforeBase.Pairs)
				assert.Nil(t, comparison.Drift.HeadBeforeBase.MedianDifference)
				if comparison.Role == analysisDescriptiveRole {
					assert.Equal(t, analysisDescriptiveRole, comparison.Status)
					assert.Nil(t, comparison.Interval)
				} else {
					require.NotNil(t, comparison.Interval)
					assert.Equal(t, analysisBlocks, comparison.Interval.Pairs)
				}
			}
		})
	}
}

func TestAnalyzeCannotAttributeChangingNativeControls(t *testing.T) {
	t.Parallel()
	exp := analysisFixture()
	shiftHeadMetric(exp, "fat-memfd", 3*analysisMixedMargin)
	shiftHeadMetric(exp, "native-specialized", 2*analysisMixedMargin)
	result, err := Analyze(exp, analysisMixedScenario)
	require.NoError(t, err)
	assert.Equal(t, analysisRegression, result.Status)
	assert.False(t, result.NativeControlsEquivalent)
	assert.False(t, result.FollowupWarranted)
	assert.Equal(t, analysisInconclusive, result.AttributionStatus)
	assert.Contains(t, result.Attribution, "native controls did not both establish practical equivalence")
}

func TestAnalyzeRetainsDescriptiveOutcomesAndPairedEstimand(t *testing.T) {
	t.Parallel()
	exp := analysisFixture()
	shiftHeadMetric(exp, "fat-cache-warm", 1)
	shiftHeadMetric(exp, "fat-memfd", -2*analysisMixedMargin)
	result, err := Analyze(exp, analysisMixedScenario)
	require.NoError(t, err)
	assert.Equal(t, analysisImprovement, result.Status)
	for _, comparison := range result.Comparisons {
		if comparison.Configuration == "fat-cache-warm" {
			assert.Equal(t, analysisDescriptiveRole, comparison.Status)
			assert.Nil(t, comparison.Interval)
			assert.InDelta(t, 1, comparison.Bootstrap.MedianDifference, 1e-12)
		}
		if comparison.Role == analysisPrimaryRole {
			require.NotNil(t, comparison.Bootstrap.RelativePercent)
			shift := -2 * analysisMixedMargin
			expectedPairedPercent := (shift/0.010 + shift/0.011) / 2 * 100
			assert.InDelta(t, expectedPairedPercent, *comparison.Bootstrap.RelativePercent, 1e-12)
			assert.NotEqual(t, shift/0.0105*100, *comparison.Bootstrap.RelativePercent)
		}
	}
}

func TestAnalyzeIncompleteCohortsRemainInconclusive(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*schema.ExperimentV2){
		"incomplete marker": func(exp *schema.ExperimentV2) { exp.Complete = false },
		"missing trial": func(exp *schema.ExperimentV2) {
			exp.Complete = false
			exp.Trials = exp.Trials[1:]
		},
		"failed descriptive configuration": func(exp *schema.ExperimentV2) {
			exp.Trials[8].Outcome, exp.Trials[8].Reason = schema.OutcomeFailed, "fixed budget expired"
		},
		"unavailable primary p99": func(exp *schema.ExperimentV2) {
			exp.Trials[4].Metrics[analysisMetric] = schema.Unavailable("s", "steady_state", "fortio-histogram", "histogram missing")
		},
		"missing primary metric": func(exp *schema.ExperimentV2) { delete(exp.Trials[4].Metrics, analysisMetric) },
		"short schedule": func(exp *schema.ExperimentV2) {
			exp.Schedule = exp.Schedule[:len(exp.Schedule)-1]
			exp.Trials = exp.Trials[:len(exp.Trials)-1]
		},
		"outside block range": func(exp *schema.ExperimentV2) {
			exp.Schedule[0].Block = analysisBlocks
			exp.Trials[0].Block = analysisBlocks
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			exp := analysisFixture()
			shiftHeadMetric(exp, "fat-memfd", 3*analysisMixedMargin)
			change(exp)
			result, err := Analyze(exp, analysisMixedScenario)
			require.NoError(t, err)
			assert.False(t, result.Complete)
			assert.Equal(t, analysisInconclusive, result.Status)
			assert.False(t, result.NativeControlsEquivalent)
			assert.False(t, result.FollowupWarranted)
			assert.Equal(t, analysisInconclusive, result.AttributionStatus)
			assert.NotEmpty(t, result.Reasons)
			for _, comparison := range result.Comparisons {
				if comparison.Role != analysisDescriptiveRole {
					assert.Equal(t, analysisInconclusive, comparison.Status)
					assert.Nil(t, comparison.Interval)
					assert.NotEmpty(t, comparison.Reasons)
				}
			}
			if name == "failed descriptive configuration" {
				assert.Contains(t, strings.Join(result.Reasons, " "), "fixed budget expired")
			}
		})
	}
}

func TestDescriptiveDriftBlockAndOrderBoundaries(t *testing.T) {
	t.Parallel()
	exp := analysisFixture()
	for i := range exp.Trials {
		trial := &exp.Trials[i]
		if trial.ConfigurationID == "head-fat-memfd" {
			shift := 0.001
			if trial.Block >= analysisBlocks/2 {
				shift = 0.003
			}
			*trial.Metrics[analysisMetric].Value += shift
		}
		if trial.Block%2 == 1 {
			switch trial.ConfigurationID {
			case "base-fat-memfd":
				trial.Position++
				exp.Schedule[i].Position++
			case "head-fat-memfd":
				trial.Position--
				exp.Schedule[i].Position--
			}
		}
	}
	result, err := Analyze(exp, analysisMixedScenario)
	require.NoError(t, err)
	for _, comparison := range result.Comparisons {
		if comparison.Role != analysisPrimaryRole {
			continue
		}
		for name, want := range map[string]struct {
			group  DriftGroup
			median float64
		}{
			"first ten":  {comparison.Drift.FirstTenBlocks, 0.001},
			"last ten":   {comparison.Drift.LastTenBlocks, 0.003},
			"base first": {comparison.Drift.BaseBeforeHead, 0.002},
			"head first": {comparison.Drift.HeadBeforeBase, 0.002},
		} {
			t.Run(name, func(t *testing.T) {
				assert.Equal(t, analysisBlocks/2, want.group.Pairs)
				require.NotNil(t, want.group.MedianDifference)
				assert.InDelta(t, want.median, *want.group.MedianDifference, 1e-12)
			})
		}
	}
	empty := descriptiveDrift(exp, nil)
	assert.Equal(t, DescriptiveDrift{}, empty)
	outOfRange := descriptiveDrift(exp, []schema.Pair{{Block: analysisBlocks, Baseline: 1, Candidate: 2}})
	assert.Zero(t, outOfRange.FirstTenBlocks.Pairs)
	assert.Zero(t, outOfRange.LastTenBlocks.Pairs)
	assert.Nil(t, outOfRange.FirstTenBlocks.MedianDifference)
	assert.Nil(t, outOfRange.LastTenBlocks.MedianDifference)
}

func TestAnalyzePracticalEquivalenceAndEmptyDiagnostics(t *testing.T) {
	t.Parallel()
	exp := analysisFixture()
	result, err := Analyze(exp, analysisMixedScenario)
	require.NoError(t, err)
	assert.Equal(t, analysisEquivalent, result.Status)
	assert.Equal(t, analysisEquivalent, result.AttributionStatus)
	exp.Complete, exp.Trials = false, nil
	result, err = Analyze(exp, analysisMixedScenario)
	require.NoError(t, err)
	assert.Equal(t, analysisInconclusive, result.Status)
	for _, comparison := range result.Comparisons {
		assert.Empty(t, comparison.Bootstrap.Pairs)
		assert.Equal(t, 10000, comparison.Bootstrap.Resamples)
		assert.Equal(t, "paired-median-percentile-bootstrap-pcg-v1", comparison.Bootstrap.Algorithm)
		assert.Nil(t, comparison.Drift.FirstTenBlocks.MedianDifference)
	}
}

func TestAnalyzeRejectsInvalidScopeAndValues(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(*schema.Measurement){
		"unit":               func(m *schema.Measurement) { m.Unit = "ns" },
		"phase":              func(m *schema.Measurement) { m.Phase = "startup" },
		"measurement source": func(m *schema.Measurement) { m.Source = "pooled-requests" },
		"provenance":         func(m *schema.Measurement) { m.Kind = "model_assumption" },
		"negative":           func(m *schema.Measurement) { *m.Value = -1 },
		"nan":                func(m *schema.Measurement) { *m.Value = math.NaN() },
		"infinite":           func(m *schema.Measurement) { *m.Value = math.Inf(1) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			exp := analysisFixture()
			metric := exp.Trials[0].Metrics[analysisMetric]
			change(&metric)
			exp.Trials[0].Metrics[analysisMetric] = metric
			_, err := Analyze(exp, analysisMixedScenario)
			require.Error(t, err)
		})
	}
	_, err := Analyze(nil, analysisMixedScenario)
	require.Error(t, err)
	_, err = Analyze(analysisFixture(), "different-scenario")
	require.Error(t, err)
}

func analysisFixture() *schema.ExperimentV2 {
	config := []byte(`{}`)
	exp := &schema.ExperimentV2{SchemaVersion: schema.VersionV2, ID: "p99-fixture", CreatedAt: "2026-10-25T00:00:00Z",
		Config: config, ConfigSHA256: schema.Digest(config), Complete: true, Seed: 1,
		Artifacts: []schema.Artifact{{ID: "app", SHA256: strings.Repeat("a", 64), Bytes: 1}}}
	for _, suffix := range analysisSuffixes() {
		for _, prefix := range []string{"base-", "head-"} {
			exp.Configurations = append(exp.Configurations, schema.Configuration{ID: prefix + suffix, ArtifactID: "app"})
		}
	}
	for block := range analysisBlocks {
		for _, cfg := range exp.Configurations {
			scheduled := schema.ScheduledTrial{ID: fmt.Sprintf("trial-%d", len(exp.Schedule)), Block: block,
				Position: len(exp.Schedule), ConfigurationID: cfg.ID}
			exp.Schedule = append(exp.Schedule, scheduled)
			exp.Trials = append(exp.Trials, schema.ProcessTrial{ScheduledTrial: scheduled, Outcome: schema.OutcomeOK,
				StartedAt: exp.CreatedAt, Metrics: map[string]schema.Measurement{
					analysisMetric: schema.Measured(float64(block+1)/1000, "s", "steady_state", "fortio-histogram"),
				}})
		}
	}
	return exp
}

func shiftHeadMetric(exp *schema.ExperimentV2, suffix string, shift float64) {
	for _, trial := range exp.Trials {
		if trial.ConfigurationID == "head-"+suffix {
			*trial.Metrics[analysisMetric].Value += shift
		}
	}
}
