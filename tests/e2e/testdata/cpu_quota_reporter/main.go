//go:build linux

// cpu_quota_reporter observes CPU policy in a freshly exec'd, test-owned cgroup.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strconv"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
	"github.com/EpicBlackWolfZ/microfat/runtimeinit"
)

const (
	sampleInterval = 100 * time.Millisecond
	fixtureTimeout = 30 * time.Second
	manualMaxProcs = 4
	bootstrapArgs  = 3
)

type observation struct {
	Phase       int                  `json:"phase"`
	GoVersion   string               `json:"go_version"`
	GOMAXPROCS  int                  `json:"gomaxprocs"`
	GOMEMLIMIT  int64                `json:"gomemlimit"`
	GOGC        uint64               `json:"gogc"`
	EnvMaxProcs string               `json:"env_maxprocs"`
	EnvGODEBUG  string               `json:"env_godebug"`
	Executable  string               `json:"executable"`
	Results     []runtimeinit.Result `json:"results,omitempty"`
}

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "--bootstrap" {
		if len(os.Args) != bootstrapArgs {
			return fmt.Errorf("bootstrap requires a child configuration")
		}
		var cfg system.ChildConfig
		if err := json.Unmarshal([]byte(os.Args[2]), &cfg); err != nil {
			return err
		}
		return system.ExecChild(cfg)
	}
	var results []runtimeinit.Result
	mode := os.Getenv("MICROFAT_CPU_QUOTA_FIXTURE")
	opts := []runtimeinit.Option{runtimeinit.WithCPUPolicy(runtimeinit.CPUPolicyNative)}
	switch mode {
	case "observe", "autoload":
	case "option":
		results = append(results, runtimeinit.AutoTune(opts...))
	case "environment":
		results = append(results, runtimeinit.AutoTune())
	case "repeated":
		results = append(results, runtimeinit.AutoTune(opts...), runtimeinit.AutoTune(opts...))
	case "setter":
		runtime.GOMAXPROCS(manualMaxProcs)
		results = append(results, runtimeinit.AutoTune(opts...))
	case "static-then-native":
		results = append(results, runtimeinit.AutoTune(), runtimeinit.AutoTune(opts...))
	case "dry-run":
		opts = append(opts, runtimeinit.WithDryRun(true))
		results = append(results, runtimeinit.AutoTune(opts...))
	default:
		return fmt.Errorf("unknown fixture mode %q", mode)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	deadline := time.NewTimer(fixtureTimeout)
	defer deadline.Stop()
	for {
		// #nosec G703 -- controller supplies its own temporary phase file to this isolated test fixture.
		data, err := os.ReadFile(os.Getenv("MICROFAT_CPU_QUOTA_PHASE"))
		if err != nil {
			return err
		}
		phase, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			return err
		}
		if phase < 0 {
			return nil
		}
		gc := []metrics.Sample{{Name: "/gc/gogc:percent"}}
		metrics.Read(gc)
		state := observation{Phase: phase, GoVersion: runtime.Version(), GOMAXPROCS: runtime.GOMAXPROCS(0),
			GOMEMLIMIT: debug.SetMemoryLimit(-1), GOGC: gc[0].Value.Uint64(),
			EnvMaxProcs: os.Getenv("GOMAXPROCS"), EnvGODEBUG: os.Getenv("GODEBUG"), Executable: executable, Results: results}
		if err := encoder.Encode(state); err != nil {
			return err
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return fmt.Errorf("quota fixture timed out waiting for controller")
		}
	}
}
