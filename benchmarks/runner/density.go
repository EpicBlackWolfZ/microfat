package runner

import "github.com/EpicBlackWolfZ/microfat/benchmarks/schema"

// Density names the applied limit used as denominator; host capacity is never substituted.
func addDensityMetrics(cfg ExperimentConfig, trial *schema.ProcessTrial) {
	const gibibyte = 1024 * 1024 * 1024
	qps := trial.Metrics["throughput_qps"]
	for _, dimension := range []struct {
		name, unit, controlV1, controlV2 string
		denominator                      float64
	}{
		{"throughput_per_quota_vcpu", "requests/s/vCPU", "target.cpu.cfs_quota_us", "target.cpu.max",
			float64(cfg.Target.CPUQuotaUS) / float64(cfg.Target.CPUPeriodUS)},
		{"throughput_per_limit_gib", "requests/s/GiB", "target.memory.limit_in_bytes", "target.memory.max",
			float64(cfg.Target.MemoryBytes) / gibibyte},
	} {
		metric := schema.Unavailable(dimension.unit, "steady_state", "fortio/applied-target-limit", "finite applied target limit unavailable")
		for _, control := range trial.Controls {
			if control.State == "applied" && (control.Name == dimension.controlV1 || control.Name == dimension.controlV2) &&
				dimension.denominator > 0 && qps.Value != nil {
				metric = schema.Measured(*qps.Value/dimension.denominator, dimension.unit, "steady_state", "fortio/applied-target-limit")
				metric.Kind = "derived"
			}
		}
		trial.Metrics[dimension.name] = metric
	}
}
