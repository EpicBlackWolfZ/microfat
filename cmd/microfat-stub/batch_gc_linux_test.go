//go:build linux

package main

import (
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/require"
)

func TestLauncherBatchGCPrerequisite(t *testing.T) {
	const memory = 64 << 20
	for _, tc := range []struct {
		name     string
		limit    int64
		payload  int64
		base     []string
		dryRun   bool
		wantGC   string
		wantSkip bool
	}{
		{name: "unlimited", payload: 1, wantSkip: true},
		{name: "finite", limit: memory, payload: 1, wantGC: "off"},
		{name: "retained exceeds limit", limit: memory, payload: memory + 1, wantSkip: true},
		{name: "user finite limit", payload: 1, base: []string{"GOMEMLIMIT=32MiB"}, wantGC: "off"},
		{name: "user disables limit", limit: memory, payload: 1, base: []string{"GOMEMLIMIT=off"}, wantSkip: true},
		{name: "empty user limit", limit: memory, payload: 1, base: []string{"GOMEMLIMIT="}, wantSkip: true},
		{name: "invalid user limit", limit: memory, payload: 1, base: []string{"GOMEMLIMIT=invalid"}, wantSkip: true},
		{name: "explicit GC", payload: 1, base: []string{"GOGC=123"}, wantGC: "123"},
		{name: "explicit GC off", payload: 1, base: []string{"GOGC=off"}, wantGC: "off"},
		{name: "dry unlimited", payload: 1, dryRun: true, wantSkip: true},
		{name: "dry finite", limit: memory, payload: 1, dryRun: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(format.EnvGCProfile, string(cgroup.GCProfileBatchETL))
			t.Setenv(format.EnvAutotune, "1")
			t.Setenv(format.EnvDryRun, "0")
			if tc.dryRun {
				t.Setenv(format.EnvDryRun, "1")
			}
			oldRead := readCgroupLimitsFunc
			t.Cleanup(func() { readCgroupLimitsFunc = oldRead })
			readCgroupLimitsFunc = func() (cgroup.Limits, error) {
				return cgroup.Limits{CgroupVersion: cgroup.VersionV2, MemoryLimitBytes: tc.limit}, nil
			}
			base := append(append([]string{}, tc.base...), format.EnvCgroupGOGCSkippedReason+"=forged")
			entry := &format.VariantEntry{UncompressedSize: tc.payload}
			env, _ := buildAutoTunedEnviron("", base, entry, format.ExecModeMemfd, microarch.Info{}, microarch.PolicyResult{})
			values := make(map[string]string)
			for _, value := range env {
				key, val, _ := strings.Cut(value, "=")
				values[key] = val
			}
			require.Equal(t, tc.wantGC, values["GOGC"])
			if tc.wantSkip {
				require.Contains(t, values[format.EnvCgroupGOGCSkippedReason], "no finite effective memory ceiling")
			} else {
				require.Empty(t, values[format.EnvCgroupGOGCSkippedReason])
			}
			for _, preserved := range tc.base {
				require.Contains(t, env, preserved)
			}
		})
	}
}
