package cgroup

import "strings"

// CPUPolicy controls whether microfat applies its static startup CPU quota policy.
type CPUPolicy string

const (
	// CPUPolicyStatic applies floor-rounded CPU quotas, with a minimum of one CPU.
	CPUPolicyStatic CPUPolicy = "static"
	// CPUPolicyNative leaves CPU parallelism to the application and Go runtime.
	// It does not re-enable adaptation disabled by environment settings or earlier setters.
	CPUPolicyNative CPUPolicy = "native"
)

// ResolveCPUPolicy gives valid environment settings precedence over the supplied fallback.
// Empty or invalid settings retain the fallback; an invalid fallback defaults to static.
func ResolveCPUPolicy(envValue string, fallback CPUPolicy) CPUPolicy {
	for _, value := range []string{envValue, string(fallback)} {
		switch policy := CPUPolicy(strings.ToLower(strings.TrimSpace(value))); policy {
		case CPUPolicyStatic, CPUPolicyNative:
			return policy
		}
	}
	return CPUPolicyStatic
}

// ApplyCPUPolicy omits CPU tuning in native-preservation mode without changing memory or GC tuning.
func (p *TuningPlan) ApplyCPUPolicy(policy CPUPolicy) {
	if policy == CPUPolicyNative {
		p.GOMAXPROCS = 0
		p.GOMAXPROCSStr = ""
	}
}
