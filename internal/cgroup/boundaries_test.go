package cgroup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMembershipPathWhitespace(t *testing.T) {
	t.Parallel()
	for _, version := range []int{VersionV1, VersionV2} {
		for _, suffix := range []string{" ", "\t"} {
			for _, sibling := range []bool{false, true} {
				t.Run(filepath.Join(string(rune('0'+version)), suffix, map[bool]string{false: "absent", true: "sibling"}[sibling]),
					func(t *testing.T) {
						t.Parallel()
						root := t.TempDir()
						base, memoryFile, proc := root, cgroupV2MemoryMax, "0::/app"+suffix+"\n"
						if version == VersionV1 {
							base = filepath.Join(root, "memory")
							memoryFile, proc = "memory.limit_in_bytes", "3:memory:/app"+suffix+"\n"
						} else {
							writeV1DetectionFiles(t, root, map[string]string{"cgroup.controllers": "memory cpu"})
						}
						writeV1DetectionFiles(t, base, map[string]string{
							memoryFile: "1073741824", filepath.Join("app"+suffix, memoryFile): "134217728",
						})
						if sibling {
							writeV1DetectionFiles(t, base, map[string]string{filepath.Join("app", memoryFile): "1073741824"})
						}
						procFile := filepath.Join(t.TempDir(), "cgroup")
						require.NoError(t, os.WriteFile(procFile, []byte(proc), 0o600))
						limits, err := ReadLimitsCustom(root, procFile)
						require.NoError(t, err)
						assert.Equal(t, version, limits.CgroupVersion)
						assert.EqualValues(t, 134217728, limits.MemoryLimitBytes)
						assert.EqualValues(t, 134217728, limits.EffectiveMemoryLimitBytes)
					})
			}
		}
	}
}

func TestDirectV1MountWithOtherMembership(t *testing.T) {
	t.Parallel()
	for _, controller := range []string{"memory", "cpu"} {
		for _, present := range []bool{false, true} {
			t.Run(controller+map[bool]string{false: " missing hierarchy", true: " available hierarchy"}[present], func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				files := map[string]string{"memory.limit_in_bytes": "-1"}
				if controller == "cpu" {
					files = map[string]string{"cpu.cfs_quota_us": "-1", "cpu.cfs_period_us": "100000"}
				}
				if present {
					if controller == "memory" {
						files["app/memory.limit_in_bytes"] = "536870912"
					} else {
						files["app/cpu.cfs_quota_us"], files["app/cpu.cfs_period_us"] = "200000", "100000"
					}
				}
				writeV1DetectionFiles(t, root, files)
				procFile := filepath.Join(t.TempDir(), "cgroup")
				require.NoError(t, os.WriteFile(procFile, []byte("3:memory:/app\n4:cpu,cpuacct:/app\n"), 0o600))
				limits, err := ReadLimitsCustom(root, procFile)
				if !present {
					require.ErrorIs(t, err, ErrCgroupHierarchyNotFound)
					assert.Equal(t, VersionUnknown, limits.CgroupVersion)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, VersionV1, limits.CgroupVersion)
				if controller == "memory" {
					assert.EqualValues(t, 536870912, limits.MemoryLimitBytes)
					assert.Zero(t, limits.CPUs)
				} else {
					assert.Equal(t, 2, limits.CPUs)
					assert.Zero(t, limits.MemoryLimitBytes)
				}
			})
		}
	}
}

func TestFiniteZeroMemoryBoundaries(t *testing.T) {
	t.Parallel()
	for _, boundary := range []string{cgroupV2MemoryMax, cgroupV2MemoryHigh} {
		for _, location := range []string{"", "app"} {
			t.Run(boundary+"/"+location, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				files := map[string]string{
					"cgroup.controllers": "memory cpu", "memory.max": "536870912", "memory.high": "max",
					"app/memory.max": "max", "app/memory.high": "max", "app/cpu.max": "200000 100000",
				}
				files[filepath.Join(location, boundary)] = "0"
				writeV1DetectionFiles(t, root, files)
				procFile := filepath.Join(t.TempDir(), "cgroup")
				require.NoError(t, os.WriteFile(procFile, []byte("0::/app\n"), 0o600))
				limits, err := ReadLimitsCustom(root, procFile)
				require.NoError(t, err)
				assert.Equal(t, VersionV2, limits.CgroupVersion)
				assert.Equal(t, 2, limits.CPUs)
				assert.True(t, limits.MemoryLimitFinite)
				assert.Equal(t, boundary == cgroupV2MemoryHigh, limits.MemoryHighFinite)
				assert.Zero(t, limits.EffectiveMemoryLimitBytes)
				limit, finite := limits.EffectiveMemoryLimit()
				assert.Zero(t, limit)
				assert.True(t, finite)
				plan := ResolveTuningPlanWithProfile(limits, "", DefaultMemoryRatio, DefaultMinHeadroomBytes, GCProfileBatchETL, 0)
				assert.Equal(t, "0B", plan.GOMEMLIMITStr)
				assert.Equal(t, 2, plan.GOMAXPROCS)
				assert.Equal(t, map[string]string{cgroupV2MemoryMax: "max", cgroupV2MemoryHigh: "high"}[boundary], plan.ConstrainingLimit)
				assert.False(t, plan.GOGCApplied, "batch mode must retain GC for an unusable zero budget")
				if boundary == cgroupV2MemoryHigh {
					assert.EqualValues(t, 536870912, limits.MemoryLimitBytes)
				}
			})
		}
	}
}

func TestMemoryBoundaryPresence(t *testing.T) {
	t.Parallel()
	for _, limits := range []Limits{
		{}, {MemoryLimitFinite: true}, {MemoryHighFinite: true}, {MemoryLimitFinite: true, MemoryHighFinite: true},
		{MemoryHighBytes: 1}, {EffectiveMemoryLimitBytes: 1},
	} {
		limit, finite := limits.EffectiveMemoryLimit()
		if limits == (Limits{}) {
			assert.Zero(t, limit)
			assert.False(t, finite)
			continue
		}
		assert.True(t, finite)
		if limits.MemoryLimitFinite {
			assert.Equal(t, LimitConstraintMax, limits.ConstrainingMemoryLimit())
		}
	}
}
