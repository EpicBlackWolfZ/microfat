package cgroup

import (
	"os"
	"path/filepath"
	"testing"
)

const (
	testBytes1GB = int64(1024 * 1024 * 1024)
	testBytes2GB = int64(2 * 1024 * 1024 * 1024)
)

func TestReadLimitsCgroupV2(t *testing.T) {
	tempDir := t.TempDir()

	// Mock cgroup v2 files
	memPath := filepath.Join(tempDir, "memory.max")
	cpuPath := filepath.Join(tempDir, "cpu.max")

	if err := os.WriteFile(memPath, []byte("1073741824\n"), 0o644); err != nil {
		t.Fatalf("writing memory.max: %v", err)
	}
	if err := os.WriteFile(cpuPath, []byte("250000 100000\n"), 0o644); err != nil {
		t.Fatalf("writing cpu.max: %v", err)
	}

	limits, err := ReadLimitsFrom(tempDir)
	if err != nil {
		t.Fatalf("ReadLimitsFrom failed: %v", err)
	}

	if limits.CgroupVersion != VersionV2 {
		t.Errorf("expected VersionV2, got %d", limits.CgroupVersion)
	}
	if limits.MemoryLimitBytes != testBytes1GB {
		t.Errorf("expected MemoryLimitBytes %d, got %d", testBytes1GB, limits.MemoryLimitBytes)
	}
	const expectedQuota = 2.5
	if limits.CPUQuota != expectedQuota {
		t.Errorf("expected CPUQuota %f, got %f", expectedQuota, limits.CPUQuota)
	}
	if limits.CPUs != 2 {
		t.Errorf("expected CPUs 2, got %d", limits.CPUs)
	}
}

func TestReadLimitsCgroupV2Unlimited(t *testing.T) {
	tempDir := t.TempDir()

	memPath := filepath.Join(tempDir, "memory.max")
	cpuPath := filepath.Join(tempDir, "cpu.max")

	if err := os.WriteFile(memPath, []byte("max\n"), 0o644); err != nil {
		t.Fatalf("writing memory.max: %v", err)
	}
	if err := os.WriteFile(cpuPath, []byte("max 100000\n"), 0o644); err != nil {
		t.Fatalf("writing cpu.max: %v", err)
	}

	limits, err := ReadLimitsFrom(tempDir)
	if err != nil {
		t.Fatalf("ReadLimitsFrom failed: %v", err)
	}

	if limits.MemoryLimitBytes != 0 {
		t.Errorf("expected MemoryLimitBytes 0 for unlimited, got %d", limits.MemoryLimitBytes)
	}
	if limits.CPUQuota != 0 || limits.CPUs != 0 {
		t.Errorf("expected CPUQuota 0 and CPUs 0 for unlimited, got quota=%f cpus=%d", limits.CPUQuota, limits.CPUs)
	}
}

func TestReadLimitsCgroupV1(t *testing.T) {
	tempDir := t.TempDir()

	memDir := filepath.Join(tempDir, "memory")
	cpuDir := filepath.Join(tempDir, "cpu")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("mkdir memory: %v", err)
	}
	if err := os.MkdirAll(cpuDir, 0o755); err != nil {
		t.Fatalf("mkdir cpu: %v", err)
	}

	memPath := filepath.Join(memDir, "memory.limit_in_bytes")
	quotaPath := filepath.Join(cpuDir, "cpu.cfs_quota_us")
	periodPath := filepath.Join(cpuDir, "cpu.cfs_period_us")

	if err := os.WriteFile(memPath, []byte("2147483648\n"), 0o644); err != nil {
		t.Fatalf("writing memory.limit_in_bytes: %v", err)
	}
	if err := os.WriteFile(quotaPath, []byte("400000\n"), 0o644); err != nil {
		t.Fatalf("writing cpu.cfs_quota_us: %v", err)
	}
	if err := os.WriteFile(periodPath, []byte("100000\n"), 0o644); err != nil {
		t.Fatalf("writing cpu.cfs_period_us: %v", err)
	}

	limits, err := ReadLimitsFrom(tempDir)
	if err != nil {
		t.Fatalf("ReadLimitsFrom failed: %v", err)
	}

	if limits.CgroupVersion != VersionV1 {
		t.Errorf("expected VersionV1, got %d", limits.CgroupVersion)
	}
	if limits.MemoryLimitBytes != testBytes2GB {
		t.Errorf("expected MemoryLimitBytes %d, got %d", testBytes2GB, limits.MemoryLimitBytes)
	}
	const expectedV1Quota = 4.0
	if limits.CPUQuota != expectedV1Quota || limits.CPUs != 4 {
		t.Errorf("expected CPUQuota 4.0 and CPUs 4, got quota=%f cpus=%d", limits.CPUQuota, limits.CPUs)
	}
}

func TestReadLimitsNonExistentMount(t *testing.T) {
	_, err := ReadLimitsFrom("/non/existent/path/for/test")
	if err == nil {
		t.Errorf("expected error for non-existent path")
	}
}

func TestReadLimitsUnknown(t *testing.T) {
	tempDir := t.TempDir()
	limits, err := ReadLimitsFrom(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if limits.CgroupVersion != VersionUnknown {
		t.Errorf("expected VersionUnknown, got %d", limits.CgroupVersion)
	}
}

func TestReadTrimmedFileErrors(t *testing.T) {
	tempDir := t.TempDir()
	emptyPath := filepath.Join(tempDir, "empty")
	if err := os.WriteFile(emptyPath, []byte(""), 0o644); err != nil {
		t.Fatalf("writing empty file: %v", err)
	}
	_, err := readTrimmedFile(emptyPath)
	if err == nil {
		t.Errorf("expected error reading empty file")
	}
}

func TestCalculateGOMEMLIMIT(t *testing.T) {
	expected90Pct := (testBytes1GB * 90) / 100
	expected80Pct := (testBytes1GB * 80) / 100

	tests := []struct {
		name        string
		limitBytes  int64
		ratio       float64
		headroom    int64
		expected    int64
		expectValid bool
	}{
		{
			name:        "1GB with default 90% and 64MB headroom",
			limitBytes:  testBytes1GB,
			ratio:       DefaultMemoryRatio,
			headroom:    DefaultMinHeadroomBytes,
			expected:    expected90Pct,
			expectValid: true,
		},
		{
			name:        "Custom 80% ratio",
			limitBytes:  testBytes1GB,
			ratio:       0.80,
			headroom:    DefaultMinHeadroomBytes,
			expected:    expected80Pct,
			expectValid: true,
		},
		{
			name:        "Invalid zero ratio fallback to default",
			limitBytes:  testBytes1GB,
			ratio:       0,
			headroom:    0,
			expected:    expected90Pct,
			expectValid: true,
		},
		{
			name:        "Unlimited cgroup v1 value",
			limitBytes:  UnlimitedCgroupV1MemoryThreshold + 1000,
			ratio:       DefaultMemoryRatio,
			headroom:    DefaultMinHeadroomBytes,
			expected:    0,
			expectValid: false,
		},
		{
			name:        "Zero / negative limit",
			limitBytes:  0,
			ratio:       DefaultMemoryRatio,
			headroom:    DefaultMinHeadroomBytes,
			expected:    0,
			expectValid: false,
		},
		{
			name:        "Very small 32MB container",
			limitBytes:  32 * 1024 * 1024,
			ratio:       DefaultMemoryRatio,
			headroom:    DefaultMinHeadroomBytes,
			expected:    16 * 1024 * 1024,
			expectValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CalculateGOMEMLIMIT(tt.limitBytes, tt.ratio, tt.headroom)
			if ok != tt.expectValid {
				t.Fatalf("expected valid=%v, got %v", tt.expectValid, ok)
			}
			if ok && got != tt.expected {
				t.Errorf("CalculateGOMEMLIMIT() = %d, expected %d", got, tt.expected)
			}
		})
	}
}

func TestCalculateGOMAXPROCS(t *testing.T) {
	tests := []struct {
		name        string
		quota       float64
		expected    int
		expectValid bool
	}{
		{
			name:        "Sub-1 core (0.5 CPUs)",
			quota:       0.5,
			expected:    1,
			expectValid: true,
		},
		{
			name:        "Fractional 2.5 CPUs",
			quota:       2.5,
			expected:    2,
			expectValid: true,
		},
		{
			name:        "Exact 4.0 CPUs",
			quota:       4.0,
			expected:    4,
			expectValid: true,
		},
		{
			name:        "Zero or negative quota (unlimited)",
			quota:       0,
			expected:    0,
			expectValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CalculateGOMAXPROCS(tt.quota)
			if ok != tt.expectValid {
				t.Fatalf("expected valid=%v, got %v", tt.expectValid, ok)
			}
			if ok && got != tt.expected {
				t.Errorf("CalculateGOMAXPROCS() = %d, expected %d", got, tt.expected)
			}
		})
	}
}

func TestReadLimitsHost(t *testing.T) {
	_, _ = ReadLimits()
}

func TestReadTrimmedFile(t *testing.T) {
	tempDir := t.TempDir()
	emptyFile := filepath.Join(tempDir, "empty")
	_ = os.WriteFile(emptyFile, []byte(""), 0o644)

	_, err := readTrimmedFile(emptyFile)
	if err == nil {
		t.Errorf("expected error reading empty file")
	}

	_, err = readTrimmedFile(filepath.Join(tempDir, "nonexistent"))
	if err == nil {
		t.Errorf("expected error reading nonexistent file")
	}
}

func TestResolveTuningPlan(t *testing.T) {
	const (
		mem32MB            = int64(32 * 1024 * 1024)
		mem16MB            = int64(16 * 1024 * 1024)
		customHeadroomBytes = int64(128 * 1024 * 1024)
		expectedCustomRatio = 0.80
		expectedCustom85    = 0.85
	)

	expected1GB90Pct := (testBytes1GB * 90) / 100
	expected1GB80Pct := (testBytes1GB * 80) / 100

	tests := []struct {
		name                 string
		limits               Limits
		envRatioStr          string
		defaultRatio         float64
		minHeadroomBytes     int64
		expectedMemBytes     int64
		expectedMemStr       string
		expectedMaxProcs     int
		expectedMaxProcsStr  string
		expectedAppliedRatio float64
	}{
		{
			name: "Standard 1GB 4-core with defaults",
			limits: Limits{
				CgroupVersion:    VersionV2,
				MemoryLimitBytes: testBytes1GB,
				CPUQuota:         4.0,
				CPUs:             4,
			},
			envRatioStr:          "",
			defaultRatio:         DefaultMemoryRatio,
			minHeadroomBytes:     DefaultMinHeadroomBytes,
			expectedMemBytes:     expected1GB90Pct,
			expectedMemStr:       "966367641B",
			expectedMaxProcs:     4,
			expectedMaxProcsStr:  "4",
			expectedAppliedRatio: DefaultMemoryRatio,
		},
		{
			name: "Custom env ratio override",
			limits: Limits{
				CgroupVersion:    VersionV2,
				MemoryLimitBytes: testBytes1GB,
				CPUQuota:         2.5,
				CPUs:             2,
			},
			envRatioStr:          "0.80",
			defaultRatio:         DefaultMemoryRatio,
			minHeadroomBytes:     DefaultMinHeadroomBytes,
			expectedMemBytes:     expected1GB80Pct,
			expectedMemStr:       "858993459B",
			expectedMaxProcs:     2,
			expectedMaxProcsStr:  "2",
			expectedAppliedRatio: expectedCustomRatio,
		},
		{
			name: "Invalid env ratio fallback to default ratio",
			limits: Limits{
				CgroupVersion:    VersionV2,
				MemoryLimitBytes: testBytes1GB,
			},
			envRatioStr:          "invalid_ratio",
			defaultRatio:         expectedCustom85,
			minHeadroomBytes:     DefaultMinHeadroomBytes,
			expectedMemBytes:     (testBytes1GB * 85) / 100,
			expectedMemStr:       "912680550B",
			expectedMaxProcs:     0,
			expectedMaxProcsStr:  "",
			expectedAppliedRatio: expectedCustom85,
		},
		{
			name: "Negative and >1.0 env ratio fallback to default",
			limits: Limits{
				CgroupVersion:    VersionV2,
				MemoryLimitBytes: testBytes1GB,
			},
			envRatioStr:          "1.5",
			defaultRatio:         0, // invalid default should also fallback to DefaultMemoryRatio (0.90)
			minHeadroomBytes:     0, // 0 headroom should fallback to DefaultMinHeadroomBytes
			expectedMemBytes:     expected1GB90Pct,
			expectedMemStr:       "966367641B",
			expectedMaxProcs:     0,
			expectedMaxProcsStr:  "",
			expectedAppliedRatio: DefaultMemoryRatio,
		},
		{
			name: "Unlimited memory and quota",
			limits: Limits{
				CgroupVersion:    VersionV2,
				MemoryLimitBytes: 0,
				CPUQuota:         0,
				CPUs:             0,
			},
			envRatioStr:          "",
			defaultRatio:         DefaultMemoryRatio,
			minHeadroomBytes:     DefaultMinHeadroomBytes,
			expectedMemBytes:     0,
			expectedMemStr:       "",
			expectedMaxProcs:     0,
			expectedMaxProcsStr:  "",
			expectedAppliedRatio: DefaultMemoryRatio,
		},
		{
			name: "Unlimited cgroup v1 memory threshold",
			limits: Limits{
				CgroupVersion:    VersionV1,
				MemoryLimitBytes: UnlimitedCgroupV1MemoryThreshold + 1024,
			},
			envRatioStr:          "",
			defaultRatio:         DefaultMemoryRatio,
			minHeadroomBytes:     DefaultMinHeadroomBytes,
			expectedMemBytes:     0,
			expectedMemStr:       "",
			expectedMaxProcs:     0,
			expectedMaxProcsStr:  "",
			expectedAppliedRatio: DefaultMemoryRatio,
		},
		{
			name: "Fractional CPU quota when CPUs field is unpopulated",
			limits: Limits{
				CgroupVersion: VersionV2,
				CPUQuota:      2.5,
				CPUs:          0,
			},
			envRatioStr:          "",
			defaultRatio:         DefaultMemoryRatio,
			minHeadroomBytes:     DefaultMinHeadroomBytes,
			expectedMemBytes:     0,
			expectedMemStr:       "",
			expectedMaxProcs:     2,
			expectedMaxProcsStr:  "2",
			expectedAppliedRatio: DefaultMemoryRatio,
		},
		{
			name: "Sub-core CPU quota (0.5 CPUs) minimum core constraint",
			limits: Limits{
				CgroupVersion: VersionV2,
				CPUQuota:      0.5,
				CPUs:          0,
			},
			envRatioStr:          "",
			defaultRatio:         DefaultMemoryRatio,
			minHeadroomBytes:     DefaultMinHeadroomBytes,
			expectedMemBytes:     0,
			expectedMemStr:       "",
			expectedMaxProcs:     1,
			expectedMaxProcsStr:  "1",
			expectedAppliedRatio: DefaultMemoryRatio,
		},
		{
			name: "Small 32MB container safety fallback",
			limits: Limits{
				CgroupVersion:    VersionV2,
				MemoryLimitBytes: mem32MB,
			},
			envRatioStr:          "",
			defaultRatio:         DefaultMemoryRatio,
			minHeadroomBytes:     DefaultMinHeadroomBytes,
			expectedMemBytes:     mem16MB,
			expectedMemStr:       "16777216B",
			expectedMaxProcs:     0,
			expectedMaxProcsStr:  "",
			expectedAppliedRatio: DefaultMemoryRatio,
		},
		{
			name: "Custom headroom parameter takes precedence when smaller than ratio",
			limits: Limits{
				CgroupVersion:    VersionV2,
				MemoryLimitBytes: testBytes2GB,
			},
			envRatioStr:          "",
			defaultRatio:         DefaultMemoryRatio,
			minHeadroomBytes:     int64(512 * 1024 * 1024),
			expectedMemBytes:     testBytes2GB - int64(512*1024*1024),
			expectedMemStr:       "1610612736B",
			expectedMaxProcs:     0,
			expectedMaxProcsStr:  "",
			expectedAppliedRatio: DefaultMemoryRatio,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := ResolveTuningPlan(tt.limits, tt.envRatioStr, tt.defaultRatio, tt.minHeadroomBytes)

			if plan.GOMEMLIMITBytes != tt.expectedMemBytes {
				t.Errorf("GOMEMLIMITBytes = %d, expected %d", plan.GOMEMLIMITBytes, tt.expectedMemBytes)
			}
			if plan.GOMEMLIMITStr != tt.expectedMemStr {
				t.Errorf("GOMEMLIMITStr = %q, expected %q", plan.GOMEMLIMITStr, tt.expectedMemStr)
			}
			if plan.GOMAXPROCS != tt.expectedMaxProcs {
				t.Errorf("GOMAXPROCS = %d, expected %d", plan.GOMAXPROCS, tt.expectedMaxProcs)
			}
			if plan.GOMAXPROCSStr != tt.expectedMaxProcsStr {
				t.Errorf("GOMAXPROCSStr = %q, expected %q", plan.GOMAXPROCSStr, tt.expectedMaxProcsStr)
			}
			if plan.AppliedRatio != tt.expectedAppliedRatio {
				t.Errorf("AppliedRatio = %f, expected %f", plan.AppliedRatio, tt.expectedAppliedRatio)
			}
		})
	}
}
