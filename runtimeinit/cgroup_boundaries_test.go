package runtimeinit

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const boundaryMemoryEnvironment = "GOMEMLIMIT"

func TestAutoTuneFilesystemBoundaries(t *testing.T) {
	useZeroRetainedExecutableMemory(t)
	const memoryCeiling = int64(536870912)
	cases := []struct {
		name       string
		files      map[string]string
		proc       string
		wantMemory int64
		wantCPU    int
		constraint string
	}{
		{
			name: "direct memory with unmounted CPU", proc: "3:memory:/app\n4:cpu,cpuacct:/app\n",
			files:      map[string]string{"memory.limit_in_bytes": "-1", "app/memory.limit_in_bytes": "536870912"},
			wantMemory: memoryCeiling, wantCPU: -1, constraint: cgroup.LimitConstraintMax,
		},
		{
			name: "direct CPU with unmounted memory", proc: "3:memory:/app\n4:cpu,cpuacct:/app\n",
			files: map[string]string{
				"cpu.cfs_quota_us": "-1", "cpu.cfs_period_us": "100000",
				"app/cpu.cfs_quota_us": "200000", "app/cpu.cfs_period_us": "100000",
			},
			wantMemory: -1, wantCPU: 2,
		},
		{
			name: "zero high retains hard ceiling and CPU", proc: "0::/app\n",
			files: map[string]string{
				"cgroup.controllers": "memory cpu", "app/memory.max": "536870912",
				"app/memory.high": "0", "app/cpu.max": "200000 100000",
			},
			wantCPU: 2, constraint: cgroup.LimitConstraintHigh,
		},
		{
			name: "zero hard ceiling retains CPU", proc: "0::/app\n",
			files: map[string]string{
				"cgroup.controllers": "memory cpu", "app/memory.max": "0",
				"app/memory.high": "max", "app/cpu.max": "200000 100000",
			},
			wantCPU: 2, constraint: cgroup.LimitConstraintMax,
		},
		{
			name: "exact spaced membership", proc: "0::/app \n",
			files: map[string]string{
				"cgroup.controllers": "memory cpu", "app /memory.max": "536870912", "app/memory.max": "1073741824",
			},
			wantMemory: memoryCeiling, wantCPU: -1, constraint: cgroup.LimitConstraintMax,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for name, contents := range tc.files {
				path := filepath.Join(root, name)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
				require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
			}
			procFile := filepath.Join(t.TempDir(), "cgroup")
			require.NoError(t, os.WriteFile(procFile, []byte(tc.proc), 0o600))
			withIsolatedEnv(t, nil, nil, nil, func(memory *int64, cpus, _ *int, _ *bytes.Buffer) {
				readLimitsFromFunc = func(cgroupRoot string) (cgroup.Limits, error) {
					return cgroup.ReadLimitsCustom(cgroupRoot, procFile)
				}
				result := AutoTune(WithCgroupRoot(root))
				assert.Empty(t, result.SkippedReason)
				assert.Equal(t, tc.constraint, result.ConstrainingLimit)
				assert.Equal(t, tc.wantCPU, *cpus)
				wantMemory := tc.wantMemory
				if wantMemory > 0 {
					var ok bool
					wantMemory, ok = cgroup.CalculateGOMEMLIMIT(wantMemory, cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes)
					require.True(t, ok)
				}
				assert.Equal(t, wantMemory, *memory)
				assert.Equal(t, tc.wantMemory >= 0, result.MemLimitApplied)
			})
		})
	}
}

func TestAutoTuneZeroMemoryPolicy(t *testing.T) {
	oldRetained, oldCurrent := retainedExecutableMemoryFunc, currentMemoryLimitFunc
	t.Cleanup(func() {
		retainedExecutableMemoryFunc, currentMemoryLimitFunc = oldRetained, oldCurrent
	})
	const previousMemory = int64(536870912)
	currentMemoryLimitFunc = func() int64 { return previousMemory }
	limits := &cgroup.Limits{CgroupVersion: cgroup.VersionV2, MemoryHighFinite: true, CPUQuota: 2, CPUs: 2}
	for _, tc := range []struct {
		name          string
		env           map[string]string
		probeErr      error
		dryRun        bool
		batch         bool
		memoryApplied bool
	}{
		{name: "unknown retained storage still has zero ceiling", probeErr: errors.New("storage unavailable"), memoryApplied: true},
		{name: "explicit memory setting preserved", env: map[string]string{boundaryMemoryEnvironment: "32MiB"}},
		{name: "dry run", dryRun: true},
		{name: "batch does not use previous positive budget", batch: true, memoryApplied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			retainedExecutableMemoryFunc = func() (int64, error) { return 0, tc.probeErr }
			withIsolatedEnv(t, tc.env, limits, nil, func(memory *int64, cpus, gogc *int, _ *bytes.Buffer) {
				opts := []Option{WithDryRun(tc.dryRun)}
				if tc.batch {
					opts = append(opts, WithProfile(ProfileBatchETL))
				}
				result := AutoTune(opts...)
				assert.Equal(t, cgroup.LimitConstraintHigh, result.ConstrainingLimit)
				assert.Equal(t, tc.memoryApplied, result.MemLimitApplied)
				if tc.memoryApplied {
					assert.Zero(t, *memory)
				} else {
					assert.EqualValues(t, -1, *memory)
				}
				assert.False(t, result.GOGCApplied)
				assert.Equal(t, -999, *gogc)
				if tc.batch {
					assert.Contains(t, result.SkippedReason, "no finite effective memory ceiling")
				}
				if tc.dryRun {
					assert.Equal(t, -1, *cpus)
					assert.Equal(t, 2, result.GOMAXPROCS)
				} else {
					assert.Equal(t, 2, *cpus)
				}
			})
		})
	}
}
