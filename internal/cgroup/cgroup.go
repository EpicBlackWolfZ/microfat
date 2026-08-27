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

	defaultCgroupMount = "/sys/fs/cgroup"
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
	CgroupVersion    int     `json:"cgroup_version"`
	MemoryLimitBytes int64   `json:"memory_limit_bytes"` // 0 if unlimited
	CPUQuota         float64 `json:"cpu_quota"`          // 0 if unlimited
	CPUs             int     `json:"cpus"`               // Computed GOMAXPROCS (0 if unlimited)
}

// TuningPlan contains computed Go runtime tuning parameters derived from container resource limits.
type TuningPlan struct {
	GOMEMLIMITBytes int64     `json:"gomemlimit_bytes"`   // Calculated memory limit in bytes (0 if unset/unlimited)
	GOMEMLIMITStr   string    `json:"gomemlimit_str"`     // Formatted memory limit string (e.g. "966367641B", empty if unset)
	GOMAXPROCS      int       `json:"gomaxprocs"`         // Calculated CPU quota core count (0 if unset/unlimited)
	GOMAXPROCSStr   string    `json:"gomaxprocs_str"`     // Formatted GOMAXPROCS string (e.g. "4", empty if unset)
	AppliedRatio    float64   `json:"applied_ratio"`      // Actual memory ratio applied (e.g. 0.90 or custom)
	GOGC            int       `json:"gogc,omitempty"`     // Calculated GOGC target (-1 if off, 0 if unset/default)
	GOGCStr         string    `json:"gogc_str,omitempty"` // Formatted GOGC string (e.g. "75", "40", "off", empty if unset)
	GCProfile       GCProfile `json:"gc_profile,omitempty"`
	GOGCApplied     bool      `json:"gogc_applied"`
}

// ReadLimits inspects /sys/fs/cgroup and returns the active container limits.
func ReadLimits() (Limits, error) {
	return ReadLimitsFrom(defaultCgroupMount)
}

// ReadLimitsFrom inspects the specified cgroup root directory.
func ReadLimitsFrom(root string) (Limits, error) {
	cleanRoot := filepath.Clean(root)
	if _, err := os.Stat(cleanRoot); err != nil {
		return Limits{}, fmt.Errorf("cgroup mount %s not accessible: %w", cleanRoot, err)
	}

	// 1. Detect cgroup v2 (unified hierarchy: memory.max or cgroup.controllers exists at root)
	v2MemMax := filepath.Join(cleanRoot, "memory.max")
	if _, err := os.Stat(v2MemMax); err == nil {
		return readCgroupV2(cleanRoot)
	}

	// 2. Detect cgroup v1 (legacy hierarchy: memory/ and cpu/ subdirectories)
	v1MemLimit := filepath.Join(cleanRoot, "memory", "memory.limit_in_bytes")
	if _, err := os.Stat(v1MemLimit); err == nil {
		return readCgroupV1(cleanRoot)
	}

	return Limits{CgroupVersion: VersionUnknown}, nil
}

func readCgroupV2(root string) (Limits, error) {
	limits := Limits{CgroupVersion: VersionV2}

	// Memory limit from memory.max
	memPath := filepath.Join(root, "memory.max")
	memBytes, err := readTrimmedFile(memPath)
	if err == nil && memBytes != "max" && memBytes != "" {
		if val, parseErr := strconv.ParseInt(memBytes, 10, 64); parseErr == nil && val > 0 {
			limits.MemoryLimitBytes = val
		}
	}

	// CPU quota from cpu.max (format: "$quota $period" e.g. "200000 100000")
	cpuPath := filepath.Join(root, "cpu.max")
	cpuContent, err := readTrimmedFile(cpuPath)
	if err == nil && cpuContent != "" {
		parts := strings.Fields(cpuContent)
		const expectedCpuFields = 2
		if len(parts) == expectedCpuFields && parts[0] != "max" {
			quota, qErr := strconv.ParseFloat(parts[0], 64)
			period, pErr := strconv.ParseFloat(parts[1], 64)
			if qErr == nil && pErr == nil && period > 0 && quota > 0 {
				limits.CPUQuota = quota / period
				if cpus, ok := CalculateGOMAXPROCS(limits.CPUQuota); ok {
					limits.CPUs = cpus
				}
			}
		}
	}

	return limits, nil
}

func readCgroupV1(root string) (Limits, error) {
	limits := Limits{CgroupVersion: VersionV1}

	// Memory limit from memory/memory.limit_in_bytes
	memPath := filepath.Join(root, "memory", "memory.limit_in_bytes")
	memBytes, err := readTrimmedFile(memPath)
	if err == nil && memBytes != "" {
		if val, parseErr := strconv.ParseInt(memBytes, 10, 64); parseErr == nil && val > 0 {
			if val < UnlimitedCgroupV1MemoryThreshold {
				limits.MemoryLimitBytes = val
			}
		}
	}

	// CPU quota from cpu/cpu.cfs_quota_us and cpu/cpu.cfs_period_us
	quotaPath := filepath.Join(root, "cpu", "cpu.cfs_quota_us")
	periodPath := filepath.Join(root, "cpu", "cpu.cfs_period_us")
	quotaStr, qErr := readTrimmedFile(quotaPath)
	periodStr, pErr := readTrimmedFile(periodPath)

	if qErr == nil && pErr == nil {
		quota, qParseErr := strconv.ParseFloat(quotaStr, 64)
		period, pParseErr := strconv.ParseFloat(periodStr, 64)
		if qParseErr == nil && pParseErr == nil && quota > 0 && period > 0 {
			limits.CPUQuota = quota / period
			if cpus, ok := CalculateGOMAXPROCS(limits.CPUQuota); ok {
				limits.CPUs = cpus
			}
		}
	}

	return limits, nil
}

// CalculateGOMEMLIMIT computes the recommended GOMEMLIMIT in bytes given a raw memory limit.
// Returns (computedBytes, true) if a valid limit is determined, or (0, false) if unlimited or invalid.
func CalculateGOMEMLIMIT(limitBytes int64, ratio float64, minHeadroomBytes int64) (int64, bool) {
	if limitBytes <= 0 || limitBytes >= UnlimitedCgroupV1MemoryThreshold {
		return 0, false
	}
	if ratio <= 0 || ratio > 1.0 {
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

	return int64(chosen), true
}

// CalculateGOMAXPROCS computes the recommended GOMAXPROCS value from a fractional CPU quota.
// Returns (cpus, true) if a valid quota exists, or (0, false) if unlimited.
func CalculateGOMAXPROCS(quota float64) (int, bool) {
	if quota <= 0 {
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
	ratio := defaultRatio
	if profile == GCProfileMemoryConstrained && defaultRatio == DefaultMemoryRatio {
		ratio = DefaultMemoryConstrainedRatio
	}
	if ratio <= 0 || ratio > 1.0 {
		ratio = DefaultMemoryRatio
	}

	if trimmed := strings.TrimSpace(envRatioStr); trimmed != "" {
		if parsedRatio, rErr := strconv.ParseFloat(trimmed, 64); rErr == nil && parsedRatio > 0 && parsedRatio <= 1.0 {
			ratio = parsedRatio
		}
	}

	if minHeadroomBytes <= 0 {
		minHeadroomBytes = DefaultMinHeadroomBytes
	}

	plan := TuningPlan{
		AppliedRatio: ratio,
		GCProfile:    profile,
	}

	if limits.MemoryLimitBytes > 0 {
		if memLimit, ok := CalculateGOMEMLIMIT(limits.MemoryLimitBytes, ratio, minHeadroomBytes); ok {
			plan.GOMEMLIMITBytes = memLimit
			plan.GOMEMLIMITStr = fmt.Sprintf("%dB", memLimit)
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
