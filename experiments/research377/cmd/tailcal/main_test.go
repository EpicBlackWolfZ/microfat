package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/experiments/research377"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCalibrationAndAnalyze(t *testing.T) {
	t.Parallel()
	var output, diagnostics bytes.Buffer
	assert.Zero(t, run([]string{"--samples=10", "--replications=2", "--seed=1"}, nil, &output, &diagnostics))
	var calibration research377.Calibration
	require.NoError(t, json.Unmarshal(output.Bytes(), &calibration))
	assert.Equal(t, uint64(1), calibration.Config.Seed)
	assert.Len(t, calibration.Scenarios, 4)
	output.Reset()
	input := `{"provenance":{"source_revision":"fixture","go_version":"go1.27.3","host":"synthetic",
	"measurement_kind":"synthetic-fixture","dataset_sha256":"fixture-no-external-data"},
	"plan":{"estimand":"SLO failure risk","independent_unit":"isolated launch","cohort":"memfd",
	"confirmation":"held out","iid_within_each_arm":true,"fixed_sample":true,"baseline_samples":1,"candidate_samples":1,
	"slo_ns":100,"practical_margin_probability":0.01,"family_alpha":0.05,"comparison_family_size":1},
	"baseline":[{"unit_id":"b","duration_ns":100}],"candidate":[{"unit_id":"c","failed":true}]}`
	assert.Zero(t, run([]string{"--mode=analyze"}, strings.NewReader(input), &output, &diagnostics))
	var report research377.Report
	require.NoError(t, json.Unmarshal(output.Bytes(), &report))
	assert.Equal(t, 1, report.Candidate.Failures)
	assert.Equal(t, research377.Inconclusive, report.Decision)
	assert.Empty(t, diagnostics.String())
}

func TestRunFailurePaths(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--unknown"}, {"unexpected"}, {"--mode=other"}, {"--replications=0"}, {"--mode=analyze"},
	} {
		var output, diagnostics bytes.Buffer
		assert.Equal(t, exitFailure, run(args, strings.NewReader("{}"), &output, &diagnostics))
		assert.Empty(t, output.String())
		assert.NotEmpty(t, diagnostics.String())
	}
	var diagnostics bytes.Buffer
	assert.Equal(t, exitFailure, run([]string{"--mode=analyze"}, strings.NewReader("{"), &bytes.Buffer{}, &diagnostics))
	diagnostics.Reset()
	assert.Equal(t, exitFailure, run([]string{"--samples=1", "--replications=1"}, nil, brokenIO{}, &diagnostics))
	assert.Contains(t, diagnostics.String(), "broken")
}

func TestBoundedStrictJSON(t *testing.T) {
	t.Parallel()
	for name, input := range map[string]io.Reader{
		"unknown field":      strings.NewReader(`{"unexpected":1}`),
		"duplicate field":    strings.NewReader(`{"plan":{},"plan":{}}`),
		"syntax":             strings.NewReader(`{`),
		"trailing value":     strings.NewReader(`{} {}`),
		"trailing malformed": strings.NewReader(`{} garbage`),
		"reader failure":     brokenIO{},
		"over cap":           strings.NewReader(strings.Repeat(" ", 101)),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeInput(input, 100)
			require.Error(t, err)
		})
	}
	_, err := decodeInput(strings.NewReader(`{}`), 2)
	require.NoError(t, err)
}

type brokenIO struct{}

func (brokenIO) Read([]byte) (int, error)  { return 0, errors.New("broken reader") }
func (brokenIO) Write([]byte) (int, error) { return 0, errors.New("broken writer") }
