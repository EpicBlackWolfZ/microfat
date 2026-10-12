//go:build linux && !minimal

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLauncherFilesystemBoundaries(t *testing.T) {
	cases := []struct {
		name         string
		files        map[string]string
		proc         string
		memory       string
		cpu          string
		zeroBoundary bool
		constraint   string
		memoryMax    int64
	}{
		{
			name: "direct memory with unmounted CPU", proc: "3:memory:/app\n4:cpu,cpuacct:/app\n",
			files:  map[string]string{"memory.limit_in_bytes": "-1", "app/memory.limit_in_bytes": "536870912"},
			memory: "469762048B", memoryMax: 536870912,
		},
		{
			name: "direct CPU with unmounted memory", proc: "3:memory:/app\n4:cpu,cpuacct:/app\n",
			files: map[string]string{
				"cpu.cfs_quota_us": "-1", "cpu.cfs_period_us": "100000",
				"app/cpu.cfs_quota_us": "200000", "app/cpu.cfs_period_us": "100000",
			},
			cpu: "2",
		},
		{
			name: "zero high retains hard ceiling and CPU", proc: "0::/app\n",
			files: map[string]string{
				"cgroup.controllers": "memory cpu", "app/memory.max": "536870912",
				"app/memory.high": "0", "app/cpu.max": "200000 100000",
			},
			memory: "0B", cpu: "2", zeroBoundary: true, constraint: cgroup.LimitConstraintHigh, memoryMax: 536870912,
		},
		{
			name: "zero hard ceiling retains CPU", proc: "0::/app\n",
			files: map[string]string{
				"cgroup.controllers": "memory cpu", "app/memory.max": "0",
				"app/memory.high": "max", "app/cpu.max": "200000 100000",
			},
			memory: "0B", cpu: "2", zeroBoundary: true, constraint: cgroup.LimitConstraintMax,
		},
		{
			name: "exact tab membership", proc: "0::/app\t\n",
			files: map[string]string{
				"cgroup.controllers": "memory cpu", "app\t/memory.max": "536870912", "app/memory.max": "1073741824",
			},
			memory: "469762048B", memoryMax: 536870912,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearInfoEnvironment(t)
			root := t.TempDir()
			for name, contents := range tc.files {
				path := filepath.Join(root, name)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
				require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
			}
			procFile := filepath.Join(t.TempDir(), "cgroup")
			require.NoError(t, os.WriteFile(procFile, []byte(tc.proc), 0o600))
			oldRead := readCgroupLimitsFunc
			t.Cleanup(func() { readCgroupLimitsFunc = oldRead })
			readCgroupLimitsFunc = func() (cgroup.Limits, error) { return cgroup.ReadLimitsCustom(root, procFile) }
			entry := &format.VariantEntry{Level: "v1", UncompressedSize: 1}
			env, limits := buildAutoTunedEnviron("", nil, entry, format.ExecModeCache, microarch.Info{}, microarch.PolicyResult{})
			require.NotNil(t, limits)
			values := infoEnvironmentValues(env)
			assert.Equal(t, tc.memory, values["GOMEMLIMIT"])
			assert.Equal(t, tc.cpu, values["GOMAXPROCS"])
			assert.Equal(t, tc.memoryMax, limits.MemoryLimitBytes)
			if tc.zeroBoundary {
				require.ErrorIs(t, checkExtractionBudget(entry, nil), cgroup.ErrMemoryBudget)
				assert.Equal(t, "0", values[format.EnvCgroupEffectiveLimitBytes])
				observed, runtime := buildRuntimeInfo(nil, entry)
				assert.Equal(t, observationFinite, observed.EffectiveMemoryLimitStatus)
				assert.Equal(t, observationFinite, observed.MemoryLimitStatus)
				assert.Equal(t, tc.constraint, runtime.Candidates[0].Tuning.ConstrainingLimit)
				assert.Equal(t, "0B", runtime.Candidates[0].EffectiveEnvironment["GOMEMLIMIT"].Value)
				if tc.constraint == cgroup.LimitConstraintHigh {
					assert.Equal(t, observationFinite, observed.MemoryHighStatus)
					assert.Equal(t, "0", values[format.EnvCgroupHighBytes])
				}
			} else {
				require.NoError(t, checkExtractionBudget(entry, nil))
			}
		})
	}
}
