//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"golang.org/x/sys/unix"
)

const (
	privateCacheDirMode = 0o700
	privateExecMode     = 0o700
	extraEnvCapacity    = 16
)

var (
	execveFunc           = syscall.Exec
	memfdCreateFunc      = unix.MemfdCreate
	readCgroupLimitsFunc = cgroup.ReadLimits
	resolveCacheDirFunc  = format.ResolveCacheDir
	userHomeDirFunc      = os.UserHomeDir
)

// executeVariant runs the selected variant payload in-memory using Linux memfd_create,
// falling back to user cache execution if memfd is restricted or if cache mode is explicitly requested.
func executeVariant(
	selfFile *os.File,
	entry *format.VariantEntry,
	args []string,
	baseEnv []string,
	hostInfo microarch.Info,
	policyRes microarch.PolicyResult,
	startTime time.Time,
) error {
	// Check if cache dispatch mode is explicitly requested via environment
	requestedMode := os.Getenv(format.EnvExecMode)
	if requestedMode == "" {
		requestedMode = os.Getenv(format.EnvDispatchMode)
	}

	if strings.EqualFold(requestedMode, format.ExecModeCache) {
		return executeViaCache(selfFile, entry, args, baseEnv, hostInfo, policyRes, nil, startTime)
	}

	// 1. Try In-Memory memfd_create
	err := executeViaMemfd(selfFile, entry, args, baseEnv, hostInfo, policyRes, startTime)
	if err == nil {
		return nil
	}

	// 2. Fallback to cached file execution
	return executeViaCache(selfFile, entry, args, baseEnv, hostInfo, policyRes, err, startTime)
}

func buildAutoTunedEnviron(
	baseEnv []string,
	entry *format.VariantEntry,
	execMode string,
	hostInfo microarch.Info,
	policyRes microarch.PolicyResult,
) ([]string, *cgroup.Limits) {
	env := make([]string, len(baseEnv), len(baseEnv)+extraEnvCapacity)
	copy(env, baseEnv)

	env = append(env,
		fmt.Sprintf("%s=%s", format.EnvSelectedVariant, entry.Level),
		fmt.Sprintf("%s=%s", format.EnvHostArch, hostInfo.Arch),
		fmt.Sprintf("%s=%s", format.EnvHostLevel, hostInfo.Level),
		fmt.Sprintf("%s=%s", format.EnvExecMode, execMode),
		fmt.Sprintf("%s=%s", format.EnvDispatchMode, execMode),
		fmt.Sprintf("%s=%s", format.EnvSelectedSHA256, entry.SHA256),
		fmt.Sprintf("%s=%d", format.EnvSelectedSize, entry.UncompressedSize),
	)

	if policyRes.PolicyApplied != "" {
		env = append(env,
			fmt.Sprintf("%s=%s", format.EnvPolicyApplied, policyRes.PolicyApplied),
			fmt.Sprintf("%s=%s", format.EnvOverrideReason, policyRes.OverrideReason),
		)
	}

	limits, err := readCgroupLimitsFunc()
	if err != nil || limits.CgroupVersion == cgroup.VersionUnknown {
		return env, nil
	}

	env = append(env,
		fmt.Sprintf("%s=%d", format.EnvCgroupVersion, limits.CgroupVersion),
		fmt.Sprintf("%s=%d", format.EnvCgroupLimitBytes, limits.MemoryLimitBytes),
		fmt.Sprintf("%s=%.2f", format.EnvCgroupCPUs, limits.CPUQuota),
	)

	var calculatedMemLimit string
	if limits.MemoryLimitBytes > 0 {
		ratio := cgroup.DefaultMemoryRatio
		if ratioStr := os.Getenv(format.EnvMemRatio); ratioStr != "" {
			if parsedRatio, rErr := strconv.ParseFloat(ratioStr, 64); rErr == nil && parsedRatio > 0 && parsedRatio <= 1.0 {
				ratio = parsedRatio
			}
		}
		if memLimit, ok := cgroup.CalculateGOMEMLIMIT(limits.MemoryLimitBytes, ratio, cgroup.DefaultMinHeadroomBytes); ok {
			calculatedMemLimit = fmt.Sprintf("%dB", memLimit)
			env = append(env, fmt.Sprintf("%s=%s", format.EnvCgroupGOMEMLIMIT, calculatedMemLimit))
		}
	}

	var calculatedMaxProcs string
	if limits.CPUs > 0 {
		calculatedMaxProcs = strconv.Itoa(limits.CPUs)
		env = append(env, fmt.Sprintf("%s=%s", format.EnvCgroupGOMAXPROCS, calculatedMaxProcs))
	}

	// Check if user opted out of auto-tuning
	autoTuneOpt := os.Getenv(format.EnvAutotune)
	if autoTuneOpt == "0" || strings.EqualFold(autoTuneOpt, "false") {
		return env, &limits
	}

	hasMemLimit := false
	hasMaxProcs := false
	for _, e := range baseEnv {
		if strings.HasPrefix(e, "GOMEMLIMIT=") {
			hasMemLimit = true
		}
		if strings.HasPrefix(e, "GOMAXPROCS=") {
			hasMaxProcs = true
		}
	}

	if !hasMemLimit && calculatedMemLimit != "" {
		env = append(env, fmt.Sprintf("GOMEMLIMIT=%s", calculatedMemLimit))
	}

	if !hasMaxProcs && calculatedMaxProcs != "" {
		env = append(env, fmt.Sprintf("GOMAXPROCS=%s", calculatedMaxProcs))
	}

	return env, &limits
}

func logDiagnostics(
	entry *format.VariantEntry,
	execMode string,
	hostInfo microarch.Info,
	policyRes microarch.PolicyResult,
	env []string,
	limits *cgroup.Limits,
	decompDuration time.Duration,
	totalDuration time.Duration,
) {
	debugOpt := os.Getenv(format.EnvDebug)
	logOpt := os.Getenv(format.EnvLog)
	if debugOpt == "" && logOpt == "" {
		return
	}
	if debugOpt == "0" || strings.EqualFold(debugOpt, "false") {
		if logOpt == "" {
			return
		}
	}

	var memLimit, maxProcs string
	for _, e := range env {
		if strings.HasPrefix(e, "GOMEMLIMIT=") {
			memLimit = strings.TrimPrefix(e, "GOMEMLIMIT=")
		}
		if strings.HasPrefix(e, "GOMAXPROCS=") {
			maxProcs = strings.TrimPrefix(e, "GOMAXPROCS=")
		}
	}

	if strings.EqualFold(logOpt, "json") {
		d := format.DispatchTelemetry{
			Event:                   format.EventDispatch,
			TimestampUnixNano:       time.Now().UnixNano(),
			HostArch:                hostInfo.Arch,
			HostLevel:               hostInfo.Level,
			SelectedVariant:         entry.Level,
			SelectedSHA256:          entry.SHA256,
			SelectedSizeBytes:       entry.UncompressedSize,
			ExecMode:                execMode,
			PolicyApplied:           policyRes.PolicyApplied,
			PolicyReason:            policyRes.OverrideReason,
			GOMEMLIMIT:              memLimit,
			GOMAXPROCS:              maxProcs,
			DecompressionDurationUs: decompDuration.Microseconds(),
			TotalLauncherUs:         totalDuration.Microseconds(),
		}
		if limits != nil {
			d.CgroupVersion = limits.CgroupVersion
			d.CgroupMemLimitBytes = limits.MemoryLimitBytes
			d.CgroupCPUQuota = limits.CPUQuota
		}
		if b, err := json.Marshal(d); err == nil {
			fmt.Fprintf(os.Stderr, "[microfat] %s\n", string(b))
		}
		return
	}

	policyStr := ""
	if policyRes.PolicyApplied != "" {
		policyStr = fmt.Sprintf(" policy=%s policy_reason=%q", policyRes.PolicyApplied, policyRes.OverrideReason)
	}

	fmt.Fprintf(
		os.Stderr,
		"[microfat:debug] host_arch=%s host_level=%s selected_variant=%s exec_mode=%s gomemlimit=%s gomaxprocs=%s%s "+
			"decompress_us=%d total_us=%d\n",
		hostInfo.Arch, hostInfo.Level, entry.Level, execMode, memLimit, maxProcs, policyStr,
		decompDuration.Microseconds(), totalDuration.Microseconds(),
	)
}

func logErrorDiagnostics(
	stage string,
	err error,
	hostInfo microarch.Info,
	entry *format.VariantEntry,
	policyRes microarch.PolicyResult,
	details string,
) {
	logOpt := os.Getenv(format.EnvLog)
	if !strings.EqualFold(logOpt, "json") {
		return
	}
	e := format.ErrorTelemetry{
		Event:             format.EventError,
		TimestampUnixNano: time.Now().UnixNano(),
		HostArch:          hostInfo.Arch,
		HostLevel:         hostInfo.Level,
		PolicyApplied:     policyRes.PolicyApplied,
		PolicyReason:      policyRes.OverrideReason,
		Stage:             stage,
		Error:             err.Error(),
		Details:           details,
	}
	if entry != nil {
		e.SelectedVariant = entry.Level
	}
	if b, mErr := json.Marshal(e); mErr == nil {
		fmt.Fprintf(os.Stderr, "[microfat] %s\n", string(b))
	}
}

func executeViaMemfd(
	selfFile *os.File,
	entry *format.VariantEntry,
	args []string,
	baseEnv []string,
	hostInfo microarch.Info,
	policyRes microarch.PolicyResult,
	startTime time.Time,
) error {
	env, limits := buildAutoTunedEnviron(baseEnv, entry, format.ExecModeMemfd, hostInfo, policyRes)

	fd, err := memfdCreateFunc("microfat_payload", unix.MFD_CLOEXEC)
	if err != nil {
		logErrorDiagnostics("memfd_create", err, hostInfo, entry, policyRes, "falling back to disk cache")
		return fmt.Errorf("memfd_create failed: %w", err)
	}

	memFile := os.NewFile(uintptr(fd), "microfat_payload")
	defer func() { _ = memFile.Close() }()

	decompStart := time.Now()
	if err := extractVariantToWriter(selfFile, entry, memFile); err != nil {
		logErrorDiagnostics("extract_memfd", err, hostInfo, entry, policyRes, "decompressing payload failed")
		return fmt.Errorf("decompressing into memfd: %w", err)
	}
	decompDuration := time.Since(decompStart)

	logDiagnostics(entry, format.ExecModeMemfd, hostInfo, policyRes, env, limits, decompDuration, time.Since(startTime))

	procPath := "/proc/self/fd/" + strconv.Itoa(fd)
	// #nosec G204, G702 -- launcher stub explicitly forwards process execution to the payload
	execErr := execveFunc(procPath, args, env)
	if execErr == nil {
		return nil
	}
	logErrorDiagnostics("execve_memfd", execErr, hostInfo, entry, policyRes, "execve failed on "+procPath)
	return fmt.Errorf("execve on %s failed: %w", procPath, execErr)
}

func executeViaCache(
	selfFile *os.File,
	entry *format.VariantEntry,
	args []string,
	baseEnv []string,
	hostInfo microarch.Info,
	policyRes microarch.PolicyResult,
	primaryErr error,
	startTime time.Time,
) error {
	env, limits := buildAutoTunedEnviron(baseEnv, entry, format.ExecModeCache, hostInfo, policyRes)

	cacheDir, err := resolveCacheDirFunc("")
	if err != nil {
		errOut := fmt.Errorf("launcher execution failed: unable to initialize cache: %w (primary memfd error: %v)", err, primaryErr)
		logErrorDiagnostics("cache_dir_init", errOut, hostInfo, entry, policyRes, "cache directory creation failed")
		return errOut
	}

	cachedBinary := filepath.Join(cacheDir, filepath.Clean(entry.SHA256))
	var decompDuration time.Duration
	stat, statErr := os.Stat(cachedBinary)
	// #nosec G703 -- cache entry existence and size verification
	if statErr != nil || stat.Size() != entry.UncompressedSize {
		if statErr == nil && stat.Size() != entry.UncompressedSize {
			if os.Getenv(format.EnvDebug) == "1" {
				fmt.Fprintf(
					os.Stderr,
					"[microfat:debug] truncated cache file detected (%s, expected %d B, got %d B), re-extracting\n",
					cachedBinary, entry.UncompressedSize, stat.Size(),
				)
			}
		}
		tmpFile, err := os.CreateTemp(cacheDir, ".exec-*.tmp")
		if err != nil {
			errOut := fmt.Errorf("launcher execution failed: cannot create temp file in %s: %w (primary memfd error: %v)",
				cacheDir, err, primaryErr)
			logErrorDiagnostics("cache_create_temp", errOut, hostInfo, entry, policyRes, "temp file creation failed in "+cacheDir)
			return errOut
		}
		tmpPath := tmpFile.Name()
		defer func() {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}()

		decompStart := time.Now()
		if err := extractVariantToWriter(selfFile, entry, tmpFile); err != nil {
			logErrorDiagnostics("cache_extract", err, hostInfo, entry, policyRes, "decompressing payload to cache failed")
			return fmt.Errorf("extracting to cache fallback: %w", err)
		}
		decompDuration = time.Since(decompStart)

		_ = tmpFile.Chmod(format.PrivateExecMode)
		_ = tmpFile.Close()
		// #nosec G703 -- atomic move to cache location
		_ = os.Rename(tmpPath, cachedBinary)
	}

	logDiagnostics(entry, format.ExecModeCache, hostInfo, policyRes, env, limits, decompDuration, time.Since(startTime))

	// #nosec G204, G702 -- launcher fallback execution
	execErr := execveFunc(cachedBinary, args, env)
	if execErr == nil {
		return nil
	}
	logErrorDiagnostics("execve_cache", execErr, hostInfo, entry, policyRes, "execve failed on cached binary "+cachedBinary)
	if primaryErr != nil {
		return fmt.Errorf("cache fallback execve failed (%s): %w (primary memfd error: %v)", cachedBinary, execErr, primaryErr)
	}
	return fmt.Errorf("cache execve failed (%s): %w", cachedBinary, execErr)
}
