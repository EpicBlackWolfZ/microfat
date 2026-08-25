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
