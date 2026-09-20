package cgroup

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBatchGCRequiresFiniteCeiling(t *testing.T) {
	t.Parallel()
	const memory = 64 << 20
	for _, tc := range []struct {
		name   string
		limits Limits
		apply  bool
	}{
		{"unlimited", Limits{CgroupVersion: VersionV2}, false},
		{"unavailable", Limits{}, false},
		{"retained exhausts ceiling", Limits{MemoryLimitBytes: memory, RetainedExecutableBytes: memory + 1}, false},
		{"finite", Limits{MemoryLimitBytes: memory}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plan := ResolveTuningPlanWithProfile(tc.limits, "", DefaultMemoryRatio, DefaultMinHeadroomBytes, GCProfileBatchETL, 0)
			require.Equal(t, tc.apply, plan.GOGCApplied)
			if tc.apply {
				require.Equal(t, "off", plan.GOGCStr)
				require.Empty(t, plan.GOGCSkippedReason)
			} else {
				require.Empty(t, plan.GOGCStr)
				require.Contains(t, plan.GOGCSkippedReason, "no finite effective memory ceiling")
			}
		})
	}
	plan := TuningPlan{GCProfile: GCProfileBatchETL}
	plan.ResolveBatchGOGC(memory)
	require.True(t, plan.GOGCApplied)
	plan.ResolveBatchGOGC(math.MaxInt64)
	require.False(t, plan.GOGCApplied)
	plan = TuningPlan{GCProfile: GCProfileLatencyCritical, GOGC: DefaultLatencyCriticalGOGC, GOGCApplied: true}
	plan.ResolveBatchGOGC(0)
	require.True(t, plan.GOGCApplied)
	require.Equal(t, DefaultLatencyCriticalGOGC, plan.GOGC)
}

func TestRuntimeMemoryLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		value string
		want  int64
	}{
		{"", 0}, {"off", 0}, {"0", 0}, {"-1", 0}, {"1.5MiB", 0}, {"1MB", 0}, {"1PiB", 0},
		{" 42B", 0}, {"B", 0}, {"1iB", 0}, {"9223372036854775807", 0}, {"9223372036854775808", 0},
		{"99999999TiB", 0}, {"42", 42}, {"42B", 42}, {"2KiB", 2 << 10}, {"2MiB", 2 << 20},
		{"2GiB", 2 << 30}, {"2TiB", 2 << 40}, {"9223372036854775806", math.MaxInt64 - 1},
		{"+64MiB", 64 << 20},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, RuntimeMemoryLimit(tc.value))
		})
	}
}
