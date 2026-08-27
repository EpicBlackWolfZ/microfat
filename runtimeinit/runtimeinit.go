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
	MemLimitApplied  bool    `json:"mem_limit_applied"`
	MaxProcsApplied  bool    `json:"max_procs_applied"`
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
	MemLimitApplied     bool    `json:"mem_limit_applied"`
	MaxProcsApplied     bool    `json:"max_procs_applied"`
	SkippedReason       string  `json:"skipped_reason,omitempty"`
}

func init() {
	_ = AutoTune()
}

// AutoTune inspects container cgroup limits and configures GOMEMLIMIT and GOMAXPROCS.
//
// Precedence rules:
//  1. If MICROFAT_AUTOTUNE is set to "0" or "false", tuning is skipped entirely.
//  2. If GOMEMLIMIT is already set in the environment, debug.SetMemoryLimit is skipped to respect explicit user configuration.
//  3. If GOMAXPROCS is already set in the environment, runtime.GOMAXPROCS is skipped.
//  4. If MICROFAT_MEM_RATIO is defined (e.g. "0.85"), it overrides the default 90% memory limit calculation ratio.
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

	// 3. Resolve tuning plan
	plan := cgroup.ResolveTuningPlan(limits, getenvFunc(format.EnvMemRatio), cfg.memoryRatio, cfg.minHeadroomBytes)

	// 4. Configure GOMEMLIMIT
	if getenvFunc("GOMEMLIMIT") != "" {
		res.MemLimitApplied = false
	} else if plan.GOMEMLIMITBytes > 0 {
		setMemoryLimitFunc(plan.GOMEMLIMITBytes)
		res.GOMEMLIMIT = plan.GOMEMLIMITBytes
		res.MemLimitApplied = true
	}

	// 5. Configure GOMAXPROCS
	if getenvFunc("GOMAXPROCS") != "" {
		res.MaxProcsApplied = false
	} else if plan.GOMAXPROCS > 0 {
		setMaxProcsFunc(plan.GOMAXPROCS)
		res.GOMAXPROCS = plan.GOMAXPROCS
		res.MaxProcsApplied = true
	}

	logResult(cfg, res)
	return res
}

func logResult(cfg *config, res Result) {
	if cfg.logger != nil {
		cfg.logger("cgroup_version=%d gomemlimit=%d gomaxprocs=%d applied_mem=%t applied_cpu=%t skipped=%q",
			res.CgroupVersion, res.GOMEMLIMIT, res.GOMAXPROCS, res.MemLimitApplied, res.MaxProcsApplied, res.SkippedReason)
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
			MemLimitApplied:     res.MemLimitApplied,
			MaxProcsApplied:     res.MaxProcsApplied,
			SkippedReason:       res.SkippedReason,
		}
		if b, err := json.Marshal(telem); err == nil {
			_, _ = fmt.Fprintf(stderrWriter, "[microfat] %s\n", string(b))
		}
		return
	}

	if debugOpt == envValEnabledOne || strings.EqualFold(debugOpt, envValEnabledTrue) {
		_, _ = fmt.Fprintf(stderrWriter,
			"[microfat:runtimeinit] cgroup_v=%d mem_bytes=%d cpu_quota=%.2f gomemlimit=%dB (%t) gomaxprocs=%d (%t) reason=%q\n",
			res.CgroupVersion, res.MemoryLimitBytes, res.CPUQuota,
			res.GOMEMLIMIT, res.MemLimitApplied,
			res.GOMAXPROCS, res.MaxProcsApplied,
			res.SkippedReason,
		)
	}
}
