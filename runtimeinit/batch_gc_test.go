package runtimeinit

import (
	"bytes"
	"errors"
	"math"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/require"
)

func TestBatchGCEffectiveRuntimeCeiling(t *testing.T) {
	const memory = 64 << 20
	for _, tc := range []struct {
		name           string
		limit          int64
		retained       int64
		probeError     bool
		userLimit      string
		activeLimit    int64
		userGC         string
		explicitOption bool
		dryRun         bool
		wantOff        bool
		wantSkip       bool
	}{
		{name: "unlimited", wantSkip: true},
		{name: "retained exceeds budget", limit: memory, retained: memory + 1, wantSkip: true},
		{name: "retained probe fails", limit: memory, probeError: true, wantSkip: true},
		{name: "finite planned ceiling", limit: memory, wantOff: true},
		{name: "explicit finite ceiling", userLimit: "64MiB", activeLimit: memory, wantOff: true},
		{name: "active programmatic ceiling", activeLimit: memory, wantOff: true},
		{name: "user disabled ceiling", limit: memory, userLimit: "off", wantSkip: true},
		{name: "stale finite environment hint", limit: memory, userLimit: "64MiB", wantSkip: true},
		{name: "probe failure with active user ceiling", probeError: true, userLimit: "64MiB", activeLimit: memory, wantOff: true},
		{name: "explicit environment GC", userGC: "off"},
		{name: "explicit option GC", explicitOption: true, wantOff: true},
		{name: "dry run without ceiling", dryRun: true, wantSkip: true},
		{name: "dry run finite", limit: memory, dryRun: true, wantOff: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldProbe, oldCurrent := retainedExecutableMemoryFunc, currentMemoryLimitFunc
			t.Cleanup(func() { retainedExecutableMemoryFunc, currentMemoryLimitFunc = oldProbe, oldCurrent })
			retainedExecutableMemoryFunc = func() (int64, error) {
				if tc.probeError {
					return 0, errors.New("storage unavailable")
				}
				return tc.retained, nil
			}
			currentMemoryLimitFunc = func() int64 {
				if tc.activeLimit > 0 {
					return tc.activeLimit
				}
				return math.MaxInt64
			}
			env := map[string]string{"GOMEMLIMIT": tc.userLimit, "GOGC": tc.userGC, format.EnvGCProfile: "batch_etl"}
			limits := &cgroup.Limits{CgroupVersion: cgroup.VersionV2, MemoryLimitBytes: tc.limit, CPUs: 1}
			withIsolatedEnv(t, env, limits, nil, func(mem *int64, cpus *int, gc *int, _ *bytes.Buffer) {
				opts := []Option{WithDryRun(tc.dryRun)}
				if tc.explicitOption {
					opts = append(opts, WithGOGC(-1))
				}
				result := AutoTune(opts...)
				require.Equal(t, tc.wantOff && !tc.dryRun, result.GOGCApplied)
				if tc.wantOff && !tc.dryRun {
					require.Equal(t, -1, *gc)
				} else {
					require.Equal(t, -999, *gc, "GC setter must not run")
				}
				if tc.wantSkip {
					require.Contains(t, result.SkippedReason, "no finite effective memory ceiling")
				}
				if tc.dryRun {
					require.EqualValues(t, -1, *mem)
					require.Equal(t, -1, *cpus)
				} else {
					require.Equal(t, 1, *cpus, "GC skip must preserve CPU tuning")
				}
			})
		})
	}
}
