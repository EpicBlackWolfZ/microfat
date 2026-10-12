package runner

import (
	"strconv"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
)

// Density names the applied limit used as denominator; host capacity is never substituted.
func addDensityMetrics(trial *schema.ProcessTrial) {
	const gibibyte = 1024 * 1024 * 1024
	limits := effectiveLimits(trial.Controls)
	qps := trial.Metrics["throughput_qps"]
	for _, dimension := range []struct {
		name, unit  string
		denominator float64
	}{
		{"throughput_per_quota_vcpu", "requests/s/vCPU", effectiveCPUQuota(limits)},
		{"throughput_per_limit_gib", "requests/s/GiB", effectiveMemoryLimit(limits) / gibibyte},
	} {
		metric := schema.Unavailable(dimension.unit, "steady_state", "fortio/applied-target-limit",
			"throughput or finite applied target limit unavailable")
		if dimension.denominator > 0 && qps.Value != nil {
			metric = schema.Measured(*qps.Value/dimension.denominator, dimension.unit, "steady_state", "fortio/applied-target-limit")
			metric.Kind = "derived"
		}
		trial.Metrics[dimension.name] = metric
	}
}

func effectiveLimits(controls []schema.Control) map[string]string {
	limits := make(map[string]string, len(controls))
	for _, control := range controls {
		_, duplicate := limits[control.Name]
		value := ""
		if control.State == "applied" && !duplicate {
			value = control.Effective
		}
		limits[control.Name] = value
	}
	return limits
}

func effectiveCPUQuota(limits map[string]string) float64 {
	quota, period := limits["target.cpu.cfs_quota_us"], limits["target.cpu.cfs_period_us"]
	if value, v2 := limits["target.cpu.max"]; v2 {
		const cpuMaxFields = 2
		fields := strings.Fields(value)
		if len(fields) != cpuMaxFields {
			return 0
		}
		quota, period = fields[0], fields[1]
	}
	q, p := positiveLimit(quota), positiveLimit(period)
	if q == 0 || p == 0 {
		return 0
	}
	return float64(q) / float64(p)
}

func effectiveMemoryLimit(limits map[string]string) float64 {
	value, v2 := limits["target.memory.max"]
	if !v2 {
		value = limits["target.memory.limit_in_bytes"]
	}
	limit := positiveLimit(value)
	if !v2 && limit >= cgroup.UnlimitedCgroupV1MemoryThreshold {
		return 0
	}
	return float64(limit)
}

func positiveLimit(value string) int64 {
	const decimalBase, integerBits = 10, 64
	limit, err := strconv.ParseInt(value, decimalBase, integerBits)
	if err != nil || limit <= 0 {
		return 0
	}
	return limit
}
