// Package cgroup provides utilities for inspecting Linux cgroup v1 and cgroup v2 resource limits
// (memory ceilings and CPU CFS quotas) and calculating optimal Go runtime parameters (GOMEMLIMIT, GOMAXPROCS).
package cgroup

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Default tuning constants.
const (
	// DefaultMemoryRatio is the default fraction of container memory allocated to GOMEMLIMIT (90%).
	DefaultMemoryRatio = 0.90

	// DefaultMinHeadroomBytes is the minimum memory headroom reserved for non-heap allocations (64MB).
	DefaultMinHeadroomBytes int64 = 64 * 1024 * 1024

	// MinimumCPUs is the absolute minimum GOMAXPROCS value.
	MinimumCPUs = 1

	// UnlimitedCgroupV1MemoryThreshold represents a memory limit >= 1 Petabyte (considered unlimited in cgroup v1).
	UnlimitedCgroupV1MemoryThreshold int64 = 1024 * 1024 * 1024 * 1024 * 1024

	smallContainerFallbackRatio = 0.50

	defaultCgroupMount    = "/sys/fs/cgroup"
	defaultProcSelfCgroup = "/proc/self/cgroup"
	expectedCgroupParts   = 3
	expectedCPUFields     = 2
	cgroupV2HierarchyID   = "0"
	cgroupV2MemoryMax     = "memory.max"
	cgroupV2MemoryHigh    = "memory.high"
)

// Memory limit constraining factors.
const (
	// LimitConstraintNone indicates no memory limit constraint is active.
	LimitConstraintNone = ""

	// LimitConstraintMax indicates memory.max (or cgroup v1 hard limit) is the active constraint.
	LimitConstraintMax = "max"

	// LimitConstraintHigh indicates memory.high (cgroup v2 throttle watermark) is the active constraint.
	LimitConstraintHigh = "high"
)

// GCProfile represents a workload-aware Go runtime garbage collection tuning profile.
type GCProfile string

const (
	// GCProfileDefault represents the standard Go runtime default behavior (GOGC=100 or user-provided).
	GCProfileDefault GCProfile = "default"

	// GCProfileLatencyCritical tunes GOGC=75 to minimize GC latency spikes and tail SLAs.
	GCProfileLatencyCritical GCProfile = "latency_critical"

	// GCProfileMemoryConstrained tunes GOGC=40 and default MemoryRatio=0.80 for tight memory containers.
	GCProfileMemoryConstrained GCProfile = "memory_constrained"

	// GCProfileBatchETL tunes GOGC=-1 (off) to rely on GOMEMLIMIT soft ceiling and maximize CPU throughput.
	GCProfileBatchETL GCProfile = "batch_etl"

	// GCProfileAdaptive dynamically calculates GOGC based on estimated steady-state live heap and headroom.
	GCProfileAdaptive GCProfile = "adaptive"
)

// Profile target constants.
const (
	// DefaultLatencyCriticalGOGC is the target GOGC for latency-critical microservices (75).
	DefaultLatencyCriticalGOGC = 75

	// DefaultMemoryConstrainedGOGC is the target GOGC for memory-constrained micro-containers (40).
	DefaultMemoryConstrainedGOGC = 40

	// DefaultMemoryConstrainedRatio is the default container memory ratio for memory-constrained profiles (80%).
	DefaultMemoryConstrainedRatio = 0.80

	// DefaultBatchETLGOGC turns off periodic GC in favor of GOMEMLIMIT pacing (-1).
	DefaultBatchETLGOGC = -1

	// AdaptiveMinGOGC is the lower clamping bound for adaptive GOGC calculation (10).
	AdaptiveMinGOGC = 10

	// AdaptiveMaxGOGC is the upper clamping bound for adaptive GOGC calculation (100).
	AdaptiveMaxGOGC = 100

	adaptiveScaleMultiplier = 100.0
)

const (
	byteUnitKibi = 1024
	byteUnitMebi = 1024 * byteUnitKibi
	byteUnitGibi = 1024 * byteUnitMebi
	byteUnitTebi = 1024 * byteUnitGibi
)

// Cgroup versions.
const (
	VersionUnknown = 0
	VersionV1      = 1
	VersionV2      = 2
)

// Limits contains resolved container memory and CPU limits.
type Limits struct {
	// RetainedExecutableBytes is trusted kernel-backed storage, not inherited environment metadata.
	RetainedExecutableBytes   int64   `json:"retained_executable_bytes,omitempty"`
	CgroupVersion             int     `json:"cgroup_version"`
	MemoryLimitBytes          int64   `json:"memory_limit_bytes"`           // Hard OOM ceiling (memory.max in v2, memory.limit_in_bytes in v1)
	MemoryHighBytes           int64   `json:"memory_high_bytes,omitempty"`  // Throttle watermark (memory.high in v2, 0 if unset/v1)
	EffectiveMemoryLimitBytes int64   `json:"effective_memory_limit_bytes"` // Effective ceiling used for GOMEMLIMIT calculation
	CPUQuota                  float64 `json:"cpu_quota"`                    // 0 if unlimited
	CPUs                      int     `json:"cpus"`                         // Computed GOMAXPROCS (0 if unlimited)
}

// TuningPlan contains computed Go runtime tuning parameters derived from container resource limits.
type TuningPlan struct {
	GOMEMLIMITBytes int64  `json:"gomemlimit_bytes"` // Calculated memory limit in bytes (0 if unset/unlimited)
	GOMEMLIMITStr   string `json:"gomemlimit_str"`   // Formatted memory limit string (e.g. "966367641B", empty if unset)
	// ConstrainingLimit is the active memory constraint: "max" or "high" (empty if unlimited; ties resolve to "max").
	ConstrainingLimit string    `json:"constraining_limit,omitempty"`
	GOMAXPROCS        int       `json:"gomaxprocs"`         // Calculated CPU quota core count (0 if unset/unlimited)
	GOMAXPROCSStr     string    `json:"gomaxprocs_str"`     // Formatted GOMAXPROCS string (e.g. "4", empty if unset)
	AppliedRatio      float64   `json:"applied_ratio"`      // Actual memory ratio applied (e.g. 0.90 or custom)
	GOGC              int       `json:"gogc,omitempty"`     // Calculated GOGC target (-1 if off, 0 if unset/default)
	GOGCStr           string    `json:"gogc_str,omitempty"` // Formatted GOGC string (e.g. "75", "40", "off", empty if unset)
	GCProfile         GCProfile `json:"gc_profile,omitempty"`
	GOGCApplied       bool      `json:"gogc_applied"`
}

// Sentinel errors for cgroup inspection and hierarchy resolution.
var (
	// ErrCgroupMountInaccessible indicates that the specified cgroup mount directory cannot be accessed.
	ErrCgroupMountInaccessible = errors.New("cgroup mount inaccessible")

	// ErrCgroupProcUnreadable indicates that /proc/self/cgroup or the specified procfs file could not be read or parsed.
	ErrCgroupProcUnreadable = errors.New("cgroup proc file unreadable")

	// ErrCgroupHierarchyNotFound indicates that the relative cgroup path from procfs does not exist under the cgroup mount.
	ErrCgroupHierarchyNotFound = errors.New("cgroup hierarchy path not found")

	// ErrCgroupLimitCorrupted indicates that a cgroup limit file contains invalid or corrupted data.
	ErrCgroupLimitCorrupted = errors.New("cgroup limit file corrupted")
)

// ReadLimits inspects /sys/fs/cgroup and returns the active container limits.
func ReadLimits() (Limits, error) {
	return ReadLimitsCustom(defaultCgroupMount, defaultProcSelfCgroup)
}

// ReadLimitsFrom inspects the specified cgroup root directory using default /proc/self/cgroup resolution.
func ReadLimitsFrom(root string) (Limits, error) {
	return ReadLimitsCustom(root, defaultProcSelfCgroup)
}

// ReadLimitsCustom inspects the specified cgroup root directory and procfs cgroup file.
func ReadLimitsCustom(root string, procCgroupPath string) (Limits, error) {
	cleanRoot := filepath.Clean(root)
	if _, err := os.Stat(cleanRoot); err != nil {
		return Limits{CgroupVersion: VersionUnknown}, fmt.Errorf("%w: %s: %w", ErrCgroupMountInaccessible, cleanRoot, err)
	}

	v2RelPath, hasV2, v1RelPaths, procErr := parseProcCgroup(procCgroupPath)
	if procErr != nil {
		return Limits{CgroupVersion: VersionUnknown}, fmt.Errorf("%w: %w", ErrCgroupProcUnreadable, procErr)
	}

	// 1. Detect cgroup v2 (unified hierarchy: memory.max, memory.high, or cgroup.controllers exists at root)
	v2MemMax := filepath.Join(cleanRoot, cgroupV2MemoryMax)
	v2MemHigh := filepath.Join(cleanRoot, cgroupV2MemoryHigh)
	v2Controllers := filepath.Join(cleanRoot, "cgroup.controllers")
	if _, err := os.Stat(v2MemMax); err == nil {
		if !hasV2 {
			return Limits{CgroupVersion: VersionUnknown}, nil
		}
		return readCgroupV2(cleanRoot, v2RelPath)
	}
	if _, err := os.Stat(v2MemHigh); err == nil {
		if !hasV2 {
			return Limits{CgroupVersion: VersionUnknown}, nil
		}
		return readCgroupV2(cleanRoot, v2RelPath)
	}
	if _, err := os.Stat(v2Controllers); err == nil {
		if !hasV2 {
			return Limits{CgroupVersion: VersionUnknown}, nil
		}
		return readCgroupV2(cleanRoot, v2RelPath)
	}

	// 2. Detect cgroup v1 (legacy hierarchy: memory/ and cpu/ subdirectories)
	v1MemLimit := filepath.Join(cleanRoot, "memory", "memory.limit_in_bytes")
	if _, err := os.Stat(v1MemLimit); err == nil {
		return readCgroupV1(cleanRoot, v1RelPaths)
	}
	if _, err := os.Stat(filepath.Join(cleanRoot, "memory.limit_in_bytes")); err == nil {
		return readCgroupV1(cleanRoot, v1RelPaths)
	}

	return Limits{CgroupVersion: VersionUnknown}, nil
}

func parseProcCgroup(procPath string) (string, bool, map[string]string, error) {
	v1Paths := make(map[string]string)
	v2Path := ""
	hasV2 := false

	targetPath := procPath
	if targetPath == "" {
		targetPath = defaultProcSelfCgroup
	}

	// #nosec G304 -- reading procfs cgroup file
	f, err := os.Open(filepath.Clean(targetPath))
	if err != nil {
		return "", false, v1Paths, err
	}
	defer func() { _ = f.Close() }()

	hasEntries := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", expectedCgroupParts)
		if len(parts) != expectedCgroupParts {
			continue
		}
		hierarchyID := parts[0]
		controllers := parts[1]
		relPath := parts[2]

		hasEntries = true
		if hierarchyID == cgroupV2HierarchyID && controllers == "" {
			v2Path = relPath
			hasV2 = true
		} else {
			for _, ctrl := range strings.Split(controllers, ",") {
				trimmedCtrl := strings.TrimSpace(ctrl)
				if trimmedCtrl != "" {
					v1Paths[trimmedCtrl] = relPath
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return v2Path, hasV2, v1Paths, err
	}
	if !hasEntries {
		return "", false, v1Paths, errors.New("empty or invalid proc cgroup file")
	}
	return v2Path, hasV2, v1Paths, nil
}

func isSubpath(root, path string) bool {
	if root == path || root == string(filepath.Separator) {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

func resolveTargetDirectory(baseDir, relPath string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("%w: relative path is empty or unresolved", ErrCgroupHierarchyNotFound)
	}
	if relPath == "/" || relPath == "." {
		return baseDir, nil
	}
	candidate := filepath.Join(baseDir, filepath.Clean("/"+relPath))
	if !isSubpath(baseDir, candidate) {
		return "", fmt.Errorf("%w: relative path %q escapes base %q", ErrCgroupHierarchyNotFound, relPath, baseDir)
	}
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("%w: target directory %q not found: %w", ErrCgroupHierarchyNotFound, candidate, err)
	}
	return candidate, nil
}

func readCgroupV2MemoryValue(dir, filename string) (int64, bool, error) {
	memPath := filepath.Join(dir, filename)
	memBytes, err := readTrimmedFile(memPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("%w: reading %s: %w", ErrCgroupLimitCorrupted, memPath, err)
	}
	if memBytes == "max" {
		return 0, false, nil
	}
	if memBytes == "" {
		return 0, false, fmt.Errorf("%w: empty %s in %s", ErrCgroupLimitCorrupted, filename, dir)
	}
	val, parseErr := strconv.ParseInt(memBytes, 10, 64)
	if parseErr != nil || val <= 0 {
		return 0, false, fmt.Errorf("%w: invalid %s value %q in %s", ErrCgroupLimitCorrupted, filename, memBytes, memPath)
	}
	return val, true, nil
}

func readCgroupV2Memory(dir string) (int64, bool, error) {
	return readCgroupV2MemoryValue(dir, cgroupV2MemoryMax)
}

func readCgroupV2MemoryHigh(dir string) (int64, bool, error) {
	return readCgroupV2MemoryValue(dir, cgroupV2MemoryHigh)
}

func readCgroupV2CPU(dir string) (float64, bool, error) {
	cpuPath := filepath.Join(dir, "cpu.max")
	cpuContent, err := readTrimmedFile(cpuPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("%w: reading %s: %w", ErrCgroupLimitCorrupted, cpuPath, err)
	}
	if cpuContent == "" {
		return 0, false, fmt.Errorf("%w: empty cpu.max in %s", ErrCgroupLimitCorrupted, dir)
	}
	parts := strings.Fields(cpuContent)
	if len(parts) != expectedCPUFields {
		return 0, false, fmt.Errorf("%w: invalid cpu.max format %q in %s", ErrCgroupLimitCorrupted, cpuContent, cpuPath)
	}
	if parts[0] == "max" {
		period, pErr := strconv.ParseFloat(parts[1], 64)
		if pErr != nil || period <= 0 {
			return 0, false, fmt.Errorf("%w: invalid cpu.max period %q in %s", ErrCgroupLimitCorrupted, parts[1], cpuPath)
		}
		return 0, false, nil
	}
	quota, qErr := strconv.ParseFloat(parts[0], 64)
	period, pErr := strconv.ParseFloat(parts[1], 64)
	if qErr != nil || pErr != nil || period <= 0 || quota <= 0 {
		return 0, false, fmt.Errorf("%w: invalid cpu.max values %q in %s", ErrCgroupLimitCorrupted, cpuContent, cpuPath)
	}
	return quota / period, true, nil
}

func traverseCgroupV2Memory(targetDir, root string) (maxLimit int64, hasMax bool, highLimit int64, hasHigh bool, err error) {
	var minMax int64
	var minHigh int64
	curr := targetDir
	for {
		maxVal, okMax, maxErr := readCgroupV2Memory(curr)
		if maxErr != nil {
			return 0, false, 0, false, maxErr
		}
		if okMax {
			if !hasMax || maxVal < minMax {
				minMax = maxVal
				hasMax = true
			}
		}

		highVal, okHigh, highErr := readCgroupV2MemoryHigh(curr)
		if highErr != nil {
			return 0, false, 0, false, highErr
		}
		if okHigh {
			if !hasHigh || highVal < minHigh {
				minHigh = highVal
				hasHigh = true
			}
		}

		if curr == root {
			break
		}
		parent := filepath.Dir(curr)
		if parent == curr || !isSubpath(root, parent) {
			break
		}
		curr = parent
	}
	return minMax, hasMax, minHigh, hasHigh, nil
}

func traverseCgroupV2CPU(targetDir, root string) (float64, bool, error) {
	var minCPUQuota float64
	hasCPU := false
	curr := targetDir
	for {
		qRatio, ok, err := readCgroupV2CPU(curr)
		if err != nil {
			return 0, false, err
		}
		if ok {
			if !hasCPU || qRatio < minCPUQuota {
				minCPUQuota = qRatio
				hasCPU = true
			}
		}
		if curr == root {
			break
		}
		parent := filepath.Dir(curr)
		if parent == curr || !isSubpath(root, parent) {
			break
		}
		curr = parent
	}
	return minCPUQuota, hasCPU, nil
}

func readCgroupV2(root, relPath string) (Limits, error) {
	targetDir, err := resolveTargetDirectory(root, relPath)
	if err != nil {
		return Limits{CgroupVersion: VersionUnknown}, err
	}

	limits := Limits{CgroupVersion: VersionV2}

	minMem, hasMax, minHigh, hasHigh, memErr := traverseCgroupV2Memory(targetDir, root)
	if memErr != nil {
		return Limits{CgroupVersion: VersionUnknown}, memErr
	}
	if hasMax {
		limits.MemoryLimitBytes = minMem
	}
	if hasHigh {
		limits.MemoryHighBytes = minHigh
	}
	limits.EffectiveMemoryLimitBytes = CalculateEffectiveMemoryLimit(limits.MemoryLimitBytes, limits.MemoryHighBytes)

	minQuota, hasCPU, cpuErr := traverseCgroupV2CPU(targetDir, root)
	if cpuErr != nil {
		return Limits{CgroupVersion: VersionUnknown}, cpuErr
	}
	if hasCPU && minQuota > 0 {
		limits.CPUQuota = minQuota
		if cpus, ok := CalculateGOMAXPROCS(limits.CPUQuota); ok {
			limits.CPUs = cpus
		}
	}

	return limits, nil
}

func resolveCgroupV1MemBase(root string) string {
	memBaseDir := filepath.Join(root, "memory")
	if _, err := os.Stat(memBaseDir); err == nil {
		return memBaseDir
	}
	if _, statErr := os.Stat(filepath.Join(root, "memory.limit_in_bytes")); statErr == nil {
		return root
	}
	return memBaseDir
}

func resolveCgroupV1CPUBase(root string) string {
	cpuBaseDir := filepath.Join(root, "cpu")
	if _, err := os.Stat(cpuBaseDir); err == nil {
		return cpuBaseDir
	}
	cpuAcctDir := filepath.Join(root, "cpu,cpuacct")
	if _, acctErr := os.Stat(cpuAcctDir); acctErr == nil {
		return cpuAcctDir
	}
	if _, rootErr := os.Stat(filepath.Join(root, "cpu.cfs_quota_us")); rootErr == nil {
		return root
	}
	return cpuBaseDir
}

func readCgroupV1Memory(dir string) (int64, bool, error) {
	memPath := filepath.Join(dir, "memory.limit_in_bytes")
	memBytes, err := readTrimmedFile(memPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("%w: reading %s: %w", ErrCgroupLimitCorrupted, memPath, err)
	}
	if memBytes == "" {
		return 0, false, fmt.Errorf("%w: empty memory.limit_in_bytes in %s", ErrCgroupLimitCorrupted, dir)
	}
	if memBytes == "-1" {
		return 0, false, nil
	}
	val, parseErr := strconv.ParseInt(memBytes, 10, 64)
	if parseErr != nil || val <= 0 {
		return 0, false, fmt.Errorf("%w: invalid memory.limit_in_bytes value %q in %s", ErrCgroupLimitCorrupted, memBytes, memPath)
	}
	if val >= UnlimitedCgroupV1MemoryThreshold {
		return 0, false, nil
	}
	return val, true, nil
}

func readCgroupV1CPU(dir string) (float64, bool, error) {
	quotaPath := filepath.Join(dir, "cpu.cfs_quota_us")
	periodPath := filepath.Join(dir, "cpu.cfs_period_us")
	quotaStr, qErr := readTrimmedFile(quotaPath)
	if qErr != nil {
		if errors.Is(qErr, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("%w: reading %s: %w", ErrCgroupLimitCorrupted, quotaPath, qErr)
	}
	periodStr, pErr := readTrimmedFile(periodPath)
	if pErr != nil {
		if errors.Is(pErr, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("%w: reading %s: %w", ErrCgroupLimitCorrupted, periodPath, pErr)
	}
	if quotaStr == "-1" {
		period, pParseErr := strconv.ParseFloat(periodStr, 64)
		if pParseErr != nil || period <= 0 {
			return 0, false, fmt.Errorf("%w: invalid cpu.cfs_period_us value %q in %s", ErrCgroupLimitCorrupted, periodStr, dir)
		}
		return 0, false, nil
	}
	quota, qParseErr := strconv.ParseFloat(quotaStr, 64)
	period, pParseErr := strconv.ParseFloat(periodStr, 64)
	if qParseErr != nil || pParseErr != nil || quota <= 0 || period <= 0 {
		return 0, false, fmt.Errorf("%w: invalid cpu quota/period %q/%q in %s", ErrCgroupLimitCorrupted, quotaStr, periodStr, dir)
	}
	return quota / period, true, nil
}

func traverseCgroupV1Memory(memBaseDir, targetMemDir string) (int64, bool, error) {
	var minMem int64
	hasMem := false
	currMem := targetMemDir

	for {
		val, ok, err := readCgroupV1Memory(currMem)
		if err != nil {
			return 0, false, err
		}
		if ok {
			if !hasMem || val < minMem {
				minMem = val
				hasMem = true
			}
		}
		if currMem == memBaseDir {
			break
		}
		parent := filepath.Dir(currMem)
		if parent == currMem || !isSubpath(memBaseDir, parent) {
			break
		}
		currMem = parent
	}
	return minMem, hasMem, nil
}

func traverseCgroupV1CPU(cpuBaseDir, targetCPUDir string) (float64, bool, error) {
	var minCPUQuota float64
	hasCPU := false
	currCPU := targetCPUDir

	for {
		qRatio, ok, err := readCgroupV1CPU(currCPU)
		if err != nil {
			return 0, false, err
		}
		if ok {
			if !hasCPU || qRatio < minCPUQuota {
				minCPUQuota = qRatio
				hasCPU = true
			}
		}
		if currCPU == cpuBaseDir {
			break
		}
		parent := filepath.Dir(currCPU)
		if parent == currCPU || !isSubpath(cpuBaseDir, parent) {
			break
		}
		currCPU = parent
	}
	return minCPUQuota, hasCPU, nil
}

func readCgroupV1(root string, v1RelPaths map[string]string) (Limits, error) {
	memRelPath, hasMemPath := v1RelPaths["memory"]
	cpuRelPath := ""
	hasCPUPath := false
	if v1RelPaths != nil {
		if path, ok := v1RelPaths["cpu"]; ok {
			cpuRelPath = path
			hasCPUPath = true
		} else if path, ok := v1RelPaths["cpu,cpuacct"]; ok {
			cpuRelPath = path
			hasCPUPath = true
		}
	}

	if !hasMemPath && !hasCPUPath {
		return Limits{CgroupVersion: VersionUnknown}, nil
	}

	limits := Limits{CgroupVersion: VersionV1}

	if hasMemPath && memRelPath != "" {
		memBaseDir := resolveCgroupV1MemBase(root)
		targetMemDir, err := resolveTargetDirectory(memBaseDir, memRelPath)
		if err != nil {
			return Limits{CgroupVersion: VersionUnknown}, err
		}
		minMem, hasMem, memErr := traverseCgroupV1Memory(memBaseDir, targetMemDir)
		if memErr != nil {
			return Limits{CgroupVersion: VersionUnknown}, memErr
		}
		if hasMem {
			limits.MemoryLimitBytes = minMem
			limits.EffectiveMemoryLimitBytes = minMem
		}
	}

	if hasCPUPath && cpuRelPath != "" {
		cpuBaseDir := resolveCgroupV1CPUBase(root)
		targetCPUDir, err := resolveTargetDirectory(cpuBaseDir, cpuRelPath)
		if err != nil {
			return Limits{CgroupVersion: VersionUnknown}, err
		}
		minQuota, hasCPU, cpuErr := traverseCgroupV1CPU(cpuBaseDir, targetCPUDir)
		if cpuErr != nil {
			return Limits{CgroupVersion: VersionUnknown}, cpuErr
		}
		if hasCPU && minQuota > 0 {
			limits.CPUQuota = minQuota
			if cpus, ok := CalculateGOMAXPROCS(limits.CPUQuota); ok {
				limits.CPUs = cpus
			}
		}
	}

	return limits, nil
}

// CalculateEffectiveMemoryLimit determines the effective memory ceiling from a hard limit (max) and throttle watermark (high).
// If both are configured (> 0), it returns min(maxBytes, highBytes).
// If only one is configured, it returns that limit.
// If neither is configured, it returns 0 (unlimited).
func CalculateEffectiveMemoryLimit(maxBytes, highBytes int64) int64 {
	switch {
	case maxBytes > 0 && highBytes > 0:
		if highBytes < maxBytes {
			return highBytes
		}
		return maxBytes
	case highBytes > 0:
		return highBytes
	case maxBytes > 0:
		return maxBytes
	default:
		return 0
	}
}

// DetermineConstrainingLimit returns which limit ("max" or "high") constrains the effective memory limit.
// If both limits are configured and equal (maxBytes == highBytes), LimitConstraintMax ("max") is returned
// because memory.max represents the kernel's hard OOM boundary, taking precedence as the primary ceiling.
// Returns LimitConstraintNone ("") if neither limit is set.
func DetermineConstrainingLimit(maxBytes, highBytes int64) string {
	switch {
	case maxBytes > 0 && highBytes > 0:
		if highBytes < maxBytes {
			return LimitConstraintHigh
		}
		return LimitConstraintMax
	case highBytes > 0:
		return LimitConstraintHigh
	case maxBytes > 0:
		return LimitConstraintMax
	default:
		return LimitConstraintNone
	}
}

// CalculateGOMEMLIMIT computes the recommended GOMEMLIMIT in bytes given a raw memory limit.
// Returns (computedBytes, true) if a valid limit is determined, or (0, false) if unlimited or invalid.
func CalculateGOMEMLIMIT(limitBytes int64, ratio float64, minHeadroomBytes int64) (int64, bool) {
	if limitBytes <= 0 || limitBytes >= UnlimitedCgroupV1MemoryThreshold {
		return 0, false
	}
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio <= 0 || ratio > 1.0 {
		ratio = DefaultMemoryRatio
	}
	if minHeadroomBytes <= 0 {
		minHeadroomBytes = DefaultMinHeadroomBytes
	}

	ratioBased := float64(limitBytes) * ratio
	headroomBased := float64(limitBytes - minHeadroomBytes)

	// Take the smaller of the two to guarantee safety headroom on both small and large containers
	chosen := math.Min(ratioBased, headroomBased)
	if chosen <= 0 {
		// Extremely small container (e.g. < 64MB): allocate at least 50%
		chosen = float64(limitBytes) * smallContainerFallbackRatio
	}

	res := int64(chosen)
	if res <= 0 {
		return 0, false
	}
	return res, true
}

// CalculateGOMAXPROCS computes the recommended GOMAXPROCS value from a fractional CPU quota.
// Returns (cpus, true) if a valid quota exists, or (0, false) if unlimited.
func CalculateGOMAXPROCS(quota float64) (int, bool) {
	if quota <= 0 || math.IsNaN(quota) || math.IsInf(quota, 0) {
		return 0, false
	}
	// Floor rounding to prevent CFS scheduler period oversubscription and latency spikes
	cpus := int(math.Floor(quota))
	if cpus < MinimumCPUs {
		cpus = MinimumCPUs
	}
	return cpus, true
}

// ParseGCProfile converts a profile name string into a typed GCProfile.
func ParseGCProfile(s string) (GCProfile, error) {
	norm := strings.ToLower(strings.TrimSpace(s))
	norm = strings.ReplaceAll(norm, "-", "_")

	switch norm {
	case "", string(GCProfileDefault):
		return GCProfileDefault, nil
	case string(GCProfileLatencyCritical), "latency":
		return GCProfileLatencyCritical, nil
	case string(GCProfileMemoryConstrained), "memory":
		return GCProfileMemoryConstrained, nil
	case string(GCProfileBatchETL), "batch", "etl":
		return GCProfileBatchETL, nil
	case string(GCProfileAdaptive), "dynamic":
		return GCProfileAdaptive, nil
	default:
		return GCProfileDefault, fmt.Errorf("unknown GC profile: %q", s)
	}
}

// ParseByteSize parses human-readable byte sizes (e.g. "150MB", "150MiB", "150M", "1.5GB", "1024").
func ParseByteSize(s string) (int64, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, errors.New("empty byte size string")
	}

	i := 0
	for i < len(trimmed) && (trimmed[i] >= '0' && trimmed[i] <= '9' || trimmed[i] == '.') {
		i++
	}

	numStr := strings.TrimSpace(trimmed[:i])
	unitStr := strings.ToLower(strings.TrimSpace(trimmed[i:]))

	if numStr == "" {
		return 0, fmt.Errorf("missing numeric value in byte size string %q", s)
	}

	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil || val < 0 || math.IsNaN(val) || math.IsInf(val, 0) {
		return 0, fmt.Errorf("invalid numeric value %q in byte size: %w", numStr, err)
	}

	var multiplier float64
	switch unitStr {
	case "", "b", "byte", "bytes":
		multiplier = 1
	case "k", "kb", "kib":
		multiplier = float64(byteUnitKibi)
	case "m", "mb", "mib":
		multiplier = float64(byteUnitMebi)
	case "g", "gb", "gib":
		multiplier = float64(byteUnitGibi)
	case "t", "tb", "tib":
		multiplier = float64(byteUnitTebi)
	default:
		return 0, fmt.Errorf("unknown byte unit %q in %q", unitStr, s)
	}

	total := val * multiplier
	if total > float64(math.MaxInt64) {
		return 0, fmt.Errorf("byte size %q exceeds max int64", s)
	}

	return int64(math.Round(total)), nil
}

// CalculateAdaptiveGOGC computes the recommended GOGC percentage given available headroom and estimated live heap.
// Sizing formula: GOGC = min(100, max(10, ((Available Headroom / Live Heap) - 1) * 100))
func CalculateAdaptiveGOGC(availableHeadroomBytes int64, liveHeapBytes int64) (int, bool) {
	if availableHeadroomBytes <= 0 || liveHeapBytes <= 0 {
		return 0, false
	}

	ratio := float64(availableHeadroomBytes) / float64(liveHeapBytes)
	gogcFloat := (ratio - 1.0) * adaptiveScaleMultiplier

	if gogcFloat < float64(AdaptiveMinGOGC) {
		gogcFloat = float64(AdaptiveMinGOGC)
	}
	if gogcFloat > float64(AdaptiveMaxGOGC) {
		gogcFloat = float64(AdaptiveMaxGOGC)
	}

	return int(math.Round(gogcFloat)), true
}

// ResolveTuningPlan derives GOMEMLIMIT and GOMAXPROCS settings from container limits,
// parsing custom memory ratio strings and applying headroom safety constraints.
func ResolveTuningPlan(limits Limits, envRatioStr string, defaultRatio float64, minHeadroomBytes int64) TuningPlan {
	return ResolveTuningPlanWithProfile(limits, envRatioStr, defaultRatio, minHeadroomBytes, GCProfileDefault, 0)
}

func resolveTuningRatio(envRatioStr string, defaultRatio float64, profile GCProfile) float64 {
	ratio := defaultRatio
	if profile == GCProfileMemoryConstrained && defaultRatio == DefaultMemoryRatio {
		ratio = DefaultMemoryConstrainedRatio
	}
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio <= 0 || ratio > 1.0 {
		ratio = DefaultMemoryRatio
	}

	if trimmed := strings.TrimSpace(envRatioStr); trimmed != "" {
		if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
			if !math.IsNaN(parsed) && !math.IsInf(parsed, 0) && parsed > 0 && parsed <= 1.0 {
				ratio = parsed
			}
		}
	}
	return ratio
}

func applyProfileGOGC(plan *TuningPlan, profile GCProfile, liveHeapEstimateBytes int64) {
	switch profile {
	case GCProfileLatencyCritical:
		plan.GOGC = DefaultLatencyCriticalGOGC
		plan.GOGCStr = strconv.Itoa(DefaultLatencyCriticalGOGC)
		plan.GOGCApplied = true
	case GCProfileMemoryConstrained:
		plan.GOGC = DefaultMemoryConstrainedGOGC
		plan.GOGCStr = strconv.Itoa(DefaultMemoryConstrainedGOGC)
		plan.GOGCApplied = true
	case GCProfileBatchETL:
		plan.GOGC = DefaultBatchETLGOGC
		plan.GOGCStr = "off"
		plan.GOGCApplied = true
	case GCProfileAdaptive:
		if liveHeapEstimateBytes > 0 && plan.GOMEMLIMITBytes > 0 {
			if gogc, ok := CalculateAdaptiveGOGC(plan.GOMEMLIMITBytes, liveHeapEstimateBytes); ok {
				plan.GOGC = gogc
				plan.GOGCStr = strconv.Itoa(gogc)
				plan.GOGCApplied = true
			}
		}
	case GCProfileDefault:
		// Default profile does not alter GOGC
	}
}

// ResolveTuningPlanWithProfile derives GOMEMLIMIT, GOMAXPROCS, and GOGC settings from container limits,
// taking into account the active GCProfile, live heap estimate, and custom memory ratios.
func ResolveTuningPlanWithProfile(
	limits Limits,
	envRatioStr string,
	defaultRatio float64,
	minHeadroomBytes int64,
	profile GCProfile,
	liveHeapEstimateBytes int64,
) TuningPlan {
	ratio := resolveTuningRatio(envRatioStr, defaultRatio, profile)

	if minHeadroomBytes <= 0 {
		minHeadroomBytes = DefaultMinHeadroomBytes
	}

	plan := TuningPlan{
		AppliedRatio: ratio,
		GCProfile:    profile,
	}

	effectiveLimit := limits.EffectiveMemoryLimitBytes
	if effectiveLimit <= 0 {
		effectiveLimit = CalculateEffectiveMemoryLimit(limits.MemoryLimitBytes, limits.MemoryHighBytes)
	}

	effectiveLimit = RetainedMemoryLimit(effectiveLimit, limits.RetainedExecutableBytes)

	if effectiveLimit > 0 {
		if memLimit, ok := CalculateGOMEMLIMIT(effectiveLimit, ratio, minHeadroomBytes); ok {
			plan.GOMEMLIMITBytes = memLimit
			plan.GOMEMLIMITStr = fmt.Sprintf("%dB", memLimit)
			plan.ConstrainingLimit = DetermineConstrainingLimit(limits.MemoryLimitBytes, limits.MemoryHighBytes)
			if plan.ConstrainingLimit == LimitConstraintNone {
				plan.ConstrainingLimit = LimitConstraintMax
			}
		}
	}

	if limits.CPUs > 0 {
		plan.GOMAXPROCS = limits.CPUs
		plan.GOMAXPROCSStr = strconv.Itoa(limits.CPUs)
	} else if limits.CPUQuota > 0 {
		if cpus, ok := CalculateGOMAXPROCS(limits.CPUQuota); ok {
			plan.GOMAXPROCS = cpus
			plan.GOMAXPROCSStr = strconv.Itoa(cpus)
		}
	}

	applyProfileGOGC(&plan, profile, liveHeapEstimateBytes)

	return plan
}

func readTrimmedFile(path string) (string, error) {
	// #nosec G304 -- reading sysfs cgroup files
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text()), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errors.New("empty file")
}
