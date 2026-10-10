package runtimeinit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutoTune_CgroupV1CPUOnlyFilesystem(t *testing.T) {
	useZeroRetainedExecutableMemory(t)

	const (
		quotaCPUs   = 2.5
		plannedCPUs = 2
	)
	layouts := []struct {
		name       string
		controller string
	}{
		{name: "CPUController", controller: "cpu"},
		{name: "CombinedCPUController", controller: "cpu,cpuacct"},
		{name: "FlatMount"},
	}
	policies := []struct {
		name         string
		policy       CPUPolicy
		env          map[string]string
		wantPolicy   CPUPolicy
		wantCPUApply bool
	}{
		{name: "DefaultStatic", wantPolicy: CPUPolicyStatic, wantCPUApply: true},
		{
			name: "ExplicitGOMAXPROCS", env: map[string]string{"GOMAXPROCS": "7"},
			wantPolicy: CPUPolicyStatic,
		},
		{name: "NativeOption", policy: CPUPolicyNative, wantPolicy: CPUPolicyNative},
		{
			name: "NativeEnvironment", env: map[string]string{format.EnvCPUPolicy: string(CPUPolicyNative)},
			wantPolicy: CPUPolicyNative,
		},
	}

	// These tests replace package-level runtime hooks, so they must remain serial.
	for _, layout := range layouts {
		t.Run(layout.name, func(t *testing.T) {
			root := t.TempDir()
			cpuDir := filepath.Join(root, layout.controller)
			require.NoError(t, os.MkdirAll(cpuDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(cpuDir, "cpu.cfs_quota_us"), []byte("250000\n"), testFilePerm))
			require.NoError(t, os.WriteFile(filepath.Join(cpuDir, "cpu.cfs_period_us"), []byte("100000\n"), testFilePerm))
			procFile := filepath.Join(root, "proc_cgroup")
			require.NoError(t, os.WriteFile(procFile, []byte("2:cpu,cpuacct:/\n"), testFilePerm))

			for _, policy := range policies {
				t.Run(policy.name, func(t *testing.T) {
					withIsolatedEnv(t, policy.env, nil, nil, func(memLimit *int64, maxProcs, gogc *int, _ *bytes.Buffer) {
						readLimitsFromFunc = func(cgroupRoot string) (cgroup.Limits, error) {
							return cgroup.ReadLimitsCustom(cgroupRoot, procFile)
						}
						opts := []Option{WithCgroupRoot(root)}
						if policy.policy != "" {
							opts = append(opts, WithCPUPolicy(policy.policy))
						}
						res := AutoTune(opts...)

						require.Equal(t, cgroup.VersionV1, res.CgroupVersion)
						assert.InDelta(t, quotaCPUs, res.CPUQuota, 0)
						assert.Equal(t, policy.wantPolicy, res.CPUPolicy)
						assert.Equal(t, policy.wantCPUApply, res.MaxProcsApplied)
						if policy.wantCPUApply {
							assert.Equal(t, plannedCPUs, res.GOMAXPROCS)
							assert.Equal(t, plannedCPUs, *maxProcs)
						} else {
							assert.Zero(t, res.GOMAXPROCS)
							assert.Equal(t, -1, *maxProcs, "CPU setter must not be invoked")
						}
						assert.Zero(t, res.MemoryLimitBytes)
						assert.Zero(t, res.EffectiveMemoryLimitBytes)
						assert.Zero(t, res.GOMEMLIMIT)
						assert.False(t, res.MemLimitApplied)
						assert.Equal(t, int64(-1), *memLimit, "memory setter must not be invoked")
						assert.False(t, res.GOGCApplied)
						assert.Equal(t, -999, *gogc, "GC setter must not be invoked")
						assert.Empty(t, res.SkippedReason)
					})
				})
			}
		})
	}
}
