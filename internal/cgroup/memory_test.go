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
		{"normal", 100, 20, 30, 40, false},
		{"empty", 0, 0, 0, 0, false},
		{"maximum_exact_sum", math.MaxInt64 - 3, 1, 1, 1, false},
		{"negative", -1, 0, 0, 0, true},
		{"negative_after_valid_prefix", math.MaxInt64, 0, 0, -1, true},
		{"overflow", math.MaxInt64, 1, 0, 0, true},
		{"overflow_on_final_addition", math.MaxInt64 - 3, 1, 1, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ExtractionMemory(tc.payload, tc.dict, tc.decoder, tc.reserve)
			if tc.fails {
				require.ErrorIs(t, err, ErrMemoryBudget)
				require.Zero(t, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.payload+tc.dict+tc.decoder+tc.reserve, got)
			}
		})
	}
	for _, tc := range []struct{ limit, retained, want int64 }{
		{100, 20, 80}, {100, 0, 100}, {100, 100, 0}, {100, 101, 0}, {100, -1, 0}, {0, 0, 0},
		{math.MaxInt64, 0, math.MaxInt64}, {math.MaxInt64, math.MaxInt64 - 1, 1}, {math.MinInt64, math.MaxInt64, 0},
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

func TestZstdDecoderMemoryBounds(t *testing.T) {
	t.Parallel()

	const (
		win1KiB   int64 = 1024
		win4MiB   int64 = 4 * 1024 * 1024
		win8MiB   int64 = 8 * 1024 * 1024
		win128MiB int64 = 128 * 1024 * 1024
		ovh4MiB   int64 = 4 * 1024 * 1024
		max512MiB int64 = 512 * 1024 * 1024
	)

	for _, tc := range []struct {
		name                                  string
		window, maxWindow, overhead, maxLimit int64
		want                                  int64
		fails                                 bool
	}{
		{"1kib_window", win1KiB, win128MiB, ovh4MiB, max512MiB, 2*win1KiB + ovh4MiB, false},
		{"4mib_window", win4MiB, win128MiB, ovh4MiB, max512MiB, 2*win4MiB + ovh4MiB, false},
		{"8mib_window", win8MiB, win128MiB, ovh4MiB, max512MiB, 2*win8MiB + ovh4MiB, false},
		{"exact_max_window", win128MiB, win128MiB, ovh4MiB, max512MiB, 2*win128MiB + ovh4MiB, false},
		{"window_above_max", win128MiB + 1, win128MiB, ovh4MiB, max512MiB, 0, true},
		{"exact_max_decoder", win8MiB, win128MiB, 0, 2 * win8MiB, 2 * win8MiB, false},
		{"exceeds_max_decoder", win8MiB, win128MiB, 1, 2 * win8MiB, 0, true},
		{"zero_window", 0, win128MiB, ovh4MiB, max512MiB, 0, true},
		{"negative_window", -1, win128MiB, ovh4MiB, max512MiB, 0, true},
		{"zero_max_window", win1KiB, 0, ovh4MiB, max512MiB, 0, true},
		{"negative_overhead", win1KiB, win128MiB, -1, max512MiB, 0, true},
		{"zero_max_decoder", win1KiB, win128MiB, ovh4MiB, 0, 0, true},
		{
			"doubling_overflow",
			math.MaxInt64/2 + 1,
			math.MaxInt64,
			0,
			math.MaxInt64,
			0,
			true,
		},
		{
			"overhead_overflow",
			math.MaxInt64 / 2,
			math.MaxInt64,
			math.MaxInt64 - (math.MaxInt64/2)*2 + 1,
			math.MaxInt64,
			0,
			true,
		},
		{
			"max_int64_exact",
			math.MaxInt64 / 2,
			math.MaxInt64,
			math.MaxInt64 - (math.MaxInt64/2)*2,
			math.MaxInt64,
			math.MaxInt64,
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ZstdDecoderMemory(tc.window, tc.maxWindow, tc.overhead, tc.maxLimit)
			if tc.fails {
				require.ErrorIs(t, err, ErrMemoryBudget)
				require.Zero(t, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			}
		})
	}
}
