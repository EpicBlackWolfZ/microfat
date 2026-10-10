//go:build linux

package main

import (
	"strings"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLauncherCPUPolicy(t *testing.T) {
	const (
		memoryLimit      = 64 << 20
		cpuCount         = 4
		staticProcs      = "4"
		explicitProcs    = "8"
		explicitProcsEnv = "GOMAXPROCS=8"
		native           = string(cgroup.CPUPolicyNative)
	)
	for _, tc := range []struct {
		name          string
		policy        string
		base          []string
		dryRun        bool
		optOut        bool
		wantProcs     string
		wantProcsKey  bool
		wantCandidate bool
	}{
		{name: "default static", wantProcs: staticProcs, wantProcsKey: true, wantCandidate: true},
		{name: "explicit static", policy: "static", wantProcs: staticProcs, wantProcsKey: true, wantCandidate: true},
		{name: "invalid policy falls back to static", policy: "invalid", wantProcs: staticProcs, wantProcsKey: true, wantCandidate: true},
		{name: "native", policy: native},
		{name: "native case and whitespace", policy: "  NaTiVe  "},
		{name: "static preserves explicit procs", base: []string{explicitProcsEnv},
			wantProcs: explicitProcs, wantProcsKey: true, wantCandidate: true},
		{name: "static preserves empty procs", base: []string{"GOMAXPROCS="}, wantProcsKey: true, wantCandidate: true},
		{name: "native preserves explicit procs", policy: native, base: []string{explicitProcsEnv},
			wantProcs: explicitProcs, wantProcsKey: true},
		{name: "native preserves empty procs", policy: native, base: []string{"GOMAXPROCS="}, wantProcsKey: true},
		{name: "native deduplicates user procs", policy: native, base: []string{"GOMAXPROCS=2", explicitProcsEnv},
			wantProcs: explicitProcs, wantProcsKey: true},
		{name: "static dry run", dryRun: true, wantCandidate: true},
		{name: "native dry run", policy: native, dryRun: true},
		{name: "native dry run preserves explicit procs", policy: native, dryRun: true, base: []string{explicitProcsEnv},
			wantProcs: explicitProcs, wantProcsKey: true},
		{name: "static full opt out", optOut: true, wantCandidate: true},
		{name: "native full opt out", policy: native, optOut: true},
		{name: "native full opt out preserves explicit procs", policy: native, optOut: true, base: []string{explicitProcsEnv},
			wantProcs: explicitProcs, wantProcsKey: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(format.EnvCPUPolicy, tc.policy)
			t.Setenv(format.EnvAutotune, "1")
			t.Setenv(format.EnvDryRun, "0")
			t.Setenv(format.EnvGCProfile, string(cgroup.GCProfileBatchETL))
			if tc.optOut {
				t.Setenv(format.EnvAutotune, "0")
			}
			if tc.dryRun {
				t.Setenv(format.EnvDryRun, "1")
			}
			oldRead := readCgroupLimitsFunc
			t.Cleanup(func() { readCgroupLimitsFunc = oldRead })
			readCgroupLimitsFunc = func() (cgroup.Limits, error) {
				return cgroup.Limits{
					CgroupVersion: cgroup.VersionV2, MemoryLimitBytes: memoryLimit, CPUQuota: cpuCount, CPUs: cpuCount,
				}, nil
			}
			base := append([]string{}, tc.base...)
			base = append(base,
				format.EnvCPUPolicy+"=static",
				format.EnvCPUPolicy+"="+tc.policy,
				format.EnvCgroupGOMAXPROCS+"=999",
				format.EnvCgroupGOMAXPROCS+"=888",
			)
			entry := &format.VariantEntry{UncompressedSize: 1}
			env, limits := buildAutoTunedEnviron(
				"", base, entry, format.ExecModeMemfd, microarch.Info{}, microarch.PolicyResult{},
			)
			require.NotNil(t, limits)
			values := make(map[string]string)
			for _, value := range env {
				key, val, _ := strings.Cut(value, "=")
				_, exists := values[key]
				require.False(t, exists, "duplicate key %q", key)
				values[key] = val
			}
			procs, hasProcs := values["GOMAXPROCS"]
			assert.Equal(t, tc.wantProcsKey, hasProcs)
			assert.Equal(t, tc.wantProcs, procs)
			candidate, hasCandidate := values[format.EnvCgroupGOMAXPROCS]
			assert.Equal(t, tc.wantCandidate, hasCandidate)
			if hasCandidate {
				assert.Equal(t, staticProcs, candidate)
			}
			assert.Equal(t, tc.policy, values[format.EnvCPUPolicy], "CPU policy must reach the payload")
			assert.Equal(t, "4.00", values[format.EnvCgroupCPUs], "raw quota remains available")
			assert.NotEmpty(t, values[format.EnvCgroupGOMEMLIMIT], "memory candidate remains independent")
			assert.Equal(t, "off", values[format.EnvCgroupGOGC], "GC candidate remains independent")
			if tc.dryRun || tc.optOut {
				assert.NotContains(t, values, "GOMEMLIMIT")
				assert.NotContains(t, values, "GOGC")
			} else {
				assert.NotEmpty(t, values["GOMEMLIMIT"])
				assert.Equal(t, "off", values["GOGC"])
			}
		})
	}
}
