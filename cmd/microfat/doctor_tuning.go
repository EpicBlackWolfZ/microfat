package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/runtimeinfo"
)

type doctorLookupEnv func(string) (string, bool)

type doctorTuningInputs struct {
	GCProfile             runtimeinfo.Field[string] `json:"gc_profile"`
	LiveHeapEstimateBytes runtimeinfo.Field[int64]  `json:"live_heap_estimate_bytes"`
}

// DoctorTuningPreview separates a simulated plan from inherited configuration.
// It never applies settings and does not select or account for a payload.
type DoctorTuningPreview struct {
	Applied              bool                                      `json:"applied"`
	Inputs               doctorTuningInputs                        `json:"inputs"`
	Autotune             string                                    `json:"autotune"`
	CPUPolicy            cgroup.CPUPolicy                          `json:"cpu_policy"`
	Tuning               format.RuntimeTuningInfo                  `json:"tuning"`
	EffectiveEnvironment map[string]format.RuntimeEnvironmentValue `json:"effective_environment"`
	Reasons              map[string]string                         `json:"reasons"`
	Notice               string                                    `json:"notice"`
}

func doctorInput(flag, value, env string, changed bool, lookup doctorLookupEnv) runtimeinfo.Field[string] {
	f := runtimeinfo.Field[string]{Source: env, Status: runtimeinfo.StatusMissing}
	if changed {
		f.Raw, f.Source, f.Status = value, flag, runtimeinfo.StatusPresent
	} else if raw, exists := lookup(env); exists {
		f.Raw, f.Status = raw, runtimeinfo.StatusPresent
	}
	f.Value = f.Raw
	return f
}

func resolveDoctorTuningInputs(
	profile, heap string, profileSet, heapSet bool, lookup doctorLookupEnv,
) (doctorTuningInputs, error) {
	inputs := doctorTuningInputs{GCProfile: doctorInput("--gc-profile", profile, format.EnvGCProfile, profileSet, lookup)}
	parsedProfile, err := cgroup.ParseGCProfile(inputs.GCProfile.Raw)
	if err != nil {
		return inputs, fmt.Errorf("%s: %w", inputs.GCProfile.Source, err)
	}
	if inputs.GCProfile.Status == runtimeinfo.StatusMissing {
		inputs.GCProfile.Source = "default"
	}
	inputs.GCProfile.Value, inputs.GCProfile.Status = string(parsedProfile), runtimeinfo.StatusPresent
	heapInput := doctorInput("--live-heap-estimate", heap, format.EnvLiveHeapEstimate, heapSet, lookup)
	inputs.LiveHeapEstimateBytes = runtimeinfo.Field[int64]{
		Raw: heapInput.Raw, Source: heapInput.Source, Status: heapInput.Status,
	}
	if heapInput.Status == runtimeinfo.StatusPresent {
		value, parseErr := cgroup.ParseByteSize(heapInput.Raw)
		if parseErr != nil {
			return inputs, fmt.Errorf("%s: %w", heapInput.Source, parseErr)
		}
		inputs.LiveHeapEstimateBytes.Value = value
	}
	return inputs, nil
}

func doctorAutotuneStatus(lookup doctorLookupEnv) string {
	value, _ := lookup(format.EnvAutotune)
	value = strings.TrimSpace(value)
	if value == "0" || strings.EqualFold(value, "false") {
		return "disabled"
	}
	value, _ = lookup(format.EnvDryRun)
	value = strings.TrimSpace(value)
	if value == "1" || strings.EqualFold(value, "true") {
		return "dry-run"
	}
	return "enabled"
}

func buildDoctorTuningPreview(
	limits cgroup.Limits, available bool, inputs doctorTuningInputs, lookup doctorLookupEnv,
) *DoctorTuningPreview {
	if inputs.GCProfile.Status == "" {
		inputs.GCProfile = runtimeinfo.Field[string]{
			Value: string(cgroup.GCProfileDefault), Status: runtimeinfo.StatusPresent, Source: "default",
		}
		inputs.LiveHeapEstimateBytes = runtimeinfo.Field[int64]{Source: format.EnvLiveHeapEstimate, Status: runtimeinfo.StatusMissing}
	}
	cpuPolicy, _ := lookup(format.EnvCPUPolicy)
	preview := &DoctorTuningPreview{
		Inputs: inputs, Autotune: doctorAutotuneStatus(lookup),
		CPUPolicy: cgroup.ResolveCPUPolicy(cpuPolicy, cgroup.CPUPolicyStatic),
		Notice: "Preview only; no settings applied. Supplied heap is not measured. GOMEMLIMIT is a soft Go budget, not an RSS cap. " +
			"No payload is selected; retained executable memory is excluded.",
		EffectiveEnvironment: make(map[string]format.RuntimeEnvironmentValue),
		Reasons:              make(map[string]string),
	}
	plan := cgroup.TuningPlan{}
	if available {
		ratio, _ := lookup(format.EnvMemRatio)
		plan = cgroup.ResolveTuningPlanWithProfile(limits, ratio, cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes,
			cgroup.GCProfile(inputs.GCProfile.Value), inputs.LiveHeapEstimateBytes.Value)
		plan.ApplyCPUPolicy(preview.CPUPolicy)
		if value, present := lookup("GOMEMLIMIT"); present {
			plan.ResolveBatchGOGC(cgroup.RuntimeMemoryLimit(value))
		}
	}
	preview.Tuning = format.RuntimeTuningInfo{
		Available: available, GOMEMLIMIT: plan.GOMEMLIMITStr, GOMEMLIMITBytes: plan.GOMEMLIMITBytes,
		GOMAXPROCS: plan.GOMAXPROCS, GOMAXPROCSValue: plan.GOMAXPROCSStr, GOGC: plan.GOGCStr,
		GCProfile: string(plan.GCProfile), AppliedRatio: plan.AppliedRatio, ConstrainingLimit: plan.ConstrainingLimit,
		GOGCSkippedReason: plan.GOGCSkippedReason,
	}
	for _, setting := range []struct{ key, recommendation string }{
		{"GOMEMLIMIT", plan.GOMEMLIMITStr}, {"GOMAXPROCS", plan.GOMAXPROCSStr}, {"GOGC", plan.GOGCStr},
	} {
		value := format.RuntimeEnvironmentValue{Source: "unset"}
		reason := doctorMissingRecommendation(setting.key, plan, preview.CPUPolicy)
		switch inherited, present := lookup(setting.key); {
		case present:
			value = format.RuntimeEnvironmentValue{Present: true, Value: inherited, Source: "inherited"}
			reason = "Inherited environment value preserved verbatim; this is not an observed runtime setting."
		case !available:
			reason = "Cgroup discovery unavailable; no automatic recommendation."
		case preview.Autotune != "enabled":
			reason = "Autotuning is " + preview.Autotune + "; computed recommendations would not be applied."
		case setting.recommendation != "":
			value = format.RuntimeEnvironmentValue{Present: true, Value: setting.recommendation, Source: "autotune"}
			reason = "Shared tuning engine recommendation; preview only."
		}
		preview.EffectiveEnvironment[setting.key], preview.Reasons[setting.key] = value, reason
	}
	return preview
}

func doctorMissingRecommendation(key string, plan cgroup.TuningPlan, cpuPolicy cgroup.CPUPolicy) string {
	switch key {
	case "GOMEMLIMIT":
		return "No usable memory budget computed; see observed finite/unknown hard and throttle boundaries."
	case "GOMAXPROCS":
		if cpuPolicy == cgroup.CPUPolicyNative {
			return "Native CPU policy leaves parallelism to the payload and Go runtime."
		}
		return "No usable CPU quota recommendation; the payload and Go runtime retain control."
	default:
		if plan.GOGCSkippedReason != "" {
			return plan.GOGCSkippedReason
		}
		if plan.GCProfile == cgroup.GCProfileAdaptive {
			return "No adaptive GOGC computed; a positive supplied heap estimate and usable memory budget are required."
		}
		return "The selected profile supplies no GOGC recommendation; Go runtime defaults remain unknown to this preview."
	}
}

func probeDoctorCgroup(inputs doctorTuningInputs) (*CgroupReport, *DoctorTuningPreview) {
	limits, err := readDoctorCgroupLimits()
	available := err == nil && limits.CgroupVersion != cgroup.VersionUnknown
	preview := buildDoctorTuningPreview(limits, available, inputs, os.LookupEnv)
	if !available {
		report := &CgroupReport{Status: "unavailable"}
		if err != nil {
			report.Error = err.Error()
		}
		return report, preview
	}
	// Preserve the existing default cgroup recommendations; the requested profile
	// and override-aware simulation belong only to tuning_preview.
	plan := cgroup.ResolveTuningPlan(limits, os.Getenv(format.EnvMemRatio), cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes)
	effective, _ := limits.EffectiveMemoryLimit()
	return &CgroupReport{
		Status: "present", Detected: true, Version: limits.CgroupVersion,
		MemoryLimitBytes: limits.MemoryLimitBytes, MemoryHighBytes: limits.MemoryHighBytes,
		MemoryLimitFinite: limits.HasMemoryLimit(), MemoryHighFinite: limits.HasMemoryHigh(),
		EffectiveMemoryLimitBytes: effective, ConstrainingLimit: limits.ConstrainingMemoryLimit(),
		CPUQuota: limits.CPUQuota, GOMEMLIMITBytes: plan.GOMEMLIMITBytes, GOMEMLIMITStr: plan.GOMEMLIMITStr, GOMAXPROCS: plan.GOMAXPROCS,
	}, preview
}

func probeCgroup() *CgroupReport {
	report, _ := probeDoctorCgroup(doctorTuningInputs{})
	return report
}

func printDoctorTuningPreview(b *strings.Builder, preview *DoctorTuningPreview) {
	if preview == nil {
		return
	}
	appendFormat(b, "%s Runtime Tuning Preview (no settings applied)\n", glyphInfo)
	appendFormat(b, "    • GC Profile:     %s (source: %s)\n", preview.Inputs.GCProfile.Value, preview.Inputs.GCProfile.Source)
	if heap := preview.Inputs.LiveHeapEstimateBytes; heap.Status == runtimeinfo.StatusPresent {
		appendFormat(b, "    • Supplied Heap:  %s (source: %s; not measured)\n", formatBytes(heap.Value), heap.Source)
	} else {
		appendLine(b, "    • Supplied Heap:  missing (not measured)")
	}
	appendFormat(b, "    • Autotune:       %s (CPU policy: %s)\n", preview.Autotune, preview.CPUPolicy)
	if preview.Tuning.Available {
		boundary := preview.Tuning.ConstrainingLimit
		if boundary == "" {
			boundary = "unknown (unavailable or unlimited)"
		}
		appendFormat(b, "    • Memory Ratio:   %g (computed budget; hard/throttle boundary: %s)\n",
			preview.Tuning.AppliedRatio, boundary)
	}
	for _, setting := range []struct{ key, recommendation string }{
		{"GOMEMLIMIT", preview.Tuning.GOMEMLIMIT}, {"GOMAXPROCS", preview.Tuning.GOMAXPROCSValue}, {"GOGC", preview.Tuning.GOGC},
	} {
		key := setting.key
		if setting.recommendation != "" {
			appendFormat(b, "    • Candidate %s: %q\n", key, setting.recommendation)
		} else {
			appendFormat(b, "    • Candidate %s: unavailable (no recommendation)\n", key)
		}
		value := preview.EffectiveEnvironment[key]
		if value.Present {
			appendFormat(b, "    • %s: %q (source: %s)\n", key, value.Value, value.Source)
		} else {
			appendFormat(b, "    • %s: unset (runtime value unknown)\n", key)
		}
		appendFormat(b, "      %s\n", preview.Reasons[key])
	}
	appendFormat(b, "    • %s\n", preview.Notice)
	appendLine(b)
}
