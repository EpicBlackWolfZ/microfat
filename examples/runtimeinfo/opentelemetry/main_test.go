package main

import (
	"context"
	"math"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/runtimeinfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestObservableGauges(t *testing.T) {
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
				"microfat.runtime.gomaxprocs": 2, "microfat.runtime.memory_limit.enabled": 0,
				"microfat.runtime.gogc": 100, "microfat.runtime.gogc.enabled": 1,
			},
		},
		{
			name: "finite soft limit and disabled GOGC trigger remain separate from hints",
			snapshot: runtimeinfo.Snapshot{
				Launcher: runtimeinfo.LauncherHints{
					SelectedVariant: present("v3"),
					SelectedSHA256:  present("not-an-attribute"), OriginalExecutable: present("/not/an/attribute"),
					Cgroup: runtimeinfo.CgroupHints{CPUQuota: present(1.5), MemoryLimitBytes: present(int64(0))},
				},
				Observed: runtimeinfo.ObservedRuntime{
					GOMAXPROCS: present(uint64(2)), GOMEMLIMITBytes: present(uint64(1024)), GOGCPercent: present(uint64(math.MaxUint64)),
				},
			},
			want: map[string]float64{
				"microfat.launcher.hint.info": 1, "microfat.runtime.gomaxprocs": 2,
				"microfat.runtime.memory_limit.enabled": 1, "microfat.runtime.gomemlimit": 1024,
				"microfat.runtime.gogc.enabled": 0, "microfat.launcher.cgroup.cpu_quota.hint": 1.5,
				"microfat.launcher.cgroup.memory_limit.hint": 0,
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
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			t.Cleanup(func() { require.NoError(t, provider.Shutdown(context.Background())) })
			meter := provider.Meter("test")
			instruments, err := newInstruments(meter)
			require.NoError(t, err)
			registration, err := meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
				observe(observer, instruments, test.snapshot)
				return nil
			}, asObservables(instruments)...)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, registration.Unregister()) })
			var data metricdata.ResourceMetrics
			require.NoError(t, reader.Collect(t.Context(), &data))
			got := make(map[string]float64)
			for _, scope := range data.ScopeMetrics {
				for _, measurement := range scope.Metrics {
					gauge, ok := measurement.Data.(metricdata.Gauge[float64])
					require.True(t, ok, "measurement %s must be a gauge", measurement.Name)
					require.Len(t, gauge.DataPoints, 1)
					point := gauge.DataPoints[0]
					got[measurement.Name] = point.Value
					for _, attribute := range point.Attributes.ToSlice() {
						assert.NotContains(t, string(attribute.Key), "sha256")
						assert.NotContains(t, string(attribute.Key), "executable")
						assert.NotContains(t, attribute.Value.AsString(), "not-an-attribute")
						assert.NotContains(t, attribute.Value.AsString(), "/not/an/attribute")
					}
				}
			}
			assert.Equal(t, test.want, got)
		})
	}
}

func present[T any](value T) runtimeinfo.Field[T] {
	return runtimeinfo.Field[T]{Value: value, Status: runtimeinfo.StatusPresent}
}
