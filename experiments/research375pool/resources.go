package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	resourceUnitPrefix     = "microfat-poc375pool-"
	resourceUnitSuffix     = ".service"
	resourceTelemetryLimit = 64 << 10
	resourceOutputLimit    = 1 << 20
	resourceMinimumMemory  = 16 << 20
	resourceMaximumMemory  = 1 << 30
	resourceMaximumCPU     = 400
	resourceMaximumProcs   = 32
	resourceMaximumArgs    = 128
	resourceMaximumUnit    = 128
	resourcePollInterval   = 10 * time.Millisecond
	resourceCommandLimit   = 5 * time.Second
	resourceTrialLimit     = 3 * time.Minute
	resourcePercentScale   = 100
	resourceNSPerUS        = 1000
	resourceUSPerSecond    = 1000000
	resourceSignalOffset   = 128
	resourceExitedCode     = 1
	resourceKilledCode     = 2
	resourceDumpedCode     = 3
	resourceCounterFields  = 2
	resourcePrivateFile    = 0o600
)

var (
	ErrResourceConfig      = errors.New("invalid resource trial configuration")
	ErrResourceUnavailable = errors.New("systemd user resource controls unavailable")
	ErrResourceSetup       = errors.New("resource trial setup failed")
	ErrResourceTelemetry   = errors.New("resource trial telemetry unavailable or mismatched")
	ErrResourceTimeout     = errors.New("resource trial deadline exceeded")
	ErrResourceCleanup     = errors.New("owned resource trial cleanup failed")
)

type trialResourceConfig struct {
	UnitName        string
	CPUQuotaPercent int
	MemoryMaxBytes  int64
	GOMAXPROCS      int
	GoMemLimitBytes int64
	TrialDir        string
}

type resourceControls struct {
	MemoryMaxBytes     uint64 `json:"memory_max_bytes"`
	MemorySwapMaxBytes uint64 `json:"memory_swap_max_bytes"`
	CPUQuotaUsec       uint64 `json:"cpu_quota_usec"`
	CPUPeriodUsec      uint64 `json:"cpu_period_usec"`
}

// Cgroup peaks include worker startup, allocator/file-cache charges and the small
// stop-post keeper. They are not worker RSS. The worker separately reports rusage.
type resourceTrialResult struct {
	UnitName            string            `json:"unit_name"`
	Stdout              string            `json:"stdout"`
	Stderr              string            `json:"stderr"`
	SystemdResult       string            `json:"systemd_result"`
	ExitCode            int               `json:"exit_code"`
	Signal              int               `json:"signal"`
	WallNS              int64             `json:"orchestration_wall_ns"`
	OOMKilled           bool              `json:"oom_killed"`
	TelemetryAvailable  bool              `json:"telemetry_available"`
	OutputTruncated     bool              `json:"output_truncated"`
	OOMKills            uint64            `json:"oom_kills"`
	MemoryPeakBytes     uint64            `json:"memory_peak_bytes"`
	CPUUsageUsec        uint64            `json:"cpu_usage_usec"`
	CPUThrottledUsec    uint64            `json:"cpu_throttled_usec"`
	CPUThrottledPeriods uint64            `json:"cpu_throttled_periods"`
	ActualCG            resourceControls  `json:"actual_cgroup_controls"`
	MemoryEvents        map[string]uint64 `json:"memory_events"`
	CPUStats            map[string]uint64 `json:"cpu_stats"`
	TelemetryError      string            `json:"telemetry_error,omitempty"`
	CleanupError        string            `json:"cleanup_error,omitempty"`
	RetainedLogDir      string            `json:"retained_log_dir,omitempty"`
}

type resourceBackend struct {
	command    func(context.Context, string, ...string) ([]byte, error)
	read       func(string, int64) ([]byte, error)
	cgroupRoot string
}

// RunResourceTrial starts one fresh transient unit and retains its cgroup through
// ExecStopPost until counters are read. Child exit/OOM is data, not a setup error.
// Only the newly created unit and private log directory are cleaned up.
func RunResourceTrial(
	ctx context.Context, config trialResourceConfig, command string, args, env []string,
) (resourceTrialResult, error) {
	backend := resourceBackend{command: resourceCommand, read: resourceRead, cgroupRoot: "/sys/fs/cgroup"}
	return runResourceTrial(ctx, config, command, args, env, backend)
}

func runResourceTrial(
	ctx context.Context, config trialResourceConfig, command string, args, env []string, backend resourceBackend,
) (result resourceTrialResult, resultErr error) {
	result.ExitCode = -1
	if err := validateResourceConfig(config, command, args, env); err != nil {
		return result, err
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return result, fmt.Errorf("%w: unit identity: %v", ErrResourceSetup, err)
	}
	base := resourceUnitPrefix
	if config.UnitName != "" {
		base = strings.TrimSuffix(config.UnitName, resourceUnitSuffix) + "-"
	}
	config.UnitName = base + hex.EncodeToString(suffix[:]) + resourceUnitSuffix
	result.UnitName = config.UnitName
	directory, err := os.MkdirTemp(config.TrialDir, "microfat-resource-")
	if err != nil {
		return result, fmt.Errorf("%w: private trial directory: %v", ErrResourceSetup, err)
	}
	removeLogs := true
	defer func() {
		if removeLogs {
			_ = os.RemoveAll(directory) // Only this call's private directory.
		} else {
			result.RetainedLogDir = directory
		}
	}()
	stdout, stderr := filepath.Join(directory, "stdout"), filepath.Join(directory, "stderr")
	for _, path := range []string{stdout, stderr} {
		if err := os.WriteFile(path, nil, resourcePrivateFile); err != nil {
			return result, fmt.Errorf("%w: private output: %v", ErrResourceSetup, err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, resourceTrialLimit)
	defer cancel()
	started := time.Now()
	cleanupNeeded := true
	defer func() {
		if cleanupNeeded {
			if err := cleanupResourceUnit(config.UnitName, backend); err != nil {
				removeLogs = false
				result.CleanupError = err.Error()
				resultErr = errors.Join(resultErr, err)
			}
		}
	}()
	if output, err := backend.command(ctx, "systemd-run", resourceStartArgs(config, command, args, env, stdout, stderr)...); err != nil {
		kind := ErrResourceSetup
		switch {
		case errors.Is(err, exec.ErrNotFound):
			cleanupNeeded, kind = false, ErrResourceUnavailable // No control process ran; no unit could have been created.
		case strings.Contains(string(output), "Failed to connect to"):
			kind = ErrResourceUnavailable
		case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
			kind = ErrResourceTimeout
		}
		return result, fmt.Errorf("%w: %v: %s", kind, err, strings.TrimSpace(string(output)))
	}
	status, waitErr := waitResourceUnit(ctx, config.UnitName, backend)
	result.WallNS = time.Since(started).Nanoseconds()
	applyResourceStatus(&result, status)
	result.Stdout, result.OutputTruncated, err = readResourceOutput(stdout)
	if err == nil {
		var truncated bool
		result.Stderr, truncated, err = readResourceOutput(stderr)
		result.OutputTruncated = result.OutputTruncated || truncated
	}
	if err != nil {
		return result, fmt.Errorf("%w: child output: %v", ErrResourceTelemetry, err)
	}
	if waitErr != nil {
		return result, waitErr
	}
	if status["ExecMainCode"] == "0" {
		return result, fmt.Errorf("%w: worker did not execute (%s)", ErrResourceSetup, result.SystemdResult)
	}
	if err := collectResourceTelemetry(&result, status["ControlGroup"], config, backend); err != nil {
		result.TelemetryError = err.Error()
		if !result.OOMKilled {
			return result, err
		}
		// Cached properties remain historical evidence when an OOM also prevents
		// the keeper from starting. Never invent absent raw counters or controls.
		result.MemoryPeakBytes, _ = strconv.ParseUint(status["MemoryPeak"], 10, 64)
		cpuNS, _ := strconv.ParseUint(status["CPUUsageNSec"], 10, 64)
		result.CPUUsageUsec = cpuNS / resourceNSPerUS
	}
	return result, nil
}

func validateResourceConfig(config trialResourceConfig, command string, args, env []string) error {
	if config.MemoryMaxBytes < resourceMinimumMemory || config.MemoryMaxBytes > resourceMaximumMemory ||
		config.CPUQuotaPercent < 1 || config.CPUQuotaPercent > resourceMaximumCPU ||
		config.GOMAXPROCS < 1 || config.GOMAXPROCS > resourceMaximumProcs ||
		config.GoMemLimitBytes < 1 || config.GoMemLimitBytes > config.MemoryMaxBytes ||
		!filepath.IsAbs(command) || len(args) > resourceMaximumArgs || len(env) > resourceMaximumArgs {
		return ErrResourceConfig
	}
	if config.TrialDir != "" && !filepath.IsAbs(config.TrialDir) {
		return ErrResourceConfig
	}
	if config.UnitName != "" && !validResourceUnitName(config.UnitName) {
		return ErrResourceConfig
	}
	for _, value := range append(append([]string{command}, args...), env...) {
		if len(value) > resourceTelemetryLimit || strings.IndexByte(value, 0) >= 0 {
			return ErrResourceConfig
		}
	}
	for _, entry := range env {
		key, _, found := strings.Cut(entry, "=")
		if !found || key == "" || strings.ContainsAny(key, "\n\r") {
			return ErrResourceConfig
		}
	}
	return nil
}

func validResourceUnitName(name string) bool {
	if len(name) > resourceMaximumUnit || !strings.HasPrefix(name, resourceUnitPrefix) || !strings.HasSuffix(name, resourceUnitSuffix) {
		return false
	}
	for _, character := range strings.TrimSuffix(name, resourceUnitSuffix) {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func resourceStartArgs(config trialResourceConfig, command string, args, env []string, stdout, stderr string) []string {
	out := []string{
		"--user", "--quiet", "--no-block", "--unit=" + config.UnitName,
		"--property=Type=exec", "--property=MemoryAccounting=yes", "--property=MemorySwapMax=0",
		"--property=MemoryMax=" + strconv.FormatInt(config.MemoryMaxBytes, 10),
		"--property=CPUQuota=" + strconv.Itoa(config.CPUQuotaPercent) + "%", "--property=CPUQuotaPeriodSec=100ms",
		"--property=OOMPolicy=continue", "--property=TasksMax=128",
		"--property=ExecStopPost=/usr/bin/sleep infinity", "--property=TimeoutStopSec=30s", "--property=UMask=0077",
		"--property=StandardOutput=append:" + stdout, "--property=StandardError=append:" + stderr,
	}
	for _, entry := range env {
		if !strings.HasPrefix(entry, "GOMAXPROCS=") && !strings.HasPrefix(entry, "GOMEMLIMIT=") {
			out = append(out, "--setenv="+entry)
		}
	}
	out = append(out, "--setenv=GOMAXPROCS="+strconv.Itoa(config.GOMAXPROCS),
		"--setenv=GOMEMLIMIT="+strconv.FormatInt(config.GoMemLimitBytes, 10)+"B", "--", command)
	return append(out, args...)
}

func waitResourceUnit(ctx context.Context, unit string, backend resourceBackend) (map[string]string, error) {
	for {
		output, err := backend.command(ctx, "systemctl", "--user", "show", unit,
			"--property=LoadState,ActiveState,SubState,ExecMainCode,ExecMainStatus,Result,ControlGroup,MemoryPeak,CPUUsageNSec")
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("%w: %v", ErrResourceTimeout, ctx.Err())
			}
			return nil, fmt.Errorf("%w: unit state: %v", ErrResourceSetup, err)
		}
		status, err := parseResourceProperties(output)
		if err != nil {
			return status, err
		}
		if status["LoadState"] == "not-found" {
			return status, fmt.Errorf("%w: new unit disappeared before its result was read", ErrResourceSetup)
		}
		if status["SubState"] == "stop-post" || status["ActiveState"] == "failed" || status["ActiveState"] == "inactive" {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return status, fmt.Errorf("%w: %v", ErrResourceTimeout, ctx.Err())
		case <-time.After(resourcePollInterval):
		}
	}
}

func applyResourceStatus(result *resourceTrialResult, status map[string]string) {
	result.SystemdResult = status["Result"]
	code, _ := strconv.Atoi(status["ExecMainCode"])
	value, _ := strconv.Atoi(status["ExecMainStatus"])
	switch code {
	case resourceExitedCode:
		result.ExitCode = value
	case resourceKilledCode, resourceDumpedCode:
		result.Signal, result.ExitCode = value, resourceSignalOffset+value
	}
	result.OOMKilled = result.SystemdResult == "oom-kill"
}

func parseResourceProperties(data []byte) (map[string]string, error) {
	properties := make(map[string]string)
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found || key == "" {
			return properties, fmt.Errorf("%w: malformed unit properties", ErrResourceTelemetry)
		}
		if _, exists := properties[key]; exists {
			return properties, fmt.Errorf("%w: duplicate unit property", ErrResourceTelemetry)
		}
		properties[key] = value
	}
	return properties, nil
}

func collectResourceTelemetry(result *resourceTrialResult, group string, config trialResourceConfig, backend resourceBackend) error {
	if group == "" || !filepath.IsAbs(group) || filepath.Clean(group) != group || !strings.HasSuffix(group, "/"+config.UnitName) {
		return fmt.Errorf("%w: no owned live cgroup", ErrResourceTelemetry)
	}
	read := func(name string) ([]byte, error) {
		return backend.read(filepath.Join(backend.cgroupRoot, group, name), resourceTelemetryLimit)
	}
	values := make(map[string][]byte)
	for _, name := range []string{"memory.max", "memory.swap.max", "memory.peak", "memory.events", "cpu.max", "cpu.stat"} {
		value, err := read(name)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrResourceTelemetry, name, err)
		}
		values[name] = value
	}
	controls, err := parseResourceControls(values["memory.max"], values["memory.swap.max"], values["cpu.max"])
	if err != nil {
		return err
	}
	expectedMemory := uint64(config.MemoryMaxBytes) // #nosec G115 -- Config is validated positive and <= 1 GiB before execution.
	expectedCPU := uint64(config.CPUQuotaPercent)   // #nosec G115 -- Config is validated positive and <= 400 before execution.
	if controls.MemoryMaxBytes != expectedMemory || controls.MemorySwapMaxBytes != 0 ||
		controls.CPUQuotaUsec*resourcePercentScale != controls.CPUPeriodUsec*expectedCPU {
		return fmt.Errorf("%w: actual hard controls differ from requested controls", ErrResourceTelemetry)
	}
	peak, err := strconv.ParseUint(strings.TrimSpace(string(values["memory.peak"])), 10, 64)
	if err != nil {
		return fmt.Errorf("%w: invalid memory peak", ErrResourceTelemetry)
	}
	events, err := parseResourceCounters(values["memory.events"])
	if err != nil {
		return err
	}
	cpu, err := parseResourceCounters(values["cpu.stat"])
	if err != nil {
		return err
	}
	for _, key := range []string{"usage_usec", "nr_throttled", "throttled_usec"} {
		if _, ok := cpu[key]; !ok {
			return fmt.Errorf("%w: missing CPU counter %s", ErrResourceTelemetry, key)
		}
	}
	if _, ok := events["oom_kill"]; !ok {
		return fmt.Errorf("%w: missing OOM kill counter", ErrResourceTelemetry)
	}
	result.ActualCG, result.MemoryPeakBytes, result.MemoryEvents, result.CPUStats = controls, peak, events, cpu
	result.OOMKills, result.OOMKilled = events["oom_kill"], result.OOMKilled || events["oom_kill"] > 0
	result.CPUUsageUsec, result.CPUThrottledUsec = cpu["usage_usec"], cpu["throttled_usec"]
	result.CPUThrottledPeriods, result.TelemetryAvailable = cpu["nr_throttled"], true
	return nil
}

func parseResourceControls(memory, swap, cpu []byte) (resourceControls, error) {
	var result resourceControls
	memoryValue, memoryErr := strconv.ParseUint(strings.TrimSpace(string(memory)), 10, 64)
	swapValue, swapErr := strconv.ParseUint(strings.TrimSpace(string(swap)), 10, 64)
	fields := strings.Fields(string(cpu))
	if memoryErr != nil || swapErr != nil || len(fields) != 2 {
		return result, fmt.Errorf("%w: invalid hard control readback", ErrResourceTelemetry)
	}
	quota, quotaErr := strconv.ParseUint(fields[0], 10, 64)
	period, periodErr := strconv.ParseUint(fields[1], 10, 64)
	if quotaErr != nil || periodErr != nil || quota == 0 || quota > resourceMaximumCPU*resourceUSPerSecond ||
		period == 0 || period > resourceUSPerSecond {
		return result, fmt.Errorf("%w: invalid finite CPU quota", ErrResourceTelemetry)
	}
	return resourceControls{MemoryMaxBytes: memoryValue, MemorySwapMaxBytes: swapValue, CPUQuotaUsec: quota, CPUPeriodUsec: period}, nil
}

func parseResourceCounters(data []byte) (map[string]uint64, error) {
	result := make(map[string]uint64)
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != resourceCounterFields {
			return result, fmt.Errorf("%w: malformed cgroup counter", ErrResourceTelemetry)
		}
		if _, exists := result[fields[0]]; exists {
			return result, fmt.Errorf("%w: duplicate cgroup counter", ErrResourceTelemetry)
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return result, fmt.Errorf("%w: invalid cgroup counter", ErrResourceTelemetry)
		}
		result[fields[0]] = value
	}
	return result, nil
}

func resourceCommand(ctx context.Context, command string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, resourceCommandLimit)
	defer cancel()
	process := exec.CommandContext(ctx, command, args...) // #nosec G204 -- fixed control tools; arguments never enter a shell.
	output := &resourceLimitedWriter{remaining: resourceTelemetryLimit}
	process.Stdout, process.Stderr = output, output
	err := process.Run()
	return output.data, err
}

type resourceLimitedWriter struct {
	data      []byte
	remaining int
}

func (writer *resourceLimitedWriter) Write(data []byte) (int, error) {
	size := len(data)
	kept := min(writer.remaining, size)
	writer.data = append(writer.data, data[:kept]...)
	writer.remaining -= kept
	return size, nil
}

func resourceRead(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path) // #nosec G304 -- own cgroup telemetry path, validated before use; virtual files have zero stat size.
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: telemetry byte cap exceeded", ErrResourceTelemetry)
	}
	return data, nil
}

func readResourceOutput(path string) (string, bool, error) {
	file, err := os.Open(path) // #nosec G304 -- private output file created by this invocation.
	if err != nil {
		return "", false, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, resourceOutputLimit+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return "", false, err
	}
	return string(data[:min(len(data), resourceOutputLimit)]), len(data) > resourceOutputLimit, nil
}

func cleanupResourceUnit(unit string, backend resourceBackend) error {
	ctx, cancel := context.WithTimeout(context.Background(), resourceCommandLimit)
	defer cancel()
	// Kill the stop-post keeper (or timed-out worker) only after status/counters
	// were copied. Never discover units by wildcard or touch another unit.
	_, killErr := backend.command(ctx, "systemctl", "--user", "kill", "--kill-whom=all", "--signal=SIGKILL", unit)
	stopOutput, stopErr := backend.command(ctx, "systemctl", "--user", "stop", unit)
	resetOutput, resetErr := backend.command(ctx, "systemctl", "--user", "reset-failed", unit)
	if (stopErr != nil && !resourceUnitAbsent(stopOutput)) || (resetErr != nil && !resourceUnitAbsent(resetOutput)) {
		return fmt.Errorf("%w: %v", ErrResourceCleanup, errors.Join(killErr, stopErr, resetErr))
	}
	return nil
}

func resourceUnitAbsent(output []byte) bool {
	message := string(output)
	return strings.Contains(message, "not loaded") || strings.Contains(message, "could not be found") ||
		strings.Contains(message, "LoadState=not-found")
}
