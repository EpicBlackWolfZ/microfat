//go:build !minimal

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

const (
	observationCollected   = "collected"
	observationUnavailable = "unavailable"
	observationUnknown     = "unknown"
	observationFinite      = "finite"
	environmentInherited   = "inherited"
	environmentAutotune    = "autotune"
	environmentUnset       = "unset"
)

var runtimeEnvironmentKeys = [...]string{"GOMEMLIMIT", "GOMAXPROCS", "GOGC"}

func buildRuntimeInfo(baseEnv []string, entry *format.VariantEntry) (*format.CgroupInfo, *format.RuntimeInfo) {
	limits, err := readCgroupLimitsFunc()
	cgInfo := cgroupObservationInfo(limits, err)
	info := &format.RuntimeInfo{
		Autotune:  runtimeAutotuneStatus(),
		CPUPolicy: string(cgroup.ResolveCPUPolicy(os.Getenv(format.EnvCPUPolicy), cgroup.CPUPolicyStatic)),
	}
	modes := []string{resolveRequestedExecMode()}
	if modes[0] == format.ExecModeAuto {
		modes = []string{format.ExecModeCache, format.ExecModeMemfd}
	}
	for _, mode := range modes {
		if entry == nil {
			break
		}
		info.Candidates = append(info.Candidates,
			buildRuntimeCandidate(baseEnv, entry, limits, mode, info.Autotune, cgInfo.Status == observationCollected))
	}
	// The legacy scalar tuning fields are prospective and only unambiguous for a single candidate.
	if len(info.Candidates) == 1 {
		plan := info.Candidates[0].Tuning
		cgInfo.GOMEMLIMIT, cgInfo.GOMAXPROCS, cgInfo.GOGC, cgInfo.GCProfile =
			plan.GOMEMLIMIT, plan.GOMAXPROCS, plan.GOGC, plan.GCProfile
	}
	return cgInfo, info
}

func cgroupObservationInfo(limits cgroup.Limits, err error) *format.CgroupInfo {
	_, finite := limits.EffectiveMemoryLimit()
	info := &format.CgroupInfo{
		Version:                    limits.CgroupVersion,
		Status:                     observationCollected,
		MemoryLimitBytes:           limits.MemoryLimitBytes,
		MemoryLimitStatus:          numericObservationStatus(limits.HasMemoryLimit()),
		MemoryHighBytes:            limits.MemoryHighBytes,
		MemoryHighStatus:           numericObservationStatus(limits.HasMemoryHigh()),
		EffectiveMemoryLimitBytes:  limits.EffectiveMemoryLimitBytes,
		EffectiveMemoryLimitStatus: numericObservationStatus(finite),
		CPUQuota:                   limits.CPUQuota,
		CPUQuotaStatus:             numericObservationStatus(limits.CPUQuota > 0),
	}
	switch {
	case err != nil:
		info.Status, info.Error = observationUnavailable, err.Error()
	case limits.CgroupVersion == cgroup.VersionUnknown:
		info.Status = observationUnknown
	}
	if info.Status != observationCollected {
		info.MemoryLimitStatus, info.MemoryHighStatus = observationUnknown, observationUnknown
		info.EffectiveMemoryLimitStatus, info.CPUQuotaStatus = observationUnknown, observationUnknown
	}
	return info
}

func numericObservationStatus(finite bool) string {
	if finite {
		return observationFinite
	}
	// The collector does not distinguish an unavailable value from a confirmed unlimited value.
	return observationUnknown
}

func buildRuntimeCandidate(
	baseEnv []string,
	entry *format.VariantEntry,
	limits cgroup.Limits,
	mode, autotune string,
	available bool,
) format.RuntimeCandidate {
	env := append([]string(nil), baseEnv...)
	keyIndex := make(map[string]int, len(env))
	inherited := make(map[string]bool, len(runtimeEnvironmentKeys))
	for i, value := range env {
		if key, _, found := strings.Cut(value, "="); found && key != "" {
			keyIndex[key], inherited[key] = i, true
		}
	}
	plan := cgroup.TuningPlan{}
	retained := runtimeRetainedExecutableBytes(entry, mode)
	if available {
		limits, plan = resolveRuntimeTuning(limits, entry, mode, keyIndex, env)
		retained = limits.RetainedExecutableBytes
		if autotune == autotuneEnabled {
			env = applyRuntimeTuningPlan(env, keyIndex, plan)
		}
	}
	candidate := format.RuntimeCandidate{
		ExecMode:                mode,
		RetainedExecutableBytes: retained,
		Tuning: format.RuntimeTuningInfo{
			Available:         available,
			GOMEMLIMIT:        plan.GOMEMLIMITStr,
			GOMEMLIMITBytes:   plan.GOMEMLIMITBytes,
			GOMAXPROCS:        plan.GOMAXPROCS,
			GOMAXPROCSValue:   plan.GOMAXPROCSStr,
			GOGC:              plan.GOGCStr,
			GCProfile:         string(plan.GCProfile),
			AppliedRatio:      plan.AppliedRatio,
			ConstrainingLimit: plan.ConstrainingLimit,
			GOGCSkippedReason: plan.GOGCSkippedReason,
		},
		EffectiveEnvironment: make(map[string]format.RuntimeEnvironmentValue, len(runtimeEnvironmentKeys)),
	}
	for _, key := range runtimeEnvironmentKeys {
		value := format.RuntimeEnvironmentValue{Source: environmentUnset}
		if index, exists := keyIndex[key]; exists {
			_, value.Value, _ = strings.Cut(env[index], "=")
			value.Present, value.Source = true, environmentAutotune
			if inherited[key] {
				value.Source = environmentInherited
			}
		}
		candidate.EffectiveEnvironment[key] = value
	}
	return candidate
}

func printRuntimeInfo(cgInfo *format.CgroupInfo, info *format.RuntimeInfo, mode string) {
	fmt.Printf("Execution Policy:  %s", mode)
	if mode == format.ExecModeAuto {
		fmt.Printf(" (cache-first; sealed memfd on a cache miss)")
	}
	fmt.Printf("\nRuntime Tuning:    %s (CPU policy: %s)\n", info.Autotune, info.CPUPolicy)
	fmt.Printf("\nContainer Observations: %s (cgroup v%d)\n", cgInfo.Status, cgInfo.Version)
	if cgInfo.Error != "" {
		fmt.Printf("  Collection Error: %s\n", cgInfo.Error)
	}
	printMemoryObservation("Memory Ceiling", cgInfo.MemoryLimitBytes, cgInfo.MemoryLimitStatus)
	printMemoryObservation("Memory High", cgInfo.MemoryHighBytes, cgInfo.MemoryHighStatus)
	printMemoryObservation("Effective Ceiling", cgInfo.EffectiveMemoryLimitBytes, cgInfo.EffectiveMemoryLimitStatus)
	if cgInfo.CPUQuotaStatus == observationFinite {
		fmt.Printf("  CPU CFS Quota: %g cores", cgInfo.CPUQuota)
		if info.CPUPolicy == string(cgroup.CPUPolicyNative) {
			fmt.Printf(" -> native Go CPU policy")
		}
		fmt.Println()
	} else {
		fmt.Printf("  CPU CFS Quota: unknown (unavailable or unlimited)\n")
	}
	for _, candidate := range info.Candidates {
		printRuntimeCandidate(candidate)
	}
}

func printMemoryObservation(label string, bytes int64, status string) {
	if status == observationFinite {
		fmt.Printf("  %s: %d bytes\n", label, bytes)
		return
	}
	fmt.Printf("  %s: unknown (unavailable or unlimited)\n", label)
}

func printRuntimeCandidate(candidate format.RuntimeCandidate) {
	fmt.Printf("\nProspective %s Runtime Environment:\n", candidate.ExecMode)
	fmt.Printf("  Retained Executable: %d bytes\n", candidate.RetainedExecutableBytes)
	plan := candidate.Tuning
	if plan.Available {
		fmt.Printf("  Computed Tuning: GOMEMLIMIT=%q GOMAXPROCS=%q GOGC=%q\n", plan.GOMEMLIMIT, plan.GOMAXPROCSValue, plan.GOGC)
		fmt.Printf("  Memory Ratio: %g; GC Profile: %s; Memory Constraint: %s\n",
			plan.AppliedRatio, plan.GCProfile, plan.ConstrainingLimit)
	} else {
		fmt.Printf("  Computed Tuning: unavailable\n")
	}
	if plan.GOGCSkippedReason != "" {
		fmt.Printf("  GC Plan: %s\n", plan.GOGCSkippedReason)
	}
	for _, key := range runtimeEnvironmentKeys {
		value := candidate.EffectiveEnvironment[key]
		if value.Present {
			fmt.Printf("  Effective %s: %q (%s)\n", key, value.Value, value.Source)
		} else {
			fmt.Printf("  Effective %s: unset (Go runtime default)\n", key)
		}
	}
}
