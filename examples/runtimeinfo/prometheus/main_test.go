package main

import (
	"math"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/runtimeinfo"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollector(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		snapshot runtimeinfo.Snapshot
		want     map[string]float64
	}{
		{
			name: "native process has observations without launcher claims",
			snapshot: runtimeinfo.Snapshot{Observed: runtimeinfo.ObservedRuntime{
				GOMAXPROCS: present(uint64(2)), GOMEMLIMITBytes: present(uint64(math.MaxInt64)), GOGCPercent: present(uint64(100)),
			}},
			want: map[string]float64{
				"microfat_runtime_gomaxprocs": 2, "microfat_runtime_memory_limit_enabled": 0,
				"microfat_runtime_gogc_percent": 100, "microfat_runtime_gogc_enabled": 1,
			},
		},
		{
			name: "finite soft limit and disabled GOGC trigger remain separate from hints",
			snapshot: runtimeinfo.Snapshot{
				Launcher: runtimeinfo.LauncherHints{
					SelectedVariant: present("v3"),
					SelectedSHA256:  present("not-a-label"), OriginalExecutable: present("/not/a/label"),
					Cgroup: runtimeinfo.CgroupHints{CPUQuota: present(1.5), MemoryLimitBytes: present(int64(0))},
				},
				Observed: runtimeinfo.ObservedRuntime{
					GOMAXPROCS: present(uint64(2)), GOMEMLIMITBytes: present(uint64(1024)), GOGCPercent: present(uint64(math.MaxUint64)),
				},
			},
			want: map[string]float64{
				"microfat_launcher_hint_info": 1, "microfat_runtime_gomaxprocs": 2,
				"microfat_runtime_memory_limit_enabled": 1, "microfat_runtime_gomemlimit_bytes": 1024,
				"microfat_runtime_gogc_enabled": 0, "microfat_launcher_cgroup_cpu_quota_hint": 1.5,
				"microfat_launcher_cgroup_memory_limit_hint_bytes": 0,
			},
		},
		{
			name: "invalid unsupported and unavailable fields do not produce zero samples",
			snapshot: runtimeinfo.Snapshot{
				Launcher: runtimeinfo.LauncherHints{Cgroup: runtimeinfo.CgroupHints{
					CPUQuota:         runtimeinfo.Field[float64]{Status: runtimeinfo.StatusInvalid, Raw: "NaN"},
					MemoryLimitBytes: runtimeinfo.Field[int64]{Status: runtimeinfo.StatusUnsupported},
				}},
				Observed: runtimeinfo.ObservedRuntime{
					GOMAXPROCS: runtimeinfo.Field[uint64]{Status: runtimeinfo.StatusUnavailable},
				},
			},
			want: map[string]float64{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registry := prometheus.NewPedanticRegistry()
			collector := newCollector()
			collector.read = func() runtimeinfo.Snapshot { return test.snapshot }
			require.NoError(t, registry.Register(collector))
			families, err := registry.Gather()
			require.NoError(t, err)
			got := make(map[string]float64)
			for _, family := range families {
				require.Len(t, family.GetMetric(), 1)
				metric := family.GetMetric()[0]
				got[family.GetName()] = metric.GetGauge().GetValue()
				for _, label := range metric.GetLabel() {
					assert.NotContains(t, label.GetName(), "sha256")
					assert.NotContains(t, label.GetName(), "executable")
					assert.NotContains(t, label.GetValue(), "not-a-label")
					assert.NotContains(t, label.GetValue(), "/not/a/label")
				}
			}
			assert.Equal(t, test.want, got)
		})
	}
}

func present[T any](value T) runtimeinfo.Field[T] {
	return runtimeinfo.Field[T]{Value: value, Status: runtimeinfo.StatusPresent}
}
