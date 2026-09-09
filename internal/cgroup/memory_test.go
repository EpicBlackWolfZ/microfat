package cgroup

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractionMemoryBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                            string
		payload, dict, decoder, reserve int64
		fails                           bool
	}{
		{"normal", 100, 20, 30, 40, false}, {"empty", 0, 0, 0, 0, false},
		{"negative", -1, 0, 0, 0, true}, {"overflow", math.MaxInt64, 1, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ExtractionMemory(tc.payload, tc.dict, tc.decoder, tc.reserve)
			if tc.fails {
				require.ErrorIs(t, err, ErrMemoryBudget)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.payload+tc.dict+tc.decoder+tc.reserve, got)
			}
		})
	}
	for _, tc := range []struct{ limit, retained, want int64 }{
		{100, 20, 80}, {100, 0, 100}, {100, 100, 0}, {100, 101, 0}, {100, -1, 0}, {0, 0, 0},
	} {
		require.Equal(t, tc.want, RetainedMemoryLimit(tc.limit, tc.retained))
	}
}

func TestTuningDeductsRetainedStorageOnce(t *testing.T) {
	t.Parallel()
	const ceiling = int64(1024 * 1024 * 1024)
	const payload = int64(128 * 1024 * 1024)
	limits := Limits{MemoryLimitBytes: ceiling, RetainedExecutableBytes: payload}
	first := ResolveTuningPlan(limits, "", DefaultMemoryRatio, DefaultMinHeadroomBytes)
	second := ResolveTuningPlan(limits, "", DefaultMemoryRatio, DefaultMinHeadroomBytes)
	want, ok := CalculateGOMEMLIMIT(ceiling-payload, DefaultMemoryRatio, DefaultMinHeadroomBytes)
	require.True(t, ok)
	require.Equal(t, want, first.GOMEMLIMITBytes)
	require.Equal(t, first, second)
	limits.RetainedExecutableBytes = ceiling
	require.Zero(t, ResolveTuningPlan(limits, "", DefaultMemoryRatio, DefaultMinHeadroomBytes).GOMEMLIMITBytes)
}
