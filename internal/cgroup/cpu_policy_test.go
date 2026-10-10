package cgroup

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveCPUPolicy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		env      string
		fallback CPUPolicy
		want     CPUPolicy
	}{
		{name: "default", want: CPUPolicyStatic},
		{name: "option native", fallback: CPUPolicyNative, want: CPUPolicyNative},
		{name: "environment native", env: "native", fallback: CPUPolicyStatic, want: CPUPolicyNative},
		{name: "environment static", env: "static", fallback: CPUPolicyNative, want: CPUPolicyStatic},
		{name: "normalized environment", env: " Native \t", want: CPUPolicyNative},
		{name: "invalid environment preserves option", env: "invalid", fallback: CPUPolicyNative, want: CPUPolicyNative},
		{name: "invalid fallback defaults", fallback: "invalid", want: CPUPolicyStatic},
		{name: "normalized fallback", fallback: " NATIVE ", want: CPUPolicyNative},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ResolveCPUPolicy(tc.env, tc.fallback))
		})
	}
}

func TestTuningPlanCPUPolicy(t *testing.T) {
	t.Parallel()
	const (
		memoryCeiling = 512 * 1024 * 1024
		quota         = 3.5
		staticCPUs    = 3
	)
	for _, policy := range []CPUPolicy{CPUPolicyStatic, CPUPolicyNative} {
		t.Run(string(policy), func(t *testing.T) {
			t.Parallel()
			plan := ResolveTuningPlanWithProfile(Limits{
				CgroupVersion: VersionV2, MemoryLimitBytes: memoryCeiling, CPUQuota: quota,
			}, "", DefaultMemoryRatio, DefaultMinHeadroomBytes, GCProfileLatencyCritical, 0)
			original := plan
			plan.ApplyCPUPolicy(policy)
			if policy == CPUPolicyNative {
				assert.Zero(t, plan.GOMAXPROCS)
				assert.Empty(t, plan.GOMAXPROCSStr)
				original.GOMAXPROCS = 0
				original.GOMAXPROCSStr = ""
			} else {
				assert.Equal(t, staticCPUs, plan.GOMAXPROCS)
			}
			assert.Equal(t, original, plan, "memory, GC, and diagnostic fields must be preserved")
		})
	}
}
