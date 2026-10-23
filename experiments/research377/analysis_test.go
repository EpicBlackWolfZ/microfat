package research377

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validInput() Input {
	return Input{
		Provenance: Provenance{
			SourceRevision: "fixture", GoVersion: "go1.27.3", Host: "synthetic",
			MeasurementKind: "synthetic-unit-test", DatasetSHA256: "fixture-no-external-data",
		},
		Plan: Plan{
			Estimand:        "probability independent process launch exceeds SLO or fails",
			IndependentUnit: "one isolated process launch", Cohort: "explicit-memfd cold launch",
			Confirmation: "fixed candidate selected using separate program families", IID: true, FixedSample: true,
			BaselineSamples: 1, CandidateSamples: 1, SLONanoseconds: 100,
			PracticalMargin: DefaultMargin, Alpha: DefaultAlpha, FamilySize: 1,
		},
		Baseline: []Observation{{UnitID: "b-1", Duration: 100}}, Candidate: []Observation{{UnitID: "c-1", Duration: 100}},
	}
}

func TestAnalyzeCountsEveryFailureAndBoundary(t *testing.T) {
	t.Parallel()
	input := validInput()
	input.Plan.BaselineSamples, input.Plan.CandidateSamples = 4, 4
	input.Baseline = []Observation{
		{UnitID: "a", Duration: 100}, {UnitID: "b", Duration: 101},
		{UnitID: "c", Failed: true}, {UnitID: "d", Failed: true, TimedOut: true},
	}
	input.Candidate = []Observation{
		{UnitID: "a"}, {UnitID: "b"}, {UnitID: "c"}, {UnitID: "d", TimedOut: true},
	}
	report, err := Analyze(input)
	require.NoError(t, err)
	assert.Equal(t, Method, report.Method)
	assert.Equal(t, SchemaVersion, report.SchemaVersion)
	assert.Equal(t, 3, report.Baseline.Violations)
	assert.Equal(t, 2, report.Baseline.Failures)
	assert.Equal(t, 1, report.Baseline.Timeouts)
	assert.Equal(t, 1, report.Candidate.Violations)
	assert.Equal(t, -0.5, report.Difference)
	assert.Equal(t, Inconclusive, report.Decision)
	assert.Equal(t, DefaultAlpha/2, report.ArmAlpha)
	assert.Equal(t, input.Plan, report.Plan)
	assert.Equal(t, input.Provenance, report.Provenance)
}

func TestAnalyzeMultiplicityAndUsefulEffect(t *testing.T) {
	t.Parallel()
	const count = 1000
	input := validInput()
	input.Plan.BaselineSamples, input.Plan.CandidateSamples = count, count
	input.Baseline, input.Candidate = make([]Observation, count), make([]Observation, count)
	for i := range count {
		id := fmt.Sprintf("launch-%d", i)
		input.Baseline[i] = Observation{UnitID: id, Duration: 101}
		input.Candidate[i] = Observation{UnitID: id, Duration: 1}
	}
	single, err := Analyze(input)
	require.NoError(t, err)
	assert.Equal(t, Improvement, single.Decision)
	input.Plan.FamilySize = 10
	family, err := Analyze(input)
	require.NoError(t, err)
	assert.LessOrEqual(t, family.Interval.Low, single.Interval.Low)
	assert.Greater(t, family.Interval.High, single.Interval.High)
	assert.Equal(t, DefaultAlpha/20, family.ArmAlpha)
}

func TestAnalyzeRejectsUnestablishedContract(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*Input){
		"missing revision":         func(v *Input) { v.Provenance.SourceRevision = "" },
		"missing Go":               func(v *Input) { v.Provenance.GoVersion = "" },
		"missing host":             func(v *Input) { v.Provenance.Host = "" },
		"missing measurement kind": func(v *Input) { v.Provenance.MeasurementKind = "" },
		"missing digest":           func(v *Input) { v.Provenance.DatasetSHA256 = "" },
		"missing estimand":         func(v *Input) { v.Plan.Estimand = "" },
		"missing unit":             func(v *Input) { v.Plan.IndependentUnit = "" },
		"missing cohort":           func(v *Input) { v.Plan.Cohort = "" },
		"missing split":            func(v *Input) { v.Plan.Confirmation = "" },
		"dependent units":          func(v *Input) { v.Plan.IID = false },
		"repeated peeking":         func(v *Input) { v.Plan.FixedSample = false },
		"negative SLO":             func(v *Input) { v.Plan.SLONanoseconds = -1 },
		"nan margin":               func(v *Input) { v.Plan.PracticalMargin = math.NaN() },
		"zero margin":              func(v *Input) { v.Plan.PracticalMargin = 0 },
		"unbounded margin":         func(v *Input) { v.Plan.PracticalMargin = math.Inf(1) },
		"bad alpha":                func(v *Input) { v.Plan.Alpha = math.NaN() },
		"zero family":              func(v *Input) { v.Plan.FamilySize = 0 },
		"family cap":               func(v *Input) { v.Plan.FamilySize = MaximumFamily + 1 },
		"tiny arm alpha":           func(v *Input) { v.Plan.Alpha = minimumAlpha },
		"missing observation":      func(v *Input) { v.Plan.BaselineSamples++ },
		"zero samples":             func(v *Input) { v.Plan.BaselineSamples = 0 },
		"sample cap":               func(v *Input) { v.Plan.BaselineSamples = MaximumSamples + 1 },
		"missing candidate":        func(v *Input) { v.Candidate = nil },
		"missing ID":               func(v *Input) { v.Baseline[0].UnitID = "" },
		"negative duration":        func(v *Input) { v.Candidate[0].Duration = -1 },
		"duplicate ID": func(v *Input) {
			v.Plan.BaselineSamples = 2
			v.Baseline = append(v.Baseline, v.Baseline[0])
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			input := validInput()
			mutate(&input)
			_, err := Analyze(input)
			require.Error(t, err)
		})
	}
}

func TestDecisionsRespectPracticalMargin(t *testing.T) {
	t.Parallel()
	const margin = 0.01
	for _, test := range []struct {
		interval Interval
		want     string
	}{
		{Interval{-0.10, -0.02}, Improvement}, {Interval{0.02, 0.10}, Regression},
		{Interval{-margin, margin}, Equivalent}, {Interval{-0.02, 0.02}, Inconclusive},
		{Interval{-0.10, -margin}, Inconclusive}, {Interval{margin, 0.10}, Inconclusive},
		{Interval{-0.005, -0.001}, Equivalent},
	} {
		assert.Equal(t, test.want, Decide(test.interval, margin))
	}
}
