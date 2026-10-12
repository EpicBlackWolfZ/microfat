package runner

import (
	"strconv"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fixtureTargetMemoryMax = "target.memory.max"
	fixtureControlApplied  = "applied"
	fixtureMemoryDensity   = "throughput_per_limit_gib"
	fixtureCPUDensity      = "throughput_per_quota_vcpu"
)

func TestDensityUsesEffectiveMemoryLimit(t *testing.T) {
	t.Parallel()
	const (
		gibibyte       = 1024 * 1024 * 1024
		effectiveBytes = 64 * 1024 * 1024
		requestedBytes = effectiveBytes + 4095
		throughput     = 1000.0
	)
	for _, name := range []string{fixtureTargetMemoryMax, "target.memory.limit_in_bytes"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			trial := densityTrial([]schema.Control{{Name: name, Requested: strconv.Itoa(requestedBytes),
				Effective: strconv.Itoa(effectiveBytes), State: fixtureControlApplied}}, throughput)
			addDensityMetrics(trial)
			metric := trial.Metrics[fixtureMemoryDensity]
			require.NotNil(t, metric.Value)
			assert.Equal(t, throughput/(float64(effectiveBytes)/gibibyte), *metric.Value)
			assert.Equal(t, "derived", metric.Kind)
			assert.Equal(t, "fortio/applied-target-limit", metric.Source)
			assert.Nil(t, trial.Metrics[fixtureCPUDensity].Value)
		})
	}
}

func TestDensityUsesEffectiveCPUQuotaAndPeriod(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		controls []schema.Control
	}{
		{"v2", []schema.Control{{Name: "target.cpu.max", Requested: "200000 100000", Effective: "150000 200000", State: fixtureControlApplied}}},
		{"v1", []schema.Control{
			{Name: "target.cpu.cfs_quota_us", Requested: "200000", Effective: "150000", State: fixtureControlApplied},
			{Name: "target.cpu.cfs_period_us", Requested: "100000", Effective: "200000", State: fixtureControlApplied},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			const throughput, effectiveVCPUs = 1000.0, 0.75
			trial := densityTrial(test.controls, throughput)
			addDensityMetrics(trial)
			metric := trial.Metrics[fixtureCPUDensity]
			require.NotNil(t, metric.Value)
			assert.Equal(t, throughput/effectiveVCPUs, *metric.Value)
			assert.Equal(t, "derived", metric.Kind)
			assert.Equal(t, "fortio/applied-target-limit", metric.Source)
		})
	}
}

func TestDensityRequiresFiniteAppliedReadback(t *testing.T) {
	t.Parallel()
	const finiteMemory = "67108864"
	for _, test := range []struct {
		name, metric string
		controls     []schema.Control
	}{
		{"absent_memory", fixtureMemoryDensity, nil},
		{"missing_memory_readback", fixtureMemoryDensity, []schema.Control{{Name: fixtureTargetMemoryMax, Requested: finiteMemory}}},
		{"unapplied_memory", fixtureMemoryDensity, []schema.Control{
			{Name: fixtureTargetMemoryMax, Effective: finiteMemory, State: "unavailable"},
		}},
		{"unlimited_v2_memory", fixtureMemoryDensity, appliedDensityControl(fixtureTargetMemoryMax, "max")},
		{"unlimited_v1_memory", fixtureMemoryDensity,
			appliedDensityControl("target.memory.limit_in_bytes", strconv.FormatInt(cgroup.UnlimitedCgroupV1MemoryThreshold, 10))},
		{"zero_memory", fixtureMemoryDensity, appliedDensityControl(fixtureTargetMemoryMax, "0")},
		{"negative_memory", fixtureMemoryDensity, appliedDensityControl(fixtureTargetMemoryMax, "-1")},
		{"malformed_memory", fixtureMemoryDensity, appliedDensityControl(fixtureTargetMemoryMax, "64MiB")},
		{"overflowing_memory", fixtureMemoryDensity, appliedDensityControl(fixtureTargetMemoryMax, "9223372036854775808")},
		{"duplicate_memory", fixtureMemoryDensity, []schema.Control{
			{Name: fixtureTargetMemoryMax, Effective: finiteMemory, State: fixtureControlApplied},
			{Name: fixtureTargetMemoryMax, Effective: finiteMemory, State: fixtureControlApplied},
		}},
		{"unlimited_v2_cpu", fixtureCPUDensity, appliedDensityControl("target.cpu.max", "max 100000")},
		{"missing_v2_period", fixtureCPUDensity, appliedDensityControl("target.cpu.max", "100000")},
		{"extra_v2_field", fixtureCPUDensity, appliedDensityControl("target.cpu.max", "100000 100000 1")},
		{"zero_v2_period", fixtureCPUDensity, appliedDensityControl("target.cpu.max", "100000 0")},
		{"negative_v2_quota", fixtureCPUDensity, appliedDensityControl("target.cpu.max", "-1 100000")},
		{"missing_v1_period", fixtureCPUDensity, appliedDensityControl("target.cpu.cfs_quota_us", "100000")},
		{"unlimited_v1_cpu", fixtureCPUDensity, []schema.Control{
			{Name: "target.cpu.cfs_quota_us", Effective: "-1", State: fixtureControlApplied},
			{Name: "target.cpu.cfs_period_us", Effective: "100000", State: fixtureControlApplied},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			trial := densityTrial(test.controls, 1000)
			addDensityMetrics(trial)
			metric := trial.Metrics[test.metric]
			assert.Nil(t, metric.Value)
			assert.NotEmpty(t, metric.Reason)
		})
	}
	t.Run("missing_throughput", func(t *testing.T) {
		t.Parallel()
		trial := &schema.ProcessTrial{Metrics: make(map[string]schema.Measurement),
			Controls: appliedDensityControl(fixtureTargetMemoryMax, finiteMemory)}
		addDensityMetrics(trial)
		assert.Nil(t, trial.Metrics[fixtureMemoryDensity].Value)
	})
}

func densityTrial(controls []schema.Control, throughput float64) *schema.ProcessTrial {
	return &schema.ProcessTrial{Controls: controls,
		Metrics: map[string]schema.Measurement{"throughput_qps": schema.Measured(throughput, "requests/s", "steady_state", "fortio")}}
}

func appliedDensityControl(name, effective string) []schema.Control {
	return []schema.Control{{Name: name, Effective: effective, State: fixtureControlApplied}}
}
