//go:build linux

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
	"github.com/EpicBlackWolfZ/microfat/runtimeinit"
	"github.com/stretchr/testify/require"
)

const (
	cpuQuotaModeEnv   = "MICROFAT_CPU_QUOTA_TESTS"
	cpuQuotaObserve   = "observe"
	cpuQuotaOption    = "option"
	cpuQuotaNative    = string(runtimeinit.CPUPolicyNative)
	cpuQuotaFixture   = "./testdata/cpu_quota_reporter"
	cpuQuotaMemory    = int64(1 << 30)
	cpuQuotaInitial   = int64(150000)
	cpuQuotaRaised    = int64(250000)
	cpuQuotaLowered   = int64(50000)
	cpuQuotaMinCPUs   = 3
	cpuQuotaPoll      = 50 * time.Millisecond
	cpuQuotaStability = 1500 * time.Millisecond
	cpuQuotaTimeout   = 25 * time.Second
	cpuQuotaGODEBUG   = "containermaxprocs=1,updatemaxprocs=1"
)

type cpuQuotaObservation struct {
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

type cpuQuotaCase struct {
	name, path, mode, execMode, policy, maxProcs, godebug string
	initial, raised, lowered                              int
	memory, dryRun                                        bool
}

func cpuQuotaProducts(t *testing.T) (string, string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	reporter, autoload := filepath.Join(dir, "reporter"), filepath.Join(dir, "autoload")
	require.NoError(t, compileBinary(cpuQuotaFixture, reporter, []string{envStatic}))
	require.NoError(t, compileBinaryWithFlags(cpuQuotaFixture, autoload, []string{envStatic}, "-tags=cpuquota_autoload"))
	full, minimal := filepath.Join(dir, "microfat-stub"), filepath.Join(dir, "microfat-stub-minimal")
	require.NoError(t, compileBinaryWithFlags(stubPackagePath, full, []string{envStatic}, "-tags="))
	require.NoError(t, compileBinaryWithFlags(stubPackagePath, minimal, []string{envStatic}, "-tags=minimal"))
	fat := make(map[string]string)
	for profile, stub := range map[string]string{launcherFullProfile: full, launcherMinimalProfile: minimal} {
		fat[profile] = filepath.Join(dir, "quota-"+profile+".fat")
		require.NoError(t, packBinary(cliPath, stub, "cpu-quota", fat[profile], map[string]string{currentHostLevel: reporter}))
	}
	return reporter, autoload, fat
}

func cpuQuotaCases(reporter, autoload string, fat map[string]string) []cpuQuotaCase {
	cases := []cpuQuotaCase{
		{name: "go-native-control", path: reporter, mode: cpuQuotaObserve, initial: 2, raised: 3, lowered: 2},
		{name: "runtimeinit-option", path: reporter, mode: cpuQuotaOption, initial: 2, raised: 3, lowered: 2, memory: true},
		{name: "runtimeinit-environment", path: reporter, mode: "environment", policy: cpuQuotaNative,
			initial: 2, raised: 3, lowered: 2, memory: true},
		{name: "runtimeinit-repeated", path: reporter, mode: "repeated", initial: 2, raised: 3, lowered: 2, memory: true},
		{name: "runtimeinit-explicit", path: reporter, mode: cpuQuotaOption, maxProcs: "4",
			initial: 4, raised: 4, lowered: 4, memory: true},
		{name: "runtimeinit-prior-setter", path: reporter, mode: "setter", initial: 4, raised: 4, lowered: 4, memory: true},
		{name: "runtimeinit-static-then-native", path: reporter, mode: "static-then-native",
			initial: 1, raised: 1, lowered: 1, memory: true},
		{name: "runtimeinit-dry-run", path: reporter, mode: "dry-run", initial: 2, raised: 3, lowered: 2, dryRun: true},
		{name: "runtimeinit-updates-disabled", path: reporter, mode: cpuQuotaOption, godebug: "containermaxprocs=1,updatemaxprocs=0",
			initial: 2, raised: 2, lowered: 2, memory: true},
		{name: "runtimeinit-quota-default-disabled", path: reporter, mode: cpuQuotaOption, godebug: "containermaxprocs=0,updatemaxprocs=1",
			initial: cpuQuotaMinCPUs, raised: cpuQuotaMinCPUs, lowered: cpuQuotaMinCPUs, memory: true},
		{name: "runtimeinit-autoload", path: autoload, mode: "autoload", policy: cpuQuotaNative,
			initial: 2, raised: 3, lowered: 2, memory: true},
	}
	for _, profile := range []string{launcherFullProfile, launcherMinimalProfile} {
		for _, mode := range []string{execModeMemfd, execModeCache} {
			prefix := profile + "/" + mode
			cases = append(cases,
				cpuQuotaCase{name: prefix + "/native", path: fat[profile], mode: cpuQuotaObserve, execMode: mode, policy: cpuQuotaNative,
					initial: 2, raised: 3, lowered: 2, memory: true},
				cpuQuotaCase{name: prefix + "/native-runtimeinit", path: fat[profile], mode: "repeated", execMode: mode, policy: cpuQuotaNative,
					initial: 2, raised: 3, lowered: 2, memory: true},
				cpuQuotaCase{name: prefix + "/static", path: fat[profile], mode: cpuQuotaObserve, execMode: mode,
					initial: 1, raised: 1, lowered: 1, memory: true},
				cpuQuotaCase{name: prefix + "/explicit", path: fat[profile], mode: cpuQuotaObserve, execMode: mode,
					policy: cpuQuotaNative, maxProcs: "4", initial: 4, raised: 4, lowered: 4, memory: true},
				cpuQuotaCase{name: prefix + "/dry-run", path: fat[profile], mode: cpuQuotaObserve, execMode: mode,
					policy: cpuQuotaNative, initial: 2, raised: 3, lowered: 2, dryRun: true})
		}
	}
	return cases
}

// TestCPUQuotaAdaptation changes only quotas in test-owned children of an explicitly delegated cgroup.
// A raw Go control must adapt before the Microfat cases can count as quota-change qualification.
func TestCPUQuotaAdaptation(t *testing.T) {
	if os.Getenv(cpuQuotaModeEnv) != "required" {
		t.Skip("run task test-cpu-quota with an isolated delegated cgroup v2 root")
	}
	root := os.Getenv("MICROFAT_BENCH_CGROUP_ROOT")
	require.NotEmpty(t, root, "required CPU qualification needs a delegated cgroup v2 root")
	require.NoError(t, system.CheckCgroupRoot(root, "v2"))
	cpus, err := system.SupportedAffinity(nil)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(cpus), cpuQuotaMinCPUs, "three allowed CPUs are needed to observe native quota adaptation")
	reporter, autoload, fat := cpuQuotaProducts(t)
	cases := cpuQuotaCases(reporter, autoload, fat)
	require.True(t, t.Run(cases[0].name, func(t *testing.T) {
		runCPUQuotaCase(t, root, cpus[:cpuQuotaMinCPUs], reporter, cases[0])
	}), "raw Go control must adapt before policy cases can qualify")
	for _, tc := range cases[1:] {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runCPUQuotaCase(t, root, cpus[:cpuQuotaMinCPUs], reporter, tc)
		})
	}
}

func cpuQuotaEnv(tc cpuQuotaCase, phase, cache string) []string {
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "MICROFAT_") || key == "GODEBUG" || key == "GOMAXPROCS" || key == "GOMEMLIMIT" || key == "GOGC" {
			continue
		}
		env = append(env, entry)
	}
	godebug := tc.godebug
	if godebug == "" {
		godebug = cpuQuotaGODEBUG
	}
	env = append(env, "GODEBUG="+godebug, "MICROFAT_CPU_QUOTA_FIXTURE="+tc.mode, "MICROFAT_CPU_QUOTA_PHASE="+phase,
		"MICROFAT_CPU_POLICY="+tc.policy, "MICROFAT_EXEC_MODE="+tc.execMode, "MICROFAT_GC_PROFILE=latency_critical",
		"MICROFAT_CACHE_DIR="+cache)
	if tc.maxProcs != "" {
		env = append(env, "GOMAXPROCS="+tc.maxProcs)
	}
	if tc.dryRun && tc.mode != "dry-run" {
		env = append(env, "MICROFAT_DRY_RUN=1")
	}
	return env
}

func runCPUQuotaCase(t *testing.T, root string, cpus []int, bootstrap string, tc cpuQuotaCase) {
	t.Helper()
	s, err := system.Prepare(system.Options{CgroupRoot: root, Version: "v2", CPUQuotaUS: cpuQuotaInitial,
		CPUPeriodUS: system.DefaultPeriod, MemoryBytes: cpuQuotaMemory, Affinity: cpus})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	require.Empty(t, system.Uncontrolled(s.Controls), "required isolated resource controls")
	require.Len(t, s.Paths, 1)
	phase := filepath.Join(t.TempDir(), "phase")
	writeCPUQuotaPhase(t, phase, 0)
	cfg, err := json.Marshal(system.ChildConfig{Path: tc.path, Env: cpuQuotaEnv(tc, phase, t.TempDir()), Affinity: cpus, Procs: s.Procs})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), cpuQuotaTimeout)
	t.Cleanup(cancel)
	child, err := process.Start(ctx, process.Spec{Path: bootstrap, Args: []string{"--bootstrap", string(cfg)}})
	require.NoError(t, err)
	t.Cleanup(func() {
		cancel()
		_ = child.Wait()
	})
	_, _, err = child.FirstLine(ctx)
	require.NoError(t, err, "fixture did not start: %s", child.Stderr())
	state := waitCPUQuotaPhase(t, child, 0, tc.initial, false)
	assertCPUQuotaState(t, tc, state)
	for i, step := range []struct {
		quota int64
		want  int
	}{{cpuQuotaRaised, tc.raised}, {cpuQuotaLowered, tc.lowered}} {
		quotaPath := filepath.Join(s.Paths[0], "cpu.max")
		quota := strconv.FormatInt(step.quota, 10) + " " + strconv.FormatInt(system.DefaultPeriod, 10)
		require.NoError(t, os.WriteFile(quotaPath, []byte(quota), privateFilePerm))
		effective, err := os.ReadFile(quotaPath)
		require.NoError(t, err)
		require.Equal(t, quota, strings.TrimSpace(string(effective)))
		phaseID := i + 1
		writeCPUQuotaPhase(t, phase, phaseID)
		state = waitCPUQuotaPhase(t, child, phaseID, step.want, tc.initial == tc.raised)
		assertCPUQuotaState(t, tc, state)
		t.Logf("go=%s quota=%s observed GOMAXPROCS=%d memory=%d GOGC=%d", state.GoVersion, quota,
			state.GOMAXPROCS, state.GOMEMLIMIT, state.GOGC)
	}
	writeCPUQuotaPhase(t, phase, -1)
	require.NoError(t, child.Wait(), "fixture output=%s stderr=%s", child.Stdout(), child.Stderr())
	for _, procs := range s.Procs {
		data, err := os.ReadFile(procs)
		require.NoError(t, err)
		require.Empty(t, strings.TrimSpace(string(data)), "fixture left cgroup members")
	}
}

func writeCPUQuotaPhase(t *testing.T, path string, phase int) {
	t.Helper()
	// Publish complete phase records; a concurrent reporter must never see a truncated file.
	require.NoError(t, os.WriteFile(path+".next", []byte(strconv.Itoa(phase)), privateFilePerm))
	require.NoError(t, os.Rename(path+".next", path))
}

func cpuQuotaObservations(t *testing.T, child *process.Child) []cpuQuotaObservation {
	t.Helper()
	lines := bytes.Split(child.Stdout(), []byte{'\n'})
	states := make([]cpuQuotaObservation, 0, len(lines))
	for _, line := range lines[:len(lines)-1] {
		var state cpuQuotaObservation
		require.NoError(t, json.Unmarshal(line, &state), "reporter line: %s", line)
		states = append(states, state)
	}
	return states
}

func waitCPUQuotaPhase(t *testing.T, child *process.Child, phase, want int, stable bool) cpuQuotaObservation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var since time.Time
	var last cpuQuotaObservation
	for time.Now().Before(deadline) {
		for _, state := range cpuQuotaObservations(t, child) {
			if state.Phase != phase {
				continue
			}
			last = state
			if stable {
				require.Equal(t, want, state.GOMAXPROCS, "fixed CPU setting changed during quota update")
			}
		}
		if last.Phase == phase && last.GOMAXPROCS == want {
			if !stable {
				return last
			}
			if since.IsZero() {
				since = time.Now()
			} else if time.Since(since) >= cpuQuotaStability {
				return last
			}
		}
		select {
		case <-child.Done():
			t.Fatalf("fixture exited while waiting for phase %d: %s; stderr=%s", phase, child.Stdout(), child.Stderr())
		case <-time.After(cpuQuotaPoll):
		}
	}
	t.Fatalf("phase %d did not reach GOMAXPROCS=%d; output=%s stderr=%s", phase, want, child.Stdout(), child.Stderr())
	return cpuQuotaObservation{}
}

func assertCPUQuotaState(t *testing.T, tc cpuQuotaCase, state cpuQuotaObservation) {
	t.Helper()
	if tc.memory {
		require.Positive(t, state.GOMEMLIMIT)
		require.Less(t, state.GOMEMLIMIT, cpuQuotaMemory)
		require.Equal(t, uint64(75), state.GOGC, "native CPU policy must preserve independent GC tuning")
	} else {
		require.Equal(t, int64(math.MaxInt64), state.GOMEMLIMIT)
		require.Equal(t, uint64(100), state.GOGC)
	}
	if tc.execMode == execModeMemfd {
		require.Contains(t, state.Executable, "memfd:")
	} else if tc.execMode == execModeCache {
		require.NotContains(t, state.Executable, "memfd:")
	}
	if tc.policy == cpuQuotaNative || tc.mode == cpuQuotaOption || tc.mode == "repeated" || tc.mode == "dry-run" {
		require.Equal(t, tc.maxProcs, state.EnvMaxProcs, "native mode injected GOMAXPROCS")
	}
	if tc.godebug != "" {
		require.Equal(t, tc.godebug, state.EnvGODEBUG, "native mode rewrote GODEBUG")
	}
	for _, result := range state.Results {
		if result.CPUPolicy == runtimeinit.CPUPolicyNative {
			require.Zero(t, result.GOMAXPROCS)
			require.False(t, result.MaxProcsApplied)
		}
	}
	if tc.mode == "repeated" {
		require.Len(t, state.Results, 2, "native initialization was not repeated")
	}
	if tc.mode == "dry-run" {
		require.Len(t, state.Results, 1)
		require.True(t, state.Results[0].DryRun)
		require.False(t, state.Results[0].MemLimitApplied)
		require.False(t, state.Results[0].GOGCApplied)
	}
}
