//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/ghostnetorg/microfat/internal/format"
	"github.com/ghostnetorg/pkg/cgroup"
	"golang.org/x/sys/unix"
)

// executeVariant runs the selected variant payload in-memory using Linux memfd_create,
// falling back to user cache execution if memfd is restricted.
func executeVariant(selfFile *os.File, entry *format.VariantEntry, args []string, baseEnv []string) error {
	env := buildAutoTunedEnviron(baseEnv)

	// 1. Try In-Memory memfd_create
	err := executeViaMemfd(selfFile, entry, args, env)
	if err == nil {
		return nil
	}

	// 2. Fallback to cached file execution
	return executeViaCache(selfFile, entry, args, env, err)
}

func buildAutoTunedEnviron(baseEnv []string) []string {
	// 1. Check if user opted out of auto-tuning
	autoTuneOpt := os.Getenv("MICROFAT_AUTOTUNE")
	if autoTuneOpt == "0" || strings.EqualFold(autoTuneOpt, "false") {
		return baseEnv
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
		return baseEnv
	}

	limits, err := cgroup.ReadLimits()
	if err != nil || limits.CgroupVersion == cgroup.VersionUnknown {
		return baseEnv
	}

	const extraEnvCapacity = 2
	env := make([]string, len(baseEnv), len(baseEnv)+extraEnvCapacity)
	copy(env, baseEnv)

	if !hasMemLimit && limits.MemoryLimitBytes > 0 {
		ratio := cgroup.DefaultMemoryRatio
		if ratioStr := os.Getenv("MICROFAT_MEM_RATIO"); ratioStr != "" {
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

func executeViaMemfd(selfFile *os.File, entry *format.VariantEntry, args []string, env []string) error {
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

func executeViaCache(selfFile *os.File, entry *format.VariantEntry, args []string, env []string, primaryErr error) error {
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

	// #nosec G703 -- cache fallback directory creation
	if err := os.MkdirAll(cacheDir, defaultExecMode); err != nil {
		cacheDir = filepath.Join(os.TempDir(), fmt.Sprintf(".microfat-%d", os.Getuid()))
		_ = os.MkdirAll(cacheDir, defaultExecMode)
	}

	cachedBinary := filepath.Join(cacheDir, filepath.Clean(entry.SHA256))
	// #nosec G703 -- cache entry existence check
	if _, err := os.Stat(cachedBinary); err != nil {
		tmpFile, err := os.CreateTemp(cacheDir, ".exec-*.tmp")
		if err != nil {
			return fmt.Errorf("cache fallback failed (%v): cannot create temp file in %s: %w", primaryErr, cacheDir, err)
		}
		tmpPath := tmpFile.Name()
		defer func() {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}()

		if err := extractVariantToWriter(selfFile, entry, tmpFile); err != nil {
			return fmt.Errorf("extracting to cache fallback: %w", err)
		}
		_ = tmpFile.Chmod(defaultExecMode)
		_ = tmpFile.Close()
		// #nosec G703 -- atomic move to cache location
		_ = os.Rename(tmpPath, cachedBinary)
	}

	// #nosec G204, G702 -- launcher fallback execution
	execErr := syscall.Exec(cachedBinary, args, env)
	return fmt.Errorf("cache fallback execve failed (%s): %w (primary memfd error: %v)", cachedBinary, execErr, primaryErr)
}
