//go:build linux

package main

import (
	"os"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

const (
	autotuneEnabled  = "enabled"
	autotuneDisabled = "disabled"
	autotuneDryRun   = "dry-run"
)

// resolveRequestedExecMode mirrors dispatch policy, including its legacy fallback and invalid-value handling.
func resolveRequestedExecMode() string {
	requested := os.Getenv(format.EnvExecMode)
	if requested == "" {
		requested = os.Getenv(format.EnvDispatchMode)
	}
	switch {
	case strings.EqualFold(requested, format.ExecModeCache):
		return format.ExecModeCache
	case strings.EqualFold(requested, format.ExecModeMemfd):
		return format.ExecModeMemfd
	default:
		return format.ExecModeAuto
	}
}

func runtimeAutotuneStatus() string {
	opt := strings.TrimSpace(os.Getenv(format.EnvAutotune))
	if opt == "0" || strings.EqualFold(opt, "false") {
		return autotuneDisabled
	}
	opt = strings.TrimSpace(os.Getenv(format.EnvDryRun))
	if opt == "1" || strings.EqualFold(opt, "true") {
		return autotuneDryRun
	}
	return autotuneEnabled
}

// resolveRuntimeTuning is shared by dispatch and inspection so candidate plans use the exact same inputs.
func resolveRuntimeTuning(
	limits cgroup.Limits,
	entry *format.VariantEntry,
	execMode string,
	keyIndex map[string]int,
	env []string,
) (cgroup.Limits, cgroup.TuningPlan) {
	gcProfile, _ := cgroup.ParseGCProfile(os.Getenv(format.EnvGCProfile))
	liveHeap, _ := cgroup.ParseByteSize(os.Getenv(format.EnvLiveHeapEstimate))
	if retained := runtimeRetainedExecutableBytes(entry, execMode); retained > 0 {
		limits.RetainedExecutableBytes = retained
	}
	plan := cgroup.ResolveTuningPlanWithProfile(
		limits,
		os.Getenv(format.EnvMemRatio),
		cgroup.DefaultMemoryRatio,
		cgroup.DefaultMinHeadroomBytes,
		gcProfile,
		liveHeap,
	)
	plan.ApplyCPUPolicy(cgroup.ResolveCPUPolicy(os.Getenv(format.EnvCPUPolicy), cgroup.CPUPolicyStatic))
	if index, exists := keyIndex["GOMEMLIMIT"]; exists {
		_, value, _ := strings.Cut(env[index], "=")
		plan.ResolveBatchGOGC(cgroup.RuntimeMemoryLimit(value))
	}
	return limits, plan
}

func runtimeRetainedExecutableBytes(entry *format.VariantEntry, execMode string) int64 {
	if execMode != format.ExecModeMemfd || entry.UncompressedSize <= 0 || entry.UncompressedSize > format.MaxPayloadSize {
		return 0
	}
	page := int64(os.Getpagesize())
	return (entry.UncompressedSize + page - 1) / page * page
}
