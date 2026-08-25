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

	"github.com/ghostnetorg/microfat/internal/format"
	"github.com/ghostnetorg/pkg/cgroup"
	"github.com/ghostnetorg/pkg/microarch"
	"golang.org/x/sys/unix"
)

const (
	privateCacheDirMode = 0o700
	privateExecMode     = 0o700
)

// executeVariant runs the selected variant payload in-memory using Linux memfd_create,
// falling back to user cache execution if memfd is restricted.
func executeVariant(selfFile *os.File, entry *format.VariantEntry, args []string, baseEnv []string, hostInfo microarch.Info) error {
	// 1. Try In-Memory memfd_create
	err := executeViaMemfd(selfFile, entry, args, baseEnv, hostInfo)
	if err == nil {
		return nil
	}

	// 2. Fallback to cached file execution
	return executeViaCache(selfFile, entry, args, baseEnv, hostInfo, err)
}

func buildAutoTunedEnviron(baseEnv []string, selectedLevel string, execMode string) []string {
	const extraEnvCapacity = 4
	env := make([]string, len(baseEnv), len(baseEnv)+extraEnvCapacity)
	copy(env, baseEnv)

	env = append(env,
		fmt.Sprintf("%s=%s", format.EnvSelectedVariant, selectedLevel),
		fmt.Sprintf("%s=%s", format.EnvExecMode, execMode),
	)

	// Check if user opted out of auto-tuning
	autoTuneOpt := os.Getenv(format.EnvAutotune)
	if autoTuneOpt == "0" || strings.EqualFold(autoTuneOpt, "false") {
		return env
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

	// If both are already explicitly configured, preserve existing env
	if hasMemLimit && hasMaxProcs {
		return env
	}

	limits, err := cgroup.ReadLimits()
	if err != nil || limits.CgroupVersion == cgroup.VersionUnknown {
		return env
	}

	if !hasMemLimit && limits.MemoryLimitBytes > 0 {
		ratio := cgroup.DefaultMemoryRatio
		if ratioStr := os.Getenv(format.EnvMemRatio); ratioStr != "" {
			if parsedRatio, rErr := strconv.ParseFloat(ratioStr, 64); rErr == nil && parsedRatio > 0 && parsedRatio <= 1.0 {
				ratio = parsedRatio
			}
		}
		if memLimit, ok := cgroup.CalculateGOMEMLIMIT(limits.MemoryLimitBytes, ratio, cgroup.DefaultMinHeadroomBytes); ok {
			env = append(env, fmt.Sprintf("GOMEMLIMIT=%dB", memLimit))
		}
	}

	if !hasMaxProcs && limits.CPUs > 0 {
		env = append(env, fmt.Sprintf("GOMAXPROCS=%d", limits.CPUs))
	}

	return env
}

func logDiagnostics(entry *format.VariantEntry, execMode string, hostInfo microarch.Info, env []string) {
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
		type diagLog struct {
			HostArch        string `json:"host_arch"`
			HostLevel       string `json:"host_level"`
			SelectedVariant string `json:"selected_variant"`
			ExecMode        string `json:"exec_mode"`
			GOMEMLIMIT      string `json:"gomemlimit,omitempty"`
			GOMAXPROCS      string `json:"gomaxprocs,omitempty"`
		}
		d := diagLog{
			HostArch:        hostInfo.Arch,
			HostLevel:       hostInfo.Level,
			SelectedVariant: entry.Level,
			ExecMode:        execMode,
			GOMEMLIMIT:      memLimit,
			GOMAXPROCS:      maxProcs,
		}
		if b, err := json.Marshal(d); err == nil {
			fmt.Fprintf(os.Stderr, "[microfat] %s\n", string(b))
		}
		return
	}

	fmt.Fprintf(os.Stderr, "[microfat:debug] host_arch=%s host_level=%s selected_variant=%s exec_mode=%s gomemlimit=%s gomaxprocs=%s\n",
		hostInfo.Arch, hostInfo.Level, entry.Level, execMode, memLimit, maxProcs)
}

func executeViaMemfd(selfFile *os.File, entry *format.VariantEntry, args []string, baseEnv []string, hostInfo microarch.Info) error {
	env := buildAutoTunedEnviron(baseEnv, entry.Level, format.ExecModeMemfd)
	logDiagnostics(entry, format.ExecModeMemfd, hostInfo, env)

	fd, err := unix.MemfdCreate("microfat_payload", unix.MFD_CLOEXEC)
	if err != nil {
		return fmt.Errorf("memfd_create failed: %w", err)
	}

	memFile := os.NewFile(uintptr(fd), "microfat_payload")
	defer func() { _ = memFile.Close() }()

	if err := extractVariantToWriter(selfFile, entry, memFile); err != nil {
		return fmt.Errorf("decompressing into memfd: %w", err)
	}

	procPath := "/proc/self/fd/" + strconv.Itoa(fd)
	// #nosec G204, G702 -- launcher stub explicitly forwards process execution to the payload
	execErr := syscall.Exec(procPath, args, env)
	return fmt.Errorf("execve on %s failed: %w", procPath, execErr)
}

func executeViaCache(
	selfFile *os.File,
	entry *format.VariantEntry,
	args []string,
	baseEnv []string,
	hostInfo microarch.Info,
	primaryErr error,
) error {
	env := buildAutoTunedEnviron(baseEnv, entry.Level, format.ExecModeCache)
	logDiagnostics(entry, format.ExecModeCache, hostInfo, env)

	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			cacheDir = filepath.Join(homeDir, ".cache")
		} else {
			cacheDir = os.TempDir()
		}
	}
	cacheDir = filepath.Join(cacheDir, "microfat")

	var triedDirs []string
	triedDirs = append(triedDirs, cacheDir)

	// #nosec G703 -- cache fallback directory creation with strict private permissions
	if err := os.MkdirAll(cacheDir, privateCacheDirMode); err != nil {
		cacheDir = filepath.Join(os.TempDir(), fmt.Sprintf(".microfat-%d", os.Getuid()))
		triedDirs = append(triedDirs, cacheDir)
		if err2 := os.MkdirAll(cacheDir, privateCacheDirMode); err2 != nil {
			return fmt.Errorf("launcher execution failed: memfd_create unavailable (%v) and unable to initialize cache "+
				"directories (%s): %w. Remediation: ensure /proc and memfd_create are enabled or mount a writable "+
				"tmpfs at /tmp or $XDG_CACHE_HOME",
				primaryErr, strings.Join(triedDirs, ", "), err2)
		}
	}

	cachedBinary := filepath.Join(cacheDir, filepath.Clean(entry.SHA256))
	// #nosec G703 -- cache entry existence check
	if _, err := os.Stat(cachedBinary); err != nil {
		tmpFile, err := os.CreateTemp(cacheDir, ".exec-*.tmp")
		if err != nil {
			return fmt.Errorf("launcher execution failed: memfd_create unavailable (%v) and cannot create temp file "+
				"in %s: %w. Remediation: verify write permissions in %s or enable memfd_create",
				primaryErr, cacheDir, err, cacheDir)
		}
		tmpPath := tmpFile.Name()
		defer func() {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}()

		if err := extractVariantToWriter(selfFile, entry, tmpFile); err != nil {
			return fmt.Errorf("extracting to cache fallback: %w", err)
		}
		_ = tmpFile.Chmod(privateExecMode)
		_ = tmpFile.Close()
		// #nosec G703 -- atomic move to cache location
		_ = os.Rename(tmpPath, cachedBinary)
	}

	// #nosec G204, G702 -- launcher fallback execution
	execErr := syscall.Exec(cachedBinary, args, env)
	return fmt.Errorf("cache fallback execve failed (%s): %w (primary memfd error: %v)", cachedBinary, execErr, primaryErr)
}
