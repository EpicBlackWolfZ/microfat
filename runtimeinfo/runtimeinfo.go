// Package runtimeinfo reads informational launcher hints, current environment
// configuration and observed Go runtime settings without applying tuning.
//
// Environment metadata is untrusted and may be absent, forged or stale. Neither
// a payload digest nor an executable path proves origin or authorizes access.
// Read does not inspect a fat binary, discover cgroups or resolve tuning plans.
package runtimeinfo

import (
	"os"
	"runtime/metrics"
)

// Status describes the availability and interpretation of a field.
type Status string

const (
	StatusPresent     Status = "present"
	StatusMissing     Status = "missing"
	StatusInvalid     Status = "invalid"
	StatusUnsupported Status = "unsupported"
	StatusUnavailable Status = "unavailable"
)

// Field preserves a source and its raw environment text alongside a parsed value.
// Value is meaningful only for StatusPresent. Raw is empty for runtime metrics.
// A present opaque string is reported verbatim, without semantic validation.
type Field[T any] struct {
	Value  T      `json:"value"`
	Raw    string `json:"raw"`
	Status Status `json:"status"`
	Source string `json:"source"`
}

// Snapshot separates informational hints, configuration and runtime observations.
// Its fields are read during one call, but are not an atomic process snapshot.
type Snapshot struct {
	Launcher   LauncherHints         `json:"launcher"`
	Configured ConfiguredEnvironment `json:"configured"`
	Observed   ObservedRuntime       `json:"observed"`
}

// LauncherHints describes environment metadata emitted by current launchers.
// Presence does not establish that this process was launched by microfat.
type LauncherHints struct {
	SelectedVariant    Field[string] `json:"selected_variant"`
	HostArch           Field[string] `json:"host_arch"`
	HostLevel          Field[string] `json:"host_level"`
	ExecMode           Field[string] `json:"exec_mode"`
	DispatchMode       Field[string] `json:"dispatch_mode"`
	SelectedSHA256     Field[string] `json:"selected_sha256"`
	SelectedSizeBytes  Field[int64]  `json:"selected_size_bytes"`
	OriginalExecutable Field[string] `json:"original_executable"`
	PolicyApplied      Field[string] `json:"policy_applied"`
	OverrideReason     Field[string] `json:"override_reason"`
	Cgroup             CgroupHints   `json:"cgroup"`
}

// CgroupHints contains launcher-time observations and recommendations, not fresh
// cgroup discovery or evidence of applied tuning. Byte counts are logical values;
// GOMEMLIMIT is a soft Go runtime budget, not an RSS or physical storage quota.
type CgroupHints struct {
	Version                   Field[int64]   `json:"version"`
	MemoryLimitBytes          Field[int64]   `json:"memory_limit_bytes"`
	MemoryHighBytes           Field[int64]   `json:"memory_high_bytes"`
	EffectiveMemoryLimitBytes Field[int64]   `json:"effective_memory_limit_bytes"`
	CPUQuota                  Field[float64] `json:"cpu_quota"`
	RecommendedGOMEMLIMIT     Field[string]  `json:"recommended_gomemlimit"`
	RecommendedGOMAXPROCS     Field[int64]   `json:"recommended_gomaxprocs"`
	RecommendedGOGC           Field[int64]   `json:"recommended_gogc"`
	GCProfile                 Field[string]  `json:"gc_profile"`
	GOGCSkippedReason         Field[string]  `json:"gogc_skipped_reason"`
}

// ConfiguredEnvironment reports current environment text verbatim, including
// explicit empty strings. Values are not validated, defaulted or attributed to a
// user: a launcher can also set Go variables. Environment changes after startup
// need not change the runtime. Omitted fields are not evidence of default tuning.
type ConfiguredEnvironment struct {
	GOMEMLIMIT       Field[string] `json:"gomemlimit"`
	GOMAXPROCS       Field[string] `json:"gomaxprocs"`
	GOGC             Field[string] `json:"gogc"`
	CPUPolicy        Field[string] `json:"cpu_policy"`
	GCProfile        Field[string] `json:"gc_profile"`
	Autotune         Field[string] `json:"autotune"`
	DryRun           Field[string] `json:"dry_run"`
	MemoryRatio      Field[string] `json:"memory_ratio"`
	LiveHeapEstimate Field[string] `json:"live_heap_estimate"`
}

// ObservedRuntime reads the running Go process through runtime/metrics. An
// unavailable metric has StatusUnavailable. GOMEMLIMITBytes == math.MaxInt64
// means no Go memory limit; GOGCPercent == math.MaxUint64 means the GOGC
// percentage trigger is off. Memory-limit-driven garbage collection can remain.
// GOMAXPROCS counts threads allowed to execute Go code simultaneously, not CPUs.
type ObservedRuntime struct {
	GOMEMLIMITBytes Field[uint64] `json:"gomemlimit_bytes"`
	GOMAXPROCS      Field[uint64] `json:"gomaxprocs"`
	GOGCPercent     Field[uint64] `json:"gogc_percent"`
}

const (
	metricMemoryLimit = "/gc/gomemlimit:bytes"
	metricMaxProcs    = "/sched/gomaxprocs:threads"
	metricGCPercent   = "/gc/gogc:percent"
)

// Read returns current environment metadata and runtime observations. It does not
// modify environment, filesystem or runtime settings and has no init side effects.
// Unknown future environment keys are ignored; unsupported known enum values and
// invalid numeric fields preserve their raw values and do not fail other fields.
func Read() Snapshot {
	samples := []metrics.Sample{
		{Name: metricMemoryLimit}, {Name: metricMaxProcs}, {Name: metricGCPercent},
	}
	metrics.Read(samples)
	return Snapshot{
		Launcher:   readLauncher(os.LookupEnv),
		Configured: readConfigured(os.LookupEnv),
		Observed: ObservedRuntime{
			GOMEMLIMITBytes: observe(samples[0]),
			GOMAXPROCS:      observe(samples[1]),
			GOGCPercent:     observe(samples[2]),
		},
	}
}

func observe(sample metrics.Sample) Field[uint64] {
	f := Field[uint64]{Source: "runtime/metrics:" + sample.Name, Status: StatusUnavailable}
	if sample.Value.Kind() == metrics.KindUint64 {
		f.Value, f.Status = sample.Value.Uint64(), StatusPresent
	}
	return f
}

type lookupEnv func(string) (string, bool)

func readLauncher(lookup lookupEnv) LauncherHints {
	return LauncherHints{
		SelectedVariant:    textField(lookup, "MICROFAT_SELECTED_VARIANT"),
		HostArch:           enumField(lookup, "MICROFAT_HOST_ARCH", "amd64", "arm64"),
		HostLevel:          textField(lookup, "MICROFAT_HOST_LEVEL"),
		ExecMode:           enumField(lookup, "MICROFAT_EXEC_MODE", "memfd", "cache"),
		DispatchMode:       enumField(lookup, "MICROFAT_DISPATCH_MODE", "memfd", "cache"),
		SelectedSHA256:     digestField(lookup, "MICROFAT_SELECTED_SHA256"),
		SelectedSizeBytes:  integerField(lookup, "MICROFAT_SELECTED_SIZE", 1),
		OriginalExecutable: textField(lookup, "MICROFAT_ORIGINAL_EXE"),
		PolicyApplied:      textField(lookup, "MICROFAT_POLICY_APPLIED"),
		OverrideReason:     textField(lookup, "MICROFAT_OVERRIDE_REASON"),
		Cgroup:             readCgroup(lookup),
	}
}

func readCgroup(lookup lookupEnv) CgroupHints {
	version := integerField(lookup, "MICROFAT_CGROUP_VERSION", 0)
	if version.Status == StatusPresent && version.Value != 1 && version.Value != 2 {
		version.Value, version.Status = 0, StatusUnsupported
	}
	return CgroupHints{
		Version:                   version,
		MemoryLimitBytes:          integerField(lookup, "MICROFAT_CGROUP_LIMIT_BYTES", 0),
		MemoryHighBytes:           integerField(lookup, "MICROFAT_CGROUP_HIGH_BYTES", 0),
		EffectiveMemoryLimitBytes: integerField(lookup, "MICROFAT_CGROUP_EFFECTIVE_LIMIT_BYTES", 0),
		CPUQuota:                  quotaField(lookup, "MICROFAT_CGROUP_CPUS"),
		RecommendedGOMEMLIMIT:     textField(lookup, "MICROFAT_CGROUP_GOMEMLIMIT"),
		RecommendedGOMAXPROCS:     integerField(lookup, "MICROFAT_CGROUP_GOMAXPROCS", 1),
		RecommendedGOGC:           gcField(lookup, "MICROFAT_CGROUP_GOGC"),
		GCProfile: enumField(lookup, "MICROFAT_CGROUP_GC_PROFILE",
			"default", "latency_critical", "memory_constrained", "batch_etl", "adaptive"),
		GOGCSkippedReason: textField(lookup, "MICROFAT_CGROUP_GOGC_SKIPPED_REASON"),
	}
}

func readConfigured(lookup lookupEnv) ConfiguredEnvironment {
	return ConfiguredEnvironment{
		GOMEMLIMIT:       textField(lookup, "GOMEMLIMIT"),
		GOMAXPROCS:       textField(lookup, "GOMAXPROCS"),
		GOGC:             textField(lookup, "GOGC"),
		CPUPolicy:        textField(lookup, "MICROFAT_CPU_POLICY"),
		GCProfile:        textField(lookup, "MICROFAT_GC_PROFILE"),
		Autotune:         textField(lookup, "MICROFAT_AUTOTUNE"),
		DryRun:           textField(lookup, "MICROFAT_DRY_RUN"),
		MemoryRatio:      textField(lookup, "MICROFAT_MEM_RATIO"),
		LiveHeapEstimate: textField(lookup, "MICROFAT_LIVE_HEAP_ESTIMATE"),
	}
}
