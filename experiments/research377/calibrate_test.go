package research377

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalibrationKnownTruthAndBrokenDependence(t *testing.T) {
	t.Parallel()
	config := CalibrationConfig{Seed: 377, Samples: DefaultSamples, Replications: DefaultReplications}
	calibration, err := Calibrate(config)
	require.NoError(t, err)
	repeated, err := Calibrate(config)
	require.NoError(t, err)
	assert.Equal(t, calibration, repeated)
	require.Len(t, calibration.Scenarios, 4)
	for _, scenario := range calibration.Scenarios[:3] {
		assert.True(t, scenario.IID)
		assert.GreaterOrEqual(t, scenario.BaselineCoverage, 0.95)
		assert.GreaterOrEqual(t, scenario.CandidateCoverage, 0.95)
		assert.GreaterOrEqual(t, scenario.DifferenceCoverage, 0.95)
		assert.LessOrEqual(t, scenario.FalseDirection, 0.05)
	}
	assert.Greater(t, calibration.Scenarios[1].ImprovementPower, 0.90)
	// These broad deterministic checks expose severe undercoverage from pretending
	// within-burst launches are independent; they do not certify every random seed.
	dependent := calibration.Scenarios[3]
	assert.False(t, dependent.IID)
	assert.Less(t, dependent.BaselineCoverage, 0.60)
	assert.Less(t, dependent.CandidateCoverage, 0.60)
	assert.Less(t, dependent.DifferenceCoverage, 0.90)
	assert.LessOrEqual(t, dependent.CoverageMC.Low, dependent.DifferenceCoverage)
	assert.GreaterOrEqual(t, dependent.CoverageMC.High, dependent.DifferenceCoverage)
}

func TestCalibrationCapsAndShortPartialCluster(t *testing.T) {
	t.Parallel()
	for _, config := range []CalibrationConfig{
		{}, {Samples: MaximumSamples + 1, Replications: 1}, {Samples: 1, Replications: MaximumReplications + 1},
		{Samples: MaximumSamples, Replications: MaximumReplications},
	} {
		_, err := Calibrate(config)
		require.Error(t, err)
	}
	report, err := Calibrate(CalibrationConfig{Samples: 1, Replications: 1})
	require.NoError(t, err)
	assert.Len(t, report.Scenarios, 4)
}
