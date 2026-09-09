//go:build unix

package system

import (
	"os"
	"runtime"
	"syscall"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

func Usage(state *os.ProcessState) map[string]schema.Measurement {
	result := make(map[string]schema.Measurement)
	if state == nil {
		return result
	}
	result["user_cpu_seconds"] = schema.Measured(state.UserTime().Seconds(), "s", "lifetime", "wait4")
	result["system_cpu_seconds"] = schema.Measured(state.SystemTime().Seconds(), "s", "lifetime", "wait4")
	if usage, ok := state.SysUsage().(*syscall.Rusage); ok {
		scale := float64(kibibyte)
		if runtime.GOOS == "darwin" {
			scale = 1
		}
		result["peak_rss_bytes"] = schema.Measured(float64(usage.Maxrss)*scale, "bytes", "lifetime", "wait4")
		for name, value := range map[string]int64{"minor_faults": usage.Minflt, "major_faults": usage.Majflt,
			"voluntary_context_switches": usage.Nvcsw, "involuntary_context_switches": usage.Nivcsw} {
			result[name] = schema.Measured(float64(value), "count", "lifetime", "wait4")
		}
	}
	return result
}
