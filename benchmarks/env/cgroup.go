package env

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

const (
	cgroupVersionV1          = "v1"
	cgroupVersionV2          = "v2"
	cgroupVersionUnavailable = "unavailable"

	cgroupV2ControllersFile = "cgroup.controllers"
	cgroupV2MemoryMaxFile   = "memory.max"
	cgroupV2CPUMaxFile      = "cpu.max"

	cgroupV1MemoryLimitFile = "memory.limit_in_bytes"
	cgroupV1CPUQuotaFile    = "cpu.cfs_quota_us"
	cgroupV1CPUPeriodFile   = "cpu.cfs_period_us"

	cgroupMaxKeyword = "max"
	cgroupUnlimited  = "-1"

	cgroupExpectedCPUFields = 2
	cgroupProcSplitParts    = 3
	cgroupV2HierarchyID     = "0"

	unlimitedCgroupV1MemoryThreshold int64 = 1024 * 1024 * 1024 * 1024 * 1024 // 1 PB
)

func (d *Detector) detectCgroup() (schema.CgroupInfo, []string) {
	warnings := make([]string, 0)

	cleanRoot := filepath.Clean(d.cgroupRoot)
	if _, err := os.Stat(cleanRoot); err != nil {
		return schema.CgroupInfo{
			Version:        cgroupVersionUnavailable,
			MemoryMaxBytes: schema.NewUnavailableLimit[int64](),
			CPUQuotaUs:     schema.NewUnavailableLimit[int64](),
			CPUPeriodUs:    nil,
		}, []string{fmt.Sprintf("cgroup mount %q inaccessible: %v", cleanRoot, err)}
	}

	v2RelPath, hasV2, v1RelPaths := d.parseProcCgroupSafe()

	// Check for cgroup v2 unified hierarchy
	v2Controllers := filepath.Join(cleanRoot, cgroupV2ControllersFile)
	v2MemMax := filepath.Join(cleanRoot, cgroupV2MemoryMaxFile)
	_, errControllers := os.Stat(v2Controllers)
	_, errMemMax := os.Stat(v2MemMax)
	isV2Mount := errControllers == nil || errMemMax == nil

	// Check for cgroup v1 legacy hierarchy
	v1MemLimit := filepath.Join(cleanRoot, "memory", cgroupV1MemoryLimitFile)
	v1RootMemLimit := filepath.Join(cleanRoot, cgroupV1MemoryLimitFile)
	_, errV1Mem := os.Stat(v1MemLimit)
	_, errV1RootMem := os.Stat(v1RootMemLimit)
	isV1Mount := len(v1RelPaths) > 0 || errV1Mem == nil || errV1RootMem == nil

	if isV2Mount || (hasV2 && !isV1Mount) {
		info, warn := d.detectCgroupV2(cleanRoot, v2RelPath)
		if len(warn) > 0 {
			warnings = append(warnings, warn...)
		}
		return info, warnings
	}

	if isV1Mount {
		info, warn := d.detectCgroupV1(cleanRoot, v1RelPaths)
		if len(warn) > 0 {
			warnings = append(warnings, warn...)
		}
		return info, warnings
	}

	warnings = append(warnings, "cgroup hierarchy not detected")
	return schema.CgroupInfo{
		Version:        cgroupVersionUnavailable,
		MemoryMaxBytes: schema.NewUnavailableLimit[int64](),
		CPUQuotaUs:     schema.NewUnavailableLimit[int64](),
		CPUPeriodUs:    nil,
	}, warnings
}

func (d *Detector) parseProcCgroupSafe() (string, bool, map[string]string) {
	v1Paths := make(map[string]string)
	v2Path := ""
	hasV2 := false

	target := d.procCgroupPath
	if target == "" {
		target = defaultProcCgroupPath
	}

	f, err := os.Open(filepath.Clean(target))
	if err != nil {
		return "", false, v1Paths
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", cgroupProcSplitParts)
		if len(parts) != cgroupProcSplitParts {
			continue
		}
		hID := parts[0]
		ctrls := parts[1]
		rel := parts[2]

		if hID == cgroupV2HierarchyID && ctrls == "" {
			v2Path = rel
			hasV2 = true
		} else {
			for _, ctrl := range strings.Split(ctrls, ",") {
				c := strings.TrimSpace(ctrl)
				if c != "" {
					v1Paths[c] = rel
				}
			}
		}
	}
	return v2Path, hasV2, v1Paths
}

func (d *Detector) detectCgroupV2(root, relPath string) (schema.CgroupInfo, []string) {
	var warnings []string

	targetDir := root
	if relPath != "" && relPath != "/" && relPath != "." {
		candidate := filepath.Join(root, filepath.Clean("/"+relPath))
		if isSafeSubpath(root, candidate) {
			if _, err := os.Stat(candidate); err == nil {
				targetDir = candidate
			}
		}
	}

	// Read Memory Limit
	memLimit, memFound, memErr := traverseCgroupV2MemoryMax(targetDir, root)
	var memMaxLimit schema.ResourceLimit[int64]
	switch {
	case memErr != nil:
		memMaxLimit = schema.NewUnavailableLimit[int64]()
		warnings = append(warnings, fmt.Sprintf("failed to read cgroup v2 memory.max: %v", memErr))
	case memFound:
		memMaxLimit = schema.NewFiniteLimit(memLimit)
	default:
		memMaxLimit = schema.NewUnlimitedLimit[int64]()
	}

	// Read CPU Quota & Period
	cpuQuota, cpuPeriod, cpuFound, cpuErr := traverseCgroupV2CPUMax(targetDir, root)
	var cpuQuotaLimit schema.ResourceLimit[int64]
	switch {
	case cpuErr != nil:
		cpuQuotaLimit = schema.NewUnavailableLimit[int64]()
		warnings = append(warnings, fmt.Sprintf("failed to read cgroup v2 cpu.max: %v", cpuErr))
	case cpuFound:
		cpuQuotaLimit = schema.NewFiniteLimit(cpuQuota)
	default:
		cpuQuotaLimit = schema.NewUnlimitedLimit[int64]()
	}

	return schema.CgroupInfo{
		Version:        cgroupVersionV2,
		MemoryMaxBytes: memMaxLimit,
		CPUQuotaUs:     cpuQuotaLimit,
		CPUPeriodUs:    cpuPeriod,
	}, warnings
}

func traverseCgroupV2MemoryMax(targetDir, root string) (int64, bool, error) {
	var minMem int64
	hasMem := false
	curr := targetDir

	for {
		filePath := filepath.Join(curr, cgroupV2MemoryMaxFile)
		valStr, err := readTrimmedLine(filePath)
		if err == nil {
			if valStr != cgroupMaxKeyword && valStr != "" {
				parsed, pErr := strconv.ParseInt(valStr, 10, 64)
				if pErr != nil || parsed <= 0 {
					return 0, false, fmt.Errorf("corrupted memory.max %q: %w", valStr, pErr)
				}
				if !hasMem || parsed < minMem {
					minMem = parsed
					hasMem = true
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, false, err
		}

		if curr == root {
			break
		}
		parent := filepath.Dir(curr)
		if parent == curr || !isSafeSubpath(root, parent) {
			break
		}
		curr = parent
	}

	return minMem, hasMem, nil
}

func traverseCgroupV2CPUMax(targetDir, root string) (int64, *int64, bool, error) {
	var minQuota int64
	var foundPeriod *int64
	hasQuota := false
	curr := targetDir

	for {
		filePath := filepath.Join(curr, cgroupV2CPUMaxFile)
		valStr, err := readTrimmedLine(filePath)
		if err == nil {
			fields := strings.Fields(valStr)
			if len(fields) != cgroupExpectedCPUFields {
				return 0, nil, false, fmt.Errorf("invalid cpu.max format %q in %s", valStr, filePath)
			}
			qStr := fields[0]
			pStr := fields[1]

			period, pErr := strconv.ParseInt(pStr, 10, 64)
			if pErr != nil || period <= 0 {
				return 0, nil, false, fmt.Errorf("invalid cpu.max period %q in %s: %w", pStr, filePath, pErr)
			}
			if foundPeriod == nil {
				foundPeriod = &period
			}

			if qStr != cgroupMaxKeyword && qStr != "" {
				parsedQuota, qErr := strconv.ParseInt(qStr, 10, 64)
				if qErr != nil || parsedQuota <= 0 {
					return 0, nil, false, fmt.Errorf("corrupted cpu.max quota %q: %w", qStr, qErr)
				}
				if !hasQuota || parsedQuota < minQuota {
					minQuota = parsedQuota
					hasQuota = true
					foundPeriod = &period
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, nil, false, err
		}

		if curr == root {
			break
		}
		parent := filepath.Dir(curr)
		if parent == curr || !isSafeSubpath(root, parent) {
			break
		}
		curr = parent
	}

	return minQuota, foundPeriod, hasQuota, nil
}

func (d *Detector) detectCgroupV1(root string, v1RelPaths map[string]string) (schema.CgroupInfo, []string) {
	var warnings []string

	memBase := filepath.Join(root, "memory")
	if _, err := os.Stat(memBase); err != nil {
		memBase = root
	}
	memRel := v1RelPaths["memory"]
	targetMem := memBase
	if memRel != "" && memRel != "/" && memRel != "." {
		candidate := filepath.Join(memBase, filepath.Clean("/"+memRel))
		if isSafeSubpath(memBase, candidate) {
			if _, err := os.Stat(candidate); err == nil {
				targetMem = candidate
			}
		}
	}

	memLimit, memFound, memErr := traverseCgroupV1Memory(memBase, targetMem)
	var memMaxLimit schema.ResourceLimit[int64]
	switch {
	case memErr != nil:
		memMaxLimit = schema.NewUnavailableLimit[int64]()
		warnings = append(warnings, fmt.Sprintf("failed to read cgroup v1 memory limit: %v", memErr))
	case memFound:
		memMaxLimit = schema.NewFiniteLimit(memLimit)
	default:
		memMaxLimit = schema.NewUnlimitedLimit[int64]()
	}

	cpuBase := filepath.Join(root, "cpu")
	if _, err := os.Stat(cpuBase); err != nil {
		cpuAcct := filepath.Join(root, "cpu,cpuacct")
		if _, aErr := os.Stat(cpuAcct); aErr == nil {
			cpuBase = cpuAcct
		} else {
			cpuBase = root
		}
	}
	cpuRel := v1RelPaths["cpu"]
	if cpuRel == "" {
		cpuRel = v1RelPaths["cpu,cpuacct"]
	}
	targetCPU := cpuBase
	if cpuRel != "" && cpuRel != "/" && cpuRel != "." {
		candidate := filepath.Join(cpuBase, filepath.Clean("/"+cpuRel))
		if isSafeSubpath(cpuBase, candidate) {
			if _, err := os.Stat(candidate); err == nil {
				targetCPU = candidate
			}
		}
	}

	cpuQuota, cpuPeriod, cpuFound, cpuErr := traverseCgroupV1CPU(cpuBase, targetCPU)
	var cpuQuotaLimit schema.ResourceLimit[int64]
	switch {
	case cpuErr != nil:
		cpuQuotaLimit = schema.NewUnavailableLimit[int64]()
		warnings = append(warnings, fmt.Sprintf("failed to read cgroup v1 cpu quota: %v", cpuErr))
	case cpuFound:
		cpuQuotaLimit = schema.NewFiniteLimit(cpuQuota)
	default:
		cpuQuotaLimit = schema.NewUnlimitedLimit[int64]()
	}

	return schema.CgroupInfo{
		Version:        cgroupVersionV1,
		MemoryMaxBytes: memMaxLimit,
		CPUQuotaUs:     cpuQuotaLimit,
		CPUPeriodUs:    cpuPeriod,
	}, warnings
}

func traverseCgroupV1Memory(base, target string) (int64, bool, error) {
	var minMem int64
	hasMem := false
	curr := target

	for {
		filePath := filepath.Join(curr, cgroupV1MemoryLimitFile)
		valStr, err := readTrimmedLine(filePath)
		if err == nil {
			if valStr != cgroupUnlimited && valStr != "" {
				parsed, pErr := strconv.ParseInt(valStr, 10, 64)
				if pErr != nil || parsed <= 0 {
					return 0, false, fmt.Errorf("corrupted memory.limit_in_bytes %q: %w", valStr, pErr)
				}
				if parsed < unlimitedCgroupV1MemoryThreshold {
					if !hasMem || parsed < minMem {
						minMem = parsed
						hasMem = true
					}
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, false, err
		}

		if curr == base {
			break
		}
		parent := filepath.Dir(curr)
		if parent == curr || !isSafeSubpath(base, parent) {
			break
		}
		curr = parent
	}

	return minMem, hasMem, nil
}

func traverseCgroupV1CPU(base, target string) (int64, *int64, bool, error) {
	var minQuota int64
	var foundPeriod *int64
	hasQuota := false
	curr := target

	for {
		var currPeriod *int64
		periodPath := filepath.Join(curr, cgroupV1CPUPeriodFile)
		pStr, pErr := readTrimmedLine(periodPath)
		if pErr == nil && pStr != "" {
			if period, parseErr := strconv.ParseInt(pStr, 10, 64); parseErr == nil && period > 0 {
				currPeriod = &period
				if foundPeriod == nil {
					foundPeriod = &period
				}
			}
		}

		quotaPath := filepath.Join(curr, cgroupV1CPUQuotaFile)
		qStr, qErr := readTrimmedLine(quotaPath)
		if qErr == nil {
			if qStr != cgroupUnlimited && qStr != "" {
				parsedQuota, parseErr := strconv.ParseInt(qStr, 10, 64)
				if parseErr != nil || parsedQuota <= 0 {
					return 0, nil, false, fmt.Errorf("corrupted cpu.cfs_quota_us %q: %w", qStr, parseErr)
				}
				if !hasQuota || parsedQuota < minQuota {
					minQuota = parsedQuota
					hasQuota = true
					if currPeriod != nil {
						foundPeriod = currPeriod
					}
				}
			}
		} else if !errors.Is(qErr, os.ErrNotExist) {
			return 0, nil, false, qErr
		}

		if curr == base {
			break
		}
		parent := filepath.Dir(curr)
		if parent == curr || !isSafeSubpath(base, parent) {
			break
		}
		curr = parent
	}

	return minQuota, foundPeriod, hasQuota, nil
}

func isSafeSubpath(root, path string) bool {
	if root == path || root == string(filepath.Separator) {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

func readTrimmedLine(path string) (string, error) {
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
