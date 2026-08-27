// Package runtimeinit provides automated and programmatic container cgroup resource auto-tuning
// (GOMEMLIMIT and GOMAXPROCS) for standalone Go services.
package runtimeinit

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

const (
	envValDisabledZero  = "0"
	envValDisabledFalse = "false"
	envValEnabledOne    = "1"
	envValEnabledTrue   = "true"
	envValLogJSON       = "json"
	eventRuntimeInit    = "runtimeinit"
)

var (
	setMemoryLimitFunc = debug.SetMemoryLimit
	setMaxProcsFunc    = runtime.GOMAXPROCS
	setGCPercentFunc   = debug.SetGCPercent
	readLimitsFunc     = cgroup.ReadLimits
	readLimitsFromFunc = cgroup.ReadLimitsFrom
	getenvFunc         = os.Getenv
	stderrWriter       io.Writer = os.Stderr
)

// Result contains the outcome and resolved parameters of a container auto-tuning operation.
type Result struct {
	CgroupVersion    int     `json:"cgroup_version"`
	MemoryLimitBytes int64   `json:"memory_limit_bytes"`
	CPUQuota         float64 `json:"cpu_quota"`
	GOMEMLIMIT       int64   `json:"gomemlimit,omitempty"`
	GOMAXPROCS       int     `json:"gomaxprocs,omitempty"`
	GOGC             int     `json:"gogc,omitempty"`
	ProfileApplied   string  `json:"profile_applied,omitempty"`
	MemLimitApplied  bool    `json:"mem_limit_applied"`
	MaxProcsApplied  bool    `json:"max_procs_applied"`
	GOGCApplied      bool    `json:"gogc_applied"`
	SkippedReason    string  `json:"skipped_reason,omitempty"`
}

// Telemetry records structured JSON telemetry emitted during runtimeinit auto-tuning.
type Telemetry struct {
	Event               string  `json:"event"`
	TimestampUnixNano   int64   `json:"timestamp_unix_nano"`
	CgroupVersion       int     `json:"cgroup_version"`
	CgroupMemLimitBytes int64   `json:"cgroup_mem_limit_bytes"`
	CgroupCPUQuota      float64 `json:"cgroup_cpu_quota"`
	GOMEMLIMIT          string  `json:"gomemlimit,omitempty"`
	GOMAXPROCS          string  `json:"gomaxprocs,omitempty"`
	GOGC                string  `json:"gogc,omitempty"`
	ProfileApplied      string  `json:"profile_applied,omitempty"`
	MemLimitApplied     bool    `json:"mem_limit_applied"`
	MaxProcsApplied     bool    `json:"max_procs_applied"`
	GOGCApplied         bool    `json:"gogc_applied"`
	SkippedReason       string  `json:"skipped_reason,omitempty"`
}

func init() {
	_ = AutoTune()
}

// AutoTune inspects container cgroup limits and configures GOMEMLIMIT, GOMAXPROCS, and GOGC.
//
// Precedence rules:
//  1. If MICROFAT_AUTOTUNE is set to "0" or "false", tuning is skipped entirely.
//  2. If GOMEMLIMIT is already set in the environment, debug.SetMemoryLimit is skipped to respect explicit user configuration.
//  3. If GOMAXPROCS is already set in the environment, runtime.GOMAXPROCS is skipped.
//  4. If GOGC is already set in the environment, debug.SetGCPercent is skipped.
//  5. MICROFAT_GC_PROFILE / MICROFAT_LIVE_HEAP_ESTIMATE env variables take precedence over in-code defaults.
//  6. If MICROFAT_MEM_RATIO is defined (e.g. "0.85"), it overrides the default memory limit calculation ratio.
func AutoTune(opts ...Option) Result {
	cfg := defaultConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}

	// 1. Check if auto-tuning is explicitly disabled
	autoTuneEnv := getenvFunc(format.EnvAutotune)
	if autoTuneEnv == envValDisabledZero || strings.EqualFold(autoTuneEnv, envValDisabledFalse) {
		res := Result{SkippedReason: "auto-tuning disabled by " + format.EnvAutotune}
		logResult(cfg, res)
		return res
	}

	// 2. Discover cgroup resource limits
	var limits cgroup.Limits
	var err error
	if cfg.cgroupRoot != "" {
		limits, err = readLimitsFromFunc(cfg.cgroupRoot)
	} else {
		limits, err = readLimitsFunc()
	}

	if err != nil || limits.CgroupVersion == cgroup.VersionUnknown {
		reason := "cgroup resource limits not detected"
		if err != nil {
			reason = fmt.Sprintf("cgroup inspection failed: %v", err)
		}
		res := Result{
			CgroupVersion: limits.CgroupVersion,
			SkippedReason: reason,
		}
		logResult(cfg, res)
		return res
	}

	res := Result{
		CgroupVersion:    limits.CgroupVersion,
		MemoryLimitBytes: limits.MemoryLimitBytes,
		CPUQuota:         limits.CPUQuota,
	}

	// 3. Resolve active profile, live heap estimate, and tuning plan
	activeProfile, activeLiveHeap := resolveProfileAndLiveHeap(cfg)
	plan := cgroup.ResolveTuningPlanWithProfile(
		limits,
		getenvFunc(format.EnvMemRatio),
		cfg.memoryRatio,
		cfg.minHeadroomBytes,
		activeProfile,
		activeLiveHeap,
	)

	if cfg.explicitGOGC != nil {
		plan.GOGC = *cfg.explicitGOGC
		plan.GOGCApplied = true
		if plan.GOGC == cgroup.DefaultBatchETLGOGC {
			plan.GOGCStr = "off"
		} else {
			plan.GOGCStr = strconv.Itoa(plan.GOGC)
		}
	}

	// 4. Apply configured limits and GC settings
	applyTuningPlan(plan, activeProfile, activeLiveHeap, &res)

	logResult(cfg, res)
	return res
}

func resolveProfileAndLiveHeap(cfg *config) (Profile, int64) {
	activeProfile := cfg.profile
	if envProfile := getenvFunc(format.EnvGCProfile); envProfile != "" {
		if p, pErr := cgroup.ParseGCProfile(envProfile); pErr == nil {
			activeProfile = p
		}
	}

	activeLiveHeap := cfg.liveHeapEstimateBytes
	if envHeap := getenvFunc(format.EnvLiveHeapEstimate); envHeap != "" {
		if h, hErr := cgroup.ParseByteSize(envHeap); hErr == nil && h > 0 {
			activeLiveHeap = h
		}
	}
	return activeProfile, activeLiveHeap
}

func applyTuningPlan(plan cgroup.TuningPlan, activeProfile Profile, activeLiveHeap int64, res *Result) {
	// Configure GOMEMLIMIT
	if getenvFunc("GOMEMLIMIT") != "" {
		res.MemLimitApplied = false
	} else if plan.GOMEMLIMITBytes > 0 {
		setMemoryLimitFunc(plan.GOMEMLIMITBytes)
		res.GOMEMLIMIT = plan.GOMEMLIMITBytes
		res.MemLimitApplied = true
	}

	// Configure GOMAXPROCS
	if getenvFunc("GOMAXPROCS") != "" {
		res.MaxProcsApplied = false
	} else if plan.GOMAXPROCS > 0 {
		setMaxProcsFunc(plan.GOMAXPROCS)
		res.GOMAXPROCS = plan.GOMAXPROCS
		res.MaxProcsApplied = true
	}

	// Configure GOGC
	switch {
	case getenvFunc("GOGC") != "":
		res.GOGCApplied = false
	case plan.GOGCApplied:
		setGCPercentFunc(plan.GOGC)
		res.GOGC = plan.GOGC
		res.GOGCApplied = true
		if activeProfile != ProfileDefault {
			res.ProfileApplied = string(activeProfile)
		}
	case activeProfile == ProfileAdaptive && activeLiveHeap <= 0:
		res.ProfileApplied = string(activeProfile)
		res.GOGCApplied = false
		if res.SkippedReason == "" {
			res.SkippedReason = "adaptive GOGC tuning skipped (missing live heap estimate)"
		}
	}
}

func logResult(cfg *config, res Result) {
	var gogcStr string
	if res.GOGCApplied {
		if res.GOGC == cgroup.DefaultBatchETLGOGC {
			gogcStr = "off"
		} else {
			gogcStr = strconv.Itoa(res.GOGC)
		}
	}

	if cfg.logger != nil {
		cfg.logger(
			"cgroup_version=%d gomemlimit=%d gomaxprocs=%d gogc=%s applied_mem=%t applied_cpu=%t applied_gogc=%t profile=%q skipped=%q",
			res.CgroupVersion, res.GOMEMLIMIT, res.GOMAXPROCS, gogcStr,
			res.MemLimitApplied, res.MaxProcsApplied, res.GOGCApplied, res.ProfileApplied, res.SkippedReason,
		)
		return
	}

	logOpt := getenvFunc(format.EnvLog)
	debugOpt := getenvFunc(format.EnvDebug)

	if strings.EqualFold(logOpt, envValLogJSON) {
		var memLimitStr, maxProcsStr string
		if res.GOMEMLIMIT > 0 {
			memLimitStr = fmt.Sprintf("%dB", res.GOMEMLIMIT)
		}
		if res.GOMAXPROCS > 0 {
			maxProcsStr = strconv.Itoa(res.GOMAXPROCS)
		}
		telem := Telemetry{
			Event:               eventRuntimeInit,
			TimestampUnixNano:   time.Now().UnixNano(),
			CgroupVersion:       res.CgroupVersion,
			CgroupMemLimitBytes: res.MemoryLimitBytes,
			CgroupCPUQuota:      res.CPUQuota,
			GOMEMLIMIT:          memLimitStr,
			GOMAXPROCS:          maxProcsStr,
			GOGC:                gogcStr,
			ProfileApplied:      res.ProfileApplied,
			MemLimitApplied:     res.MemLimitApplied,
			MaxProcsApplied:     res.MaxProcsApplied,
			GOGCApplied:         res.GOGCApplied,
			SkippedReason:       res.SkippedReason,
		}
		if b, err := json.Marshal(telem); err == nil {
			_, _ = fmt.Fprintf(stderrWriter, "[microfat] %s\n", string(b))
		}
		return
	}

	if debugOpt == envValEnabledOne || strings.EqualFold(debugOpt, envValEnabledTrue) {
		_, _ = fmt.Fprintf(stderrWriter,
			"[microfat:runtimeinit] cgroup_v=%d mem_bytes=%d cpu_quota=%.2f gomemlimit=%dB (%t) "+
				"gomaxprocs=%d (%t) gogc=%s (%t) profile=%s reason=%q\n",
			res.CgroupVersion, res.MemoryLimitBytes, res.CPUQuota,
			res.GOMEMLIMIT, res.MemLimitApplied,
			res.GOMAXPROCS, res.MaxProcsApplied,
			gogcStr, res.GOGCApplied,
			res.ProfileApplied,
			res.SkippedReason,
		)
	}
}
