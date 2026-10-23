package main

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const modelInputFlag = "--input"

func validModelInput() input {
	return input{
		SchemaVersion: schemaVersion,
		Provenance: provenance{
			SourceRevision: "synthetic-fixture", GoVersion: "go1.27.3", CorpusSHA256: strings.Repeat("a", 64),
			HostClass: "synthetic", MeasurementKind: "full-process-exit", Scope: "synthetic",
		},
		Candidates: []candidate{
			{Name: "baseline", BundleBytes: 1000, PackNS: 100, ColdNS: []int64{100, 300}, WarmNS: []int64{10, 30}},
			{Name: "candidate", BundleBytes: 500, PackNS: 200, ColdNS: []int64{100, 100}, WarmNS: []int64{10, 10}},
		},
		Scenarios: []scenario{
			{Name: "mixed", WarmFraction: 0.5, Launches: 10, Transfers: 2, BandwidthBytesPerSecond: 1000, IncludePack: true},
		},
	}
}

func TestModelSeparatesAdditiveMeanAndEmpiricalTail(t *testing.T) {
	t.Parallel()
	in := validModelInput()
	out, err := analyze(in)
	require.NoError(t, err)
	assert.Equal(t, in.Provenance, out.Provenance)
	assert.Contains(t, out.Evidence, "descriptive")
	assert.NotEmpty(t, out.Warnings)
	require.Len(t, out.Results, 1)
	require.Len(t, out.Results[0].Costs, 2)
	base := out.Results[0].Costs[0]
	assert.Equal(t, "descriptive", base.Status)
	assert.Equal(t, 110.0, *base.MeanStartupNS)
	assert.Equal(t, int64(30), *base.MixtureP50NS)
	assert.Equal(t, int64(300), *base.MixtureP99NS)
	assert.Equal(t, 2e9, *base.TransferNS)
	assert.Equal(t, 2e9+100, *base.UpfrontNS)
	assert.Equal(t, 2e9+1200, *base.MeanLifecycleTotalNS)
	assert.Equal(t, in.Scenarios[0], out.Results[0].Scenario)
	require.Len(t, out.Results[0].BreakEven, 1)
	in.Scenarios[0].IncludePack = false
	noPack, err := analyze(in)
	require.NoError(t, err)
	assert.Equal(t, 2e9, *noPack.Results[0].Costs[0].UpfrontNS)
}

func TestMixtureQuantileWeightedAtomsOverlapAndTailBoundary(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		warm, cold []int64
		h, q       float64
		want       int64
	}{
		{"cold only", []int64{1}, []int64{100}, 0, p99, 100},
		{"warm only", []int64{1}, []int64{100}, 1, p99, 1},
		{"99 percent warm atom", []int64{1}, []int64{100}, p99, p99, 1},
		{"just below warm boundary", []int64{1}, []int64{100}, math.Nextafter(p99, 0), p99, 100},
		{"just above warm boundary", []int64{1}, []int64{100}, math.Nextafter(p99, 1), p99, 1},
		{"overlap atom", []int64{20, 10}, []int64{30, 10}, 0.5, 0.5, 10},
		{"overlap next atom", []int64{20, 10}, []int64{30, 10}, 0.5, 0.75, 20},
		{"above overlap atom", []int64{20, 10}, []int64{30, 10}, 0.5, math.Nextafter(0.75, 1), 30},
		{"unequal weights", []int64{10, 20}, []int64{15, 25}, 0.25, 0.5, 15},
		{"above unequal weights", []int64{10, 20}, []int64{15, 25}, 0.25, math.Nextafter(0.5, 1), 20},
		{"fractional atom weight just below", []int64{1, 1000, 1000}, []int64{1, 1, 1, 1, 1000, 1000, 1000},
			math.Nextafter(0.3, 0), 0.5, 1},
		{"fractional atom weight just above", []int64{1, 1000, 1000}, []int64{1, 1, 1, 1, 1000, 1000, 1000},
			math.Nextafter(0.3, 1), 0.5, 1000},
		{"warm slower than cold", []int64{100}, []int64{1}, p99, p99, 100},
		{"fractional atom survives binary64 sum rounding", []int64{1, 1000, 1000},
			[]int64{1, 1, 1, 1, 1000, 1000, 1000}, 0.3, 0.5, 1},
		{"fallback maximum", []int64{2}, []int64{1}, 0.5, 1.1, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			warmBefore, coldBefore := slices.Clone(test.warm), slices.Clone(test.cold)
			assert.Equal(t, test.want, mixtureQuantile(test.warm, test.cold, test.h, test.q))
			assert.Equal(t, warmBefore, test.warm)
			assert.Equal(t, coldBefore, test.cold)
		})
	}
	// Completing an atom's mass must return h exactly, even when multiplying
	// h*n before dividing by n would round below the q=.99 boundary.
	for _, count := range []int{1, 2, 3, 7, 10, 17} {
		warm := make([]int64, count)
		for i := range warm {
			warm[i] = 10
		}
		assert.Equal(t, int64(10), mixtureQuantile(warm, []int64{100}, p99, p99), "warm count=%d", count)
	}
}

func TestFailuresPreservedAndNeverConditionedAway(t *testing.T) {
	t.Parallel()
	in := validModelInput()
	in.Candidates[1].ColdNS = nil
	in.Candidates[1].ColdFailures = 2
	in.Candidates[1].WarmFailures = 1
	in.Scenarios[0].WarmFraction = 1
	out, err := analyze(in)
	require.NoError(t, err)
	failed := out.Results[0].Costs[1]
	assert.Equal(t, 3, failed.Failures)
	assert.Contains(t, failed.Status, "incomplete-failures")
	assert.Nil(t, failed.MeanStartupNS)
	assert.Nil(t, failed.MixtureP50NS)
	assert.Nil(t, failed.MixtureP99NS)
	assert.Nil(t, failed.MeanLifecycleTotalNS)
	assert.Nil(t, failed.TransferNS)
	assert.Nil(t, failed.UpfrontNS)
	assert.Empty(t, out.Results[0].BreakEven)
	// Even when the scenario assigns zero weight to cold state, preserve that
	// candidate's observed cold failures and withhold a complete prediction.
	assert.Equal(t, int64(500), failed.BundleBytes)
}

func TestBreakEvenBothDirectionsAndDominance(t *testing.T) {
	t.Parallel()
	makeCost := func(name string, upfront, mean float64) cost {
		return cost{Candidate: name, UpfrontNS: &upfront, MeanStartupNS: &mean}
	}
	base := makeCost("base", 100, 10)
	for _, test := range []struct {
		name      string
		up, mean  float64
		direction string
		cross     *float64
	}{
		{"equal", 100, 10, "equal", nil},
		{"candidate dominates", 50, 5, "candidate-always-cheaper", nil},
		{"same startup candidate", 50, 10, "candidate-always-cheaper", nil},
		{"baseline dominates", 200, 20, "baseline-always-cheaper", nil},
		{"same startup baseline", 200, 10, "baseline-always-cheaper", nil},
		{"candidate wins after amortizing", 200, 8, "candidate-cheaper-above-crossing", new(50.0)},
		{"candidate loses after launches", 50, 11, "candidate-cheaper-below-crossing", new(50.0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cross := breakEven(base, makeCost("other", test.up, test.mean))
			assert.Equal(t, "base", cross.Baseline)
			assert.Equal(t, "other", cross.Candidate)
			assert.Equal(t, test.direction, cross.Direction)
			assert.Equal(t, test.cross, cross.CrossingLaunches)
		})
	}
}

func TestInvalidEvidenceAndObservationBounds(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*input){
		"schema":                 func(v *input) { v.SchemaVersion++ },
		"no candidates":          func(v *input) { v.Candidates = nil },
		"candidate cap":          func(v *input) { v.Candidates = make([]candidate, maxCandidates+1) },
		"no scenarios":           func(v *input) { v.Scenarios = nil },
		"scenario cap":           func(v *input) { v.Scenarios = make([]scenario, maxScenarios+1) },
		"revision":               func(v *input) { v.Provenance.SourceRevision = "" },
		"toolchain":              func(v *input) { v.Provenance.GoVersion = "go1.26" },
		"hash length":            func(v *input) { v.Provenance.CorpusSHA256 = "a" },
		"hash hex":               func(v *input) { v.Provenance.CorpusSHA256 = strings.Repeat("z", 64) },
		"host":                   func(v *input) { v.Provenance.HostClass = "" },
		"scope":                  func(v *input) { v.Provenance.Scope = "unknown" },
		"measurement":            func(v *input) { v.Provenance.MeasurementKind = "" },
		"empty candidate name":   func(v *input) { v.Candidates[0].Name = "" },
		"duplicate candidate":    func(v *input) { v.Candidates[1].Name = v.Candidates[0].Name },
		"zero bundle":            func(v *input) { v.Candidates[0].BundleBytes = 0 },
		"bundle cap":             func(v *input) { v.Candidates[0].BundleBytes = maxBytes + 1 },
		"negative pack":          func(v *input) { v.Candidates[0].PackNS = -1 },
		"pack cap":               func(v *input) { v.Candidates[0].PackNS = maxDurationNS + 1 },
		"negative cold failures": func(v *input) { v.Candidates[0].ColdFailures = -1 },
		"negative warm failures": func(v *input) { v.Candidates[0].WarmFailures = -1 },
		"cold failure cap":       func(v *input) { v.Candidates[0].ColdFailures = maxSamples + 1 },
		"warm failure cap":       func(v *input) { v.Candidates[0].WarmFailures = maxSamples + 1 },
		"no cold observations":   func(v *input) { v.Candidates[0].ColdNS = nil },
		"no warm observations":   func(v *input) { v.Candidates[0].WarmNS = nil },
		"zero cold sample":       func(v *input) { v.Candidates[0].ColdNS[0] = 0 },
		"negative warm sample":   func(v *input) { v.Candidates[0].WarmNS[0] = -1 },
		"duration cap":           func(v *input) { v.Candidates[0].WarmNS[0] = maxDurationNS + 1 },
		"total observation cap":  func(v *input) { v.Candidates[0].ColdFailures = maxSamples },
		"empty scenario":         func(v *input) { v.Scenarios[0].Name = "" },
		"duplicate scenario":     func(v *input) { v.Scenarios = append(v.Scenarios, v.Scenarios[0]) },
		"nan mixture":            func(v *input) { v.Scenarios[0].WarmFraction = math.NaN() },
		"infinite bandwidth":     func(v *input) { v.Scenarios[0].BandwidthBytesPerSecond = math.Inf(1) },
		"negative warm fraction": func(v *input) { v.Scenarios[0].WarmFraction = -0.1 },
		"excess warm fraction":   func(v *input) { v.Scenarios[0].WarmFraction = 1.1 },
		"zero bandwidth":         func(v *input) { v.Scenarios[0].BandwidthBytesPerSecond = 0 },
		"negative launches":      func(v *input) { v.Scenarios[0].Launches = -1 },
		"launch cap":             func(v *input) { v.Scenarios[0].Launches = maxLaunches + 1 },
		"negative transfers":     func(v *input) { v.Scenarios[0].Transfers = -1 },
		"transfer cap":           func(v *input) { v.Scenarios[0].Transfers = maxLaunches + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			in := validModelInput()
			mutate(&in)
			_, err := analyze(in)
			require.Error(t, err)
		})
	}
}

func TestCLIAndBoundedInputFailures(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	encoded, err := json.Marshal(validModelInput())
	require.NoError(t, err)
	path := filepath.Join(directory, "input.json")
	require.NoError(t, os.WriteFile(path, encoded, 0o600))
	var output, diagnostics bytes.Buffer
	require.NoError(t, run([]string{modelInputFlag, path}, &output, &diagnostics))
	var decoded report
	require.NoError(t, json.Unmarshal(output.Bytes(), &decoded))
	assert.Len(t, decoded.Results, 1)
	assert.Empty(t, diagnostics.String())
	for _, args := range [][]string{
		{}, {"--unknown"}, {modelInputFlag, path, "extra"}, {modelInputFlag, filepath.Join(directory, "missing")},
		{modelInputFlag, directory},
	} {
		require.Error(t, run(args, &bytes.Buffer{}, &bytes.Buffer{}))
	}
	for _, data := range []string{`{`, `{}`, `{"unknown":true}`, `{"schema_version":1,"schema_version":1}`, `{} {}`} {
		require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
		require.Error(t, run([]string{modelInputFlag, path}, &bytes.Buffer{}, &bytes.Buffer{}))
	}
	file, err := os.Create(filepath.Join(directory, "oversized"))
	require.NoError(t, err)
	require.NoError(t, file.Truncate(maxInputBytes+1))
	require.NoError(t, file.Close())
	_, err = readInput(file.Name())
	require.Error(t, err)
	require.NoError(t, os.WriteFile(path, encoded, 0o600))
	require.Error(t, run([]string{modelInputFlag, path}, modelBrokenWriter{}, &bytes.Buffer{}))
}

func TestMainSuccessfulEntrypoint(t *testing.T) {
	// Exercise the actual entry point without an error/os.Exit path. Global
	// process descriptors make this test intentionally nonparallel.
	directory := t.TempDir()
	encoded, err := json.Marshal(validModelInput())
	require.NoError(t, err)
	path := filepath.Join(directory, "input.json")
	require.NoError(t, os.WriteFile(path, encoded, 0o600))
	output, err := os.Create(filepath.Join(directory, "output.json"))
	require.NoError(t, err)
	originalArgs, originalStdout := os.Args, os.Stdout
	t.Cleanup(func() {
		os.Args, os.Stdout = originalArgs, originalStdout
		_ = output.Close()
	})
	os.Args, os.Stdout = []string{"research376", modelInputFlag, path}, output
	main()
	data, err := os.ReadFile(output.Name())
	require.NoError(t, err)
	var decoded report
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, schemaVersion, decoded.SchemaVersion)
}

type modelBrokenWriter struct{}

func (modelBrokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken writer") }
