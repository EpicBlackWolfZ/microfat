package cgroup

import (
	"errors"
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
	procFile := filepath.Join(tempDir, "proc_cgroup")
	if err := os.WriteFile(procFile, []byte("0::/\n"), 0o600); err != nil {
		t.Fatalf("writing procFile: %v", err)
	}

	// Mock cgroup v2 files
	memPath := filepath.Join(tempDir, "memory.max")
	cpuPath := filepath.Join(tempDir, "cpu.max")

	if err := os.WriteFile(memPath, []byte("1073741824\n"), 0o644); err != nil {
		t.Fatalf("writing memory.max: %v", err)
	}
	if err := os.WriteFile(cpuPath, []byte("250000 100000\n"), 0o644); err != nil {
		t.Fatalf("writing cpu.max: %v", err)
	}

	limits, err := ReadLimitsCustom(tempDir, procFile)
	if err != nil {
		t.Fatalf("ReadLimitsCustom failed: %v", err)
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
	procFile := filepath.Join(tempDir, "proc_cgroup")
	if err := os.WriteFile(procFile, []byte("0::/\n"), 0o600); err != nil {
		t.Fatalf("writing procFile: %v", err)
	}

	memPath := filepath.Join(tempDir, "memory.max")
	cpuPath := filepath.Join(tempDir, "cpu.max")

	if err := os.WriteFile(memPath, []byte("max\n"), 0o644); err != nil {
		t.Fatalf("writing memory.max: %v", err)
	}
	if err := os.WriteFile(cpuPath, []byte("max 100000\n"), 0o644); err != nil {
		t.Fatalf("writing cpu.max: %v", err)
	}

	limits, err := ReadLimitsCustom(tempDir, procFile)
	if err != nil {
		t.Fatalf("ReadLimitsCustom failed: %v", err)
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
	procFile := filepath.Join(tempDir, "proc_cgroup")
	if err := os.WriteFile(procFile, []byte("1:memory:/\n2:cpu:/\n"), 0o600); err != nil {
		t.Fatalf("writing procFile: %v", err)
	}

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

	limits, err := ReadLimitsCustom(tempDir, procFile)
	if err != nil {
		t.Fatalf("ReadLimitsCustom failed: %v", err)
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
	limits, err := ReadLimitsFrom("/non/existent/path/for/test")
	if err == nil {
		t.Errorf("expected error for non-existent path")
	}
	if limits.CgroupVersion != VersionUnknown {
		t.Errorf("expected VersionUnknown on inaccessible mount, got %d", limits.CgroupVersion)
	}
	if !errors.Is(err, ErrCgroupMountInaccessible) {
		t.Errorf("expected ErrCgroupMountInaccessible, got %v", err)
	}
}

func TestReadLimitsUnknown(t *testing.T) {
	tempDir := t.TempDir()
	procFile := filepath.Join(tempDir, "proc_cgroup")
	if err := os.WriteFile(procFile, []byte("0::/\n"), 0o600); err != nil {
		t.Fatalf("writing procFile: %v", err)
	}
	limits, err := ReadLimitsCustom(tempDir, procFile)
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

func TestParseGCProfile(t *testing.T) {
	tests := []struct {
		input       string
		expected    GCProfile
		expectError bool
	}{
		{"", GCProfileDefault, false},
		{"default", GCProfileDefault, false},
		{"latency_critical", GCProfileLatencyCritical, false},
		{"latency-critical", GCProfileLatencyCritical, false},
		{"latency", GCProfileLatencyCritical, false},
		{"LATENCY_CRITICAL", GCProfileLatencyCritical, false},
		{"memory_constrained", GCProfileMemoryConstrained, false},
		{"memory-constrained", GCProfileMemoryConstrained, false},
		{"memory", GCProfileMemoryConstrained, false},
		{"batch_etl", GCProfileBatchETL, false},
		{"batch-etl", GCProfileBatchETL, false},
		{"batch", GCProfileBatchETL, false},
		{"etl", GCProfileBatchETL, false},
		{"adaptive", GCProfileAdaptive, false},
		{"dynamic", GCProfileAdaptive, false},
		{"ADAPTIVE", GCProfileAdaptive, false},
		{"unknown_profile", GCProfileDefault, true},
		{"invalid", GCProfileDefault, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseGCProfile(tt.input)
			if (err != nil) != tt.expectError {
				t.Fatalf("ParseGCProfile(%q) error = %v, expectError = %v", tt.input, err, tt.expectError)
			}
			if got != tt.expected {
				t.Errorf("ParseGCProfile(%q) = %q, expected %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestParseByteSize(t *testing.T) {
	const (
		bytes1KB = int64(1024)
		bytes1MB = int64(1024 * 1024)
		bytes1GB = int64(1024 * 1024 * 1024)
		bytes1TB = int64(1024 * 1024 * 1024 * 1024)
	)

	tests := []struct {
		input       string
		expected    int64
		expectError bool
	}{
		{"1024", 1024, false},
		{"1024B", 1024, false},
		{"1024 bytes", 1024, false},
		{"1K", bytes1KB, false},
		{"1KB", bytes1KB, false},
		{"1KiB", bytes1KB, false},
		{"150M", 150 * bytes1MB, false},
		{"150MB", 150 * bytes1MB, false},
		{"150MiB", 150 * bytes1MB, false},
		{"1.5G", int64(1.5 * float64(bytes1GB)), false},
		{"1GB", bytes1GB, false},
		{"1GiB", bytes1GB, false},
		{"2T", 2 * bytes1TB, false},
		{"2TB", 2 * bytes1TB, false},
		{"2TiB", 2 * bytes1TB, false},
		{"0", 0, false},
		{"0MB", 0, false},
		{"", 0, true},
		{"   ", 0, true},
		{"-10MB", 0, true},
		{"invalid", 0, true},
		{"100XB", 0, true},
		{"99999999999999999999TB", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseByteSize(tt.input)
			if (err != nil) != tt.expectError {
				t.Fatalf("ParseByteSize(%q) error = %v, expectError = %v", tt.input, err, tt.expectError)
			}
			if !tt.expectError && got != tt.expected {
				t.Errorf("ParseByteSize(%q) = %d, expected %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestCalculateAdaptiveGOGC(t *testing.T) {
	const (
		mb100  = int64(100 * 1024 * 1024)
		mb450  = int64(450 * 1024 * 1024)
		mb600  = int64(600 * 1024 * 1024)
		mb900  = int64(900 * 1024 * 1024)
		mb1000 = int64(1000 * 1024 * 1024)
	)

	tests := []struct {
		name        string
		headroom    int64
		liveHeap    int64
		expected    int
		expectValid bool
	}{
		{
			name:        "Ample Headroom (ratio=2.0 -> GOGC=100 clamped)",
			headroom:    mb900,
			liveHeap:    mb450,
			expected:    100,
			expectValid: true,
		},
		{
			name:        "Moderate Headroom (ratio=1.5 -> GOGC=50)",
			headroom:    mb900,
			liveHeap:    mb600,
			expected:    50,
			expectValid: true,
		},
		{
			name:        "Tight Headroom (ratio=1.25 -> GOGC=25)",
			headroom:    int64(125 * 1024 * 1024),
			liveHeap:    mb100,
			expected:    25,
			expectValid: true,
		},
		{
			name:        "Starved Headroom (liveHeap > headroom -> GOGC=10 clamped)",
			headroom:    mb900,
			liveHeap:    mb1000,
			expected:    10,
			expectValid: true,
		},
		{
			name:        "Zero or Negative Live Heap",
			headroom:    mb900,
			liveHeap:    0,
			expected:    0,
			expectValid: false,
		},
		{
			name:        "Zero or Negative Headroom",
			headroom:    0,
			liveHeap:    mb100,
			expected:    0,
			expectValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CalculateAdaptiveGOGC(tt.headroom, tt.liveHeap)
			if ok != tt.expectValid {
				t.Fatalf("CalculateAdaptiveGOGC() ok = %v, expected %v", ok, tt.expectValid)
			}
			if ok && got != tt.expected {
				t.Errorf("CalculateAdaptiveGOGC() = %d, expected %d", got, tt.expected)
			}
		})
	}
}

func TestResolveTuningPlan_Profiles(t *testing.T) {
	const (
		mem1GB   = int64(1024 * 1024 * 1024)
		liveHeap = int64(600 * 1024 * 1024)
	)

	limits := Limits{
		CgroupVersion:    VersionV2,
		MemoryLimitBytes: mem1GB,
		CPUQuota:         4.0,
		CPUs:             4,
	}

	t.Run("LatencyCritical", func(t *testing.T) {
		plan := ResolveTuningPlanWithProfile(limits, "", DefaultMemoryRatio, DefaultMinHeadroomBytes, GCProfileLatencyCritical, 0)
		if !plan.GOGCApplied || plan.GOGC != DefaultLatencyCriticalGOGC || plan.GOGCStr != "75" {
			t.Errorf("expected GOGC=75 applied, got %d (%s, applied=%t)", plan.GOGC, plan.GOGCStr, plan.GOGCApplied)
		}
		if plan.AppliedRatio != DefaultMemoryRatio {
			t.Errorf("expected ratio %f, got %f", DefaultMemoryRatio, plan.AppliedRatio)
		}
	})

	t.Run("MemoryConstrained", func(t *testing.T) {
		plan := ResolveTuningPlanWithProfile(limits, "", DefaultMemoryRatio, DefaultMinHeadroomBytes, GCProfileMemoryConstrained, 0)
		if !plan.GOGCApplied || plan.GOGC != DefaultMemoryConstrainedGOGC || plan.GOGCStr != "40" {
			t.Errorf("expected GOGC=40 applied, got %d (%s, applied=%t)", plan.GOGC, plan.GOGCStr, plan.GOGCApplied)
		}
		if plan.AppliedRatio != DefaultMemoryConstrainedRatio {
			t.Errorf("expected ratio %f, got %f", DefaultMemoryConstrainedRatio, plan.AppliedRatio)
		}
	})

	t.Run("MemoryConstrainedWithExplicitRatioOverride", func(t *testing.T) {
		plan := ResolveTuningPlanWithProfile(limits, "0.75", DefaultMemoryRatio, DefaultMinHeadroomBytes, GCProfileMemoryConstrained, 0)
		if plan.AppliedRatio != 0.75 {
			t.Errorf("expected explicit ratio 0.75, got %f", plan.AppliedRatio)
		}
		if !plan.GOGCApplied || plan.GOGC != DefaultMemoryConstrainedGOGC {
			t.Errorf("expected GOGC=40 applied, got %d", plan.GOGC)
		}
	})

	t.Run("BatchETL", func(t *testing.T) {
		plan := ResolveTuningPlanWithProfile(limits, "", DefaultMemoryRatio, DefaultMinHeadroomBytes, GCProfileBatchETL, 0)
		if !plan.GOGCApplied || plan.GOGC != DefaultBatchETLGOGC || plan.GOGCStr != "off" {
			t.Errorf("expected GOGC=-1 (off) applied, got %d (%s, applied=%t)", plan.GOGC, plan.GOGCStr, plan.GOGCApplied)
		}
	})

	t.Run("AdaptiveWithLiveHeap", func(t *testing.T) {
		plan := ResolveTuningPlanWithProfile(limits, "", DefaultMemoryRatio, DefaultMinHeadroomBytes, GCProfileAdaptive, liveHeap)
		if !plan.GOGCApplied || plan.GOGC <= 0 || plan.GOGCStr == "" {
			t.Errorf("expected adaptive GOGC applied, got %d (%s, applied=%t)", plan.GOGC, plan.GOGCStr, plan.GOGCApplied)
		}
	})

	t.Run("AdaptiveMissingLiveHeapSkipsGOGC", func(t *testing.T) {
		plan := ResolveTuningPlanWithProfile(limits, "", DefaultMemoryRatio, DefaultMinHeadroomBytes, GCProfileAdaptive, 0)
		if plan.GOGCApplied || plan.GOGCStr != "" {
			t.Errorf("expected adaptive GOGC skipped when live heap is 0, got %d (%s, applied=%t)",
				plan.GOGC, plan.GOGCStr, plan.GOGCApplied)
		}
	})

	t.Run("DefaultProfilePreservesDefaultGOGC", func(t *testing.T) {
		plan := ResolveTuningPlanWithProfile(limits, "", DefaultMemoryRatio, DefaultMinHeadroomBytes, GCProfileDefault, 0)
		if plan.GOGCApplied || plan.GOGCStr != "" {
			t.Errorf("expected default profile not to apply GOGC, got applied=%t", plan.GOGCApplied)
		}
	})
}

func TestReadLimitsCgroupV2Nested_LeafLimit(t *testing.T) {
	tempDir := t.TempDir()
	cgroupRoot := filepath.Join(tempDir, "sys_fs_cgroup")
	procFile := filepath.Join(tempDir, "proc_cgroup")

	leafDir := filepath.Join(cgroupRoot, "user.slice", "user-1000.slice", "app.slice")
	if err := os.MkdirAll(leafDir, 0o755); err != nil {
		t.Fatalf("mkdir leafDir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(cgroupRoot, "memory.max"), []byte("max\n"), 0o600); err != nil {
		t.Fatalf("writing root memory.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cgroupRoot, "cpu.max"), []byte("max 100000\n"), 0o600); err != nil {
		t.Fatalf("writing root cpu.max: %v", err)
	}

	if err := os.WriteFile(filepath.Join(leafDir, "memory.max"), []byte("1073741824\n"), 0o600); err != nil {
		t.Fatalf("writing leaf memory.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(leafDir, "cpu.max"), []byte("200000 100000\n"), 0o600); err != nil {
		t.Fatalf("writing leaf cpu.max: %v", err)
	}

	procContent := "0::/user.slice/user-1000.slice/app.slice\n"
	if err := os.WriteFile(procFile, []byte(procContent), 0o600); err != nil {
		t.Fatalf("writing procFile: %v", err)
	}

	limits, err := ReadLimitsCustom(cgroupRoot, procFile)
	if err != nil {
		t.Fatalf("ReadLimitsCustom failed: %v", err)
	}

	if limits.CgroupVersion != VersionV2 {
		t.Errorf("expected VersionV2, got %d", limits.CgroupVersion)
	}
	if limits.MemoryLimitBytes != testBytes1GB {
		t.Errorf("expected MemoryLimitBytes %d, got %d", testBytes1GB, limits.MemoryLimitBytes)
	}
	const expectedQuota = 2.0
	if limits.CPUQuota != expectedQuota {
		t.Errorf("expected CPUQuota %f, got %f", expectedQuota, limits.CPUQuota)
	}
	if limits.CPUs != 2 {
		t.Errorf("expected CPUs 2, got %d", limits.CPUs)
	}
}

func TestReadLimitsCgroupV2Nested_InheritedAncestorLimit(t *testing.T) {
	tempDir := t.TempDir()
	cgroupRoot := filepath.Join(tempDir, "sys_fs_cgroup")
	procFile := filepath.Join(tempDir, "proc_cgroup")

	parentDir := filepath.Join(cgroupRoot, "user.slice", "user-1000.slice")
	leafDir := filepath.Join(parentDir, "app.slice")
	if err := os.MkdirAll(leafDir, 0o755); err != nil {
		t.Fatalf("mkdir leafDir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(cgroupRoot, "memory.max"), []byte("max\n"), 0o600); err != nil {
		t.Fatalf("writing root memory.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cgroupRoot, "cpu.max"), []byte("max 100000\n"), 0o600); err != nil {
		t.Fatalf("writing root cpu.max: %v", err)
	}

	if err := os.WriteFile(filepath.Join(parentDir, "memory.max"), []byte("2147483648\n"), 0o600); err != nil {
		t.Fatalf("writing parent memory.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, "cpu.max"), []byte("400000 100000\n"), 0o600); err != nil {
		t.Fatalf("writing parent cpu.max: %v", err)
	}

	if err := os.WriteFile(filepath.Join(leafDir, "memory.max"), []byte("max\n"), 0o600); err != nil {
		t.Fatalf("writing leaf memory.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(leafDir, "cpu.max"), []byte("max 100000\n"), 0o600); err != nil {
		t.Fatalf("writing leaf cpu.max: %v", err)
	}

	procContent := "0::/user.slice/user-1000.slice/app.slice\n"
	if err := os.WriteFile(procFile, []byte(procContent), 0o600); err != nil {
		t.Fatalf("writing procFile: %v", err)
	}

	limits, err := ReadLimitsCustom(cgroupRoot, procFile)
	if err != nil {
		t.Fatalf("ReadLimitsCustom failed: %v", err)
	}

	if limits.MemoryLimitBytes != testBytes2GB {
		t.Errorf("expected MemoryLimitBytes %d inherited from parent, got %d", testBytes2GB, limits.MemoryLimitBytes)
	}
	const expectedQuota = 4.0
	if limits.CPUQuota != expectedQuota {
		t.Errorf("expected CPUQuota %f, got %f", expectedQuota, limits.CPUQuota)
	}
	if limits.CPUs != 4 {
		t.Errorf("expected CPUs 4, got %d", limits.CPUs)
	}
}

func TestReadLimitsCgroupV2Nested_MultiLevelStrictestLimit(t *testing.T) {
	tempDir := t.TempDir()
	cgroupRoot := filepath.Join(tempDir, "sys_fs_cgroup")
	procFile := filepath.Join(tempDir, "proc_cgroup")

	userSlice := filepath.Join(cgroupRoot, "user.slice")
	user1000Slice := filepath.Join(userSlice, "user-1000.slice")
	leafDir := filepath.Join(user1000Slice, "app.slice")
	if err := os.MkdirAll(leafDir, 0o755); err != nil {
		t.Fatalf("mkdir leafDir: %v", err)
	}

	const bytes4GB = int64(4 * 1024 * 1024 * 1024)
	if err := os.WriteFile(filepath.Join(cgroupRoot, "memory.max"), []byte("4294967296\n"), 0o600); err != nil {
		t.Fatalf("writing root memory.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cgroupRoot, "cpu.max"), []byte("800000 100000\n"), 0o600); err != nil {
		t.Fatalf("writing root cpu.max: %v", err)
	}

	if err := os.WriteFile(filepath.Join(userSlice, "memory.max"), []byte("2147483648\n"), 0o600); err != nil {
		t.Fatalf("writing user.slice memory.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userSlice, "cpu.max"), []byte("400000 100000\n"), 0o600); err != nil {
		t.Fatalf("writing user.slice cpu.max: %v", err)
	}

	if err := os.WriteFile(filepath.Join(user1000Slice, "memory.max"), []byte("1073741824\n"), 0o600); err != nil {
		t.Fatalf("writing user-1000.slice memory.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(user1000Slice, "cpu.max"), []byte("200000 100000\n"), 0o600); err != nil {
		t.Fatalf("writing user-1000.slice cpu.max: %v", err)
	}

	if err := os.WriteFile(filepath.Join(leafDir, "memory.max"), []byte("max\n"), 0o600); err != nil {
		t.Fatalf("writing leaf memory.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(leafDir, "cpu.max"), []byte("max 100000\n"), 0o600); err != nil {
		t.Fatalf("writing leaf cpu.max: %v", err)
	}

	procContent := "0::/user.slice/user-1000.slice/app.slice\n"
	if err := os.WriteFile(procFile, []byte(procContent), 0o600); err != nil {
		t.Fatalf("writing procFile: %v", err)
	}

	limits, err := ReadLimitsCustom(cgroupRoot, procFile)
	if err != nil {
		t.Fatalf("ReadLimitsCustom failed: %v", err)
	}

	if limits.MemoryLimitBytes != testBytes1GB {
		t.Errorf("expected strictest MemoryLimitBytes %d, got %d", testBytes1GB, limits.MemoryLimitBytes)
	}
	const expectedQuota = 2.0
	if limits.CPUQuota != expectedQuota {
		t.Errorf("expected strictest CPUQuota %f, got %f", expectedQuota, limits.CPUQuota)
	}
	if limits.CPUs != 2 {
		t.Errorf("expected CPUs 2, got %d", limits.CPUs)
	}
	_ = bytes4GB
}

func TestReadLimitsCgroupV2Nested_RootPath(t *testing.T) {
	tempDir := t.TempDir()
	cgroupRoot := filepath.Join(tempDir, "sys_fs_cgroup")
	procFile := filepath.Join(tempDir, "proc_cgroup")
	if err := os.MkdirAll(cgroupRoot, 0o755); err != nil {
		t.Fatalf("mkdir cgroupRoot: %v", err)
	}

	const bytes512MB = int64(512 * 1024 * 1024)
	if err := os.WriteFile(filepath.Join(cgroupRoot, "memory.max"), []byte("536870912\n"), 0o600); err != nil {
		t.Fatalf("writing root memory.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cgroupRoot, "cpu.max"), []byte("150000 100000\n"), 0o600); err != nil {
		t.Fatalf("writing root cpu.max: %v", err)
	}

	if err := os.WriteFile(procFile, []byte("0::/\n"), 0o600); err != nil {
		t.Fatalf("writing procFile: %v", err)
	}

	limits, err := ReadLimitsCustom(cgroupRoot, procFile)
	if err != nil {
		t.Fatalf("ReadLimitsCustom failed: %v", err)
	}

	if limits.MemoryLimitBytes != bytes512MB {
		t.Errorf("expected MemoryLimitBytes %d, got %d", bytes512MB, limits.MemoryLimitBytes)
	}
	const expectedQuota = 1.5
	if limits.CPUQuota != expectedQuota {
		t.Errorf("expected CPUQuota %f, got %f", expectedQuota, limits.CPUQuota)
	}
	if limits.CPUs != 1 {
		t.Errorf("expected CPUs 1, got %d", limits.CPUs)
	}
}

func TestReadLimitsCgroupV1Nested_MemoryAndCpu(t *testing.T) {
	tempDir := t.TempDir()
	cgroupRoot := filepath.Join(tempDir, "sys_fs_cgroup")
	procFile := filepath.Join(tempDir, "proc_cgroup")

	memLeaf := filepath.Join(cgroupRoot, "memory", "docker", "container123")
	cpuLeaf := filepath.Join(cgroupRoot, "cpu", "docker", "container123")
	if err := os.MkdirAll(memLeaf, 0o755); err != nil {
		t.Fatalf("mkdir memLeaf: %v", err)
	}
	if err := os.MkdirAll(cpuLeaf, 0o755); err != nil {
		t.Fatalf("mkdir cpuLeaf: %v", err)
	}

	if err := os.WriteFile(filepath.Join(cgroupRoot, "memory", "memory.limit_in_bytes"), []byte("4294967296\n"), 0o600); err != nil {
		t.Fatalf("writing root mem limit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cgroupRoot, "cpu", "cpu.cfs_quota_us"), []byte("-1\n"), 0o600); err != nil {
		t.Fatalf("writing root cpu quota: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cgroupRoot, "cpu", "cpu.cfs_period_us"), []byte("100000\n"), 0o600); err != nil {
		t.Fatalf("writing root cpu period: %v", err)
	}

	if err := os.WriteFile(filepath.Join(memLeaf, "memory.limit_in_bytes"), []byte("1073741824\n"), 0o600); err != nil {
		t.Fatalf("writing leaf mem limit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cpuLeaf, "cpu.cfs_quota_us"), []byte("300000\n"), 0o600); err != nil {
		t.Fatalf("writing leaf cpu quota: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cpuLeaf, "cpu.cfs_period_us"), []byte("100000\n"), 0o600); err != nil {
		t.Fatalf("writing leaf cpu period: %v", err)
	}

	procContent := "5:memory:/docker/container123\n4:cpu,cpuacct:/docker/container123\n"
	if err := os.WriteFile(procFile, []byte(procContent), 0o600); err != nil {
		t.Fatalf("writing procFile: %v", err)
	}

	limits, err := ReadLimitsCustom(cgroupRoot, procFile)
	if err != nil {
		t.Fatalf("ReadLimitsCustom failed: %v", err)
	}

	if limits.CgroupVersion != VersionV1 {
		t.Errorf("expected VersionV1, got %d", limits.CgroupVersion)
	}
	if limits.MemoryLimitBytes != testBytes1GB {
		t.Errorf("expected MemoryLimitBytes %d, got %d", testBytes1GB, limits.MemoryLimitBytes)
	}
	const expectedQuota = 3.0
	if limits.CPUQuota != expectedQuota {
		t.Errorf("expected CPUQuota %f, got %f", expectedQuota, limits.CPUQuota)
	}
	if limits.CPUs != 3 {
		t.Errorf("expected CPUs 3, got %d", limits.CPUs)
	}
}

func TestReadLimitsCgroupV1Nested_AliasedCpuController(t *testing.T) {
	tempDir := t.TempDir()
	cgroupRoot := filepath.Join(tempDir, "sys_fs_cgroup")
	procFile := filepath.Join(tempDir, "proc_cgroup")

	cpuAcctLeaf := filepath.Join(cgroupRoot, "cpu,cpuacct", "system.slice", "service.scope")
	memDir := filepath.Join(cgroupRoot, "memory")
	if err := os.MkdirAll(cpuAcctLeaf, 0o755); err != nil {
		t.Fatalf("mkdir cpuAcctLeaf: %v", err)
	}
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("mkdir memDir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(memDir, "memory.limit_in_bytes"), []byte("2147483648\n"), 0o600); err != nil {
		t.Fatalf("writing mem limit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cpuAcctLeaf, "cpu.cfs_quota_us"), []byte("200000\n"), 0o600); err != nil {
		t.Fatalf("writing leaf cpu quota: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cpuAcctLeaf, "cpu.cfs_period_us"), []byte("100000\n"), 0o600); err != nil {
		t.Fatalf("writing leaf cpu period: %v", err)
	}

	procContent := "2:cpu,cpuacct:/system.slice/service.scope\n3:memory:/\n"
	if err := os.WriteFile(procFile, []byte(procContent), 0o600); err != nil {
		t.Fatalf("writing procFile: %v", err)
	}

	limits, err := ReadLimitsCustom(cgroupRoot, procFile)
	if err != nil {
		t.Fatalf("ReadLimitsCustom failed: %v", err)
	}

	if limits.CgroupVersion != VersionV1 {
		t.Errorf("expected VersionV1, got %d", limits.CgroupVersion)
	}
	if limits.MemoryLimitBytes != testBytes2GB {
		t.Errorf("expected MemoryLimitBytes %d, got %d", testBytes2GB, limits.MemoryLimitBytes)
	}
	const expectedQuota = 2.0
	if limits.CPUQuota != expectedQuota {
		t.Errorf("expected CPUQuota %f, got %f", expectedQuota, limits.CPUQuota)
	}
	if limits.CPUs != 2 {
		t.Errorf("expected CPUs 2, got %d", limits.CPUs)
	}
}

func TestReadLimitsProcCgroup_EdgeCases(t *testing.T) {
	tempDir := t.TempDir()
	cgroupRoot := filepath.Join(tempDir, "sys_fs_cgroup")
	if err := os.MkdirAll(cgroupRoot, 0o755); err != nil {
		t.Fatalf("mkdir cgroupRoot: %v", err)
	}

	if err := os.WriteFile(filepath.Join(cgroupRoot, "memory.max"), []byte("1073741824\n"), 0o600); err != nil {
		t.Fatalf("writing root memory.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cgroupRoot, "cpu.max"), []byte("200000 100000\n"), 0o600); err != nil {
		t.Fatalf("writing root cpu.max: %v", err)
	}

	t.Run("MissingProcFileReturnsVersionUnknown", func(t *testing.T) {
		limits, err := ReadLimitsCustom(cgroupRoot, filepath.Join(tempDir, "non_existent_proc"))
		if err == nil {
			t.Fatalf("expected error on missing proc file")
		}
		if limits.CgroupVersion != VersionUnknown {
			t.Errorf("expected VersionUnknown on missing proc, got %d", limits.CgroupVersion)
		}
		if !errors.Is(err, ErrCgroupProcUnreadable) {
			t.Errorf("expected ErrCgroupProcUnreadable, got %v", err)
		}
	})

	t.Run("EmptyProcFilePathDefaultsToProcSelfCgroup", func(t *testing.T) {
		// Calling with default sysfs mount and empty proc path invokes host /proc/self/cgroup
		_, _ = ReadLimitsCustom(defaultCgroupMount, "")
	})

	t.Run("MalformedLinesAndCommentsWithValidEntry", func(t *testing.T) {
		corruptProc := filepath.Join(t.TempDir(), "corrupt_proc")
		corruptContent := "# Comment header\n\nmalformed_line_no_colons\n1:only_two_parts\n:::\n0::/\n"
		if err := os.WriteFile(corruptProc, []byte(corruptContent), 0o600); err != nil {
			t.Fatalf("writing corruptProc: %v", err)
		}

		limits, err := ReadLimitsCustom(cgroupRoot, corruptProc)
		if err != nil {
			t.Fatalf("ReadLimitsCustom failed on corrupt proc with valid entry: %v", err)
		}
		if limits.MemoryLimitBytes != testBytes1GB {
			t.Errorf("expected root memory %d, got %d", testBytes1GB, limits.MemoryLimitBytes)
		}
	})

	t.Run("ProcFileWithNoValidEntriesFails", func(t *testing.T) {
		invalidProc := filepath.Join(t.TempDir(), "invalid_proc")
		invalidContent := "# Only comments\n\nmalformed_line_no_colons\n1:only_two_parts\n"
		if err := os.WriteFile(invalidProc, []byte(invalidContent), 0o600); err != nil {
			t.Fatalf("writing invalidProc: %v", err)
		}

		limits, err := ReadLimitsCustom(cgroupRoot, invalidProc)
		if err == nil {
			t.Fatalf("expected error on proc with no valid entries")
		}
		if limits.CgroupVersion != VersionUnknown {
			t.Errorf("expected VersionUnknown, got %d", limits.CgroupVersion)
		}
		if !errors.Is(err, ErrCgroupProcUnreadable) {
			t.Errorf("expected ErrCgroupProcUnreadable, got %v", err)
		}
	})

	t.Run("PathEscapeSanitization", func(t *testing.T) {
		escapeProc := filepath.Join(t.TempDir(), "escape_proc")
		escapeContent := "0::../../../../../../etc\n"
		if err := os.WriteFile(escapeProc, []byte(escapeContent), 0o600); err != nil {
			t.Fatalf("writing escapeProc: %v", err)
		}

		limits, err := ReadLimitsCustom(cgroupRoot, escapeProc)
		if err == nil {
			t.Fatalf("expected error on path escape attempt")
		}
		if limits.CgroupVersion != VersionUnknown {
			t.Errorf("expected VersionUnknown on escape attempt, got %d", limits.CgroupVersion)
		}
		if !errors.Is(err, ErrCgroupHierarchyNotFound) {
			t.Errorf("expected ErrCgroupHierarchyNotFound, got %v", err)
		}
	})

	t.Run("NonExistentHierarchySubpathFails", func(t *testing.T) {
		missingSubpathProc := filepath.Join(t.TempDir(), "missing_subpath_proc")
		procContent := "0::/docker/non_existent_container_id\n"
		if err := os.WriteFile(missingSubpathProc, []byte(procContent), 0o600); err != nil {
			t.Fatalf("writing missingSubpathProc: %v", err)
		}

		limits, err := ReadLimitsCustom(cgroupRoot, missingSubpathProc)
		if err == nil {
			t.Fatalf("expected error on missing cgroup subpath")
		}
		if limits.CgroupVersion != VersionUnknown {
			t.Errorf("expected VersionUnknown on missing hierarchy, got %d", limits.CgroupVersion)
		}
		if !errors.Is(err, ErrCgroupHierarchyNotFound) {
			t.Errorf("expected ErrCgroupHierarchyNotFound, got %v", err)
		}
	})
}

func TestIsSubpathHelper(t *testing.T) {
	tests := []struct {
		root string
		path string
		want bool
	}{
		{defaultCgroupMount, defaultCgroupMount, true},
		{defaultCgroupMount, defaultCgroupMount + "/user.slice", true},
		{defaultCgroupMount, defaultCgroupMount + "/user.slice/app.slice", true},
		{defaultCgroupMount, defaultCgroupMount + "_other", false},
		{defaultCgroupMount, "/sys/fs", false},
		{"/", "/user.slice", true},
		{"/", "/", true},
	}

	for _, tt := range tests {
		t.Run(tt.root+"_"+tt.path, func(t *testing.T) {
			got := isSubpath(tt.root, tt.path)
			if got != tt.want {
				t.Errorf("isSubpath(%q, %q) = %v, want %v", tt.root, tt.path, got, tt.want)
			}
		})
	}
}

func TestCgroupV1FlatLayoutAndEdgeCases(t *testing.T) {
	t.Run("FlatV1MountAtRoot", func(t *testing.T) {
		tempDir := t.TempDir()
		procFile := filepath.Join(tempDir, "proc_cgroup")
		if err := os.WriteFile(procFile, []byte("1:memory:/\n2:cpu:/\n"), 0o600); err != nil {
			t.Fatalf("writing procFile: %v", err)
		}

		// Write limit files directly at root of tempDir (flat v1 mount)
		if err := os.WriteFile(filepath.Join(tempDir, "memory.limit_in_bytes"), []byte("1073741824\n"), 0o600); err != nil {
			t.Fatalf("writing memory.limit_in_bytes: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tempDir, "cpu.cfs_quota_us"), []byte("200000\n"), 0o600); err != nil {
			t.Fatalf("writing cpu.cfs_quota_us: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tempDir, "cpu.cfs_period_us"), []byte("100000\n"), 0o600); err != nil {
			t.Fatalf("writing cpu.cfs_period_us: %v", err)
		}

		limits, err := ReadLimitsCustom(tempDir, procFile)
		if err != nil {
			t.Fatalf("ReadLimitsCustom failed on flat v1 layout: %v", err)
		}
		if limits.CgroupVersion != VersionV1 {
			t.Errorf("expected VersionV1, got %d", limits.CgroupVersion)
		}
		if limits.MemoryLimitBytes != testBytes1GB {
			t.Errorf("expected MemoryLimitBytes %d, got %d", testBytes1GB, limits.MemoryLimitBytes)
		}
		if limits.CPUQuota != 2.0 || limits.CPUs != 2 {
			t.Errorf("expected CPUQuota=2.0, CPUs=2, got quota=%f, cpus=%d", limits.CPUQuota, limits.CPUs)
		}
	})

	t.Run("V1UnlimitedQuotaAndMemory", func(t *testing.T) {
		tempDir := t.TempDir()
		procFile := filepath.Join(tempDir, "proc_cgroup")
		if err := os.WriteFile(procFile, []byte("1:memory:/\n2:cpu:/\n"), 0o600); err != nil {
			t.Fatalf("writing procFile: %v", err)
		}

		memDir := filepath.Join(tempDir, "memory")
		cpuDir := filepath.Join(tempDir, "cpu")
		_ = os.MkdirAll(memDir, 0o755)
		_ = os.MkdirAll(cpuDir, 0o755)

		_ = os.WriteFile(filepath.Join(memDir, "memory.limit_in_bytes"), []byte("-1\n"), 0o600)
		_ = os.WriteFile(filepath.Join(cpuDir, "cpu.cfs_quota_us"), []byte("-1\n"), 0o600)
		_ = os.WriteFile(filepath.Join(cpuDir, "cpu.cfs_period_us"), []byte("100000\n"), 0o600)

		limits, err := ReadLimitsCustom(tempDir, procFile)
		if err != nil {
			t.Fatalf("ReadLimitsCustom failed on v1 unlimited: %v", err)
		}
		if limits.MemoryLimitBytes != 0 {
			t.Errorf("expected MemoryLimitBytes 0 on -1, got %d", limits.MemoryLimitBytes)
		}
		if limits.CPUQuota != 0 || limits.CPUs != 0 {
			t.Errorf("expected CPUQuota 0 and CPUs 0 on -1 quota, got %f, %d", limits.CPUQuota, limits.CPUs)
		}
	})

	t.Run("V1InvalidPeriodOnUnlimitedQuota", func(t *testing.T) {
		tempDir := t.TempDir()
		procFile := filepath.Join(tempDir, "proc_cgroup")
		_ = os.WriteFile(procFile, []byte("1:memory:/\n2:cpu:/\n"), 0o600)

		memDir := filepath.Join(tempDir, "memory")
		cpuDir := filepath.Join(tempDir, "cpu")
		_ = os.MkdirAll(memDir, 0o755)
		_ = os.MkdirAll(cpuDir, 0o755)

		_ = os.WriteFile(filepath.Join(memDir, "memory.limit_in_bytes"), []byte("1073741824\n"), 0o600)
		_ = os.WriteFile(filepath.Join(cpuDir, "cpu.cfs_quota_us"), []byte("-1\n"), 0o600)
		_ = os.WriteFile(filepath.Join(cpuDir, "cpu.cfs_period_us"), []byte("0\n"), 0o600)

		limits, err := ReadLimitsCustom(tempDir, procFile)
		if err == nil || limits.CgroupVersion != VersionUnknown || !errors.Is(err, ErrCgroupLimitCorrupted) {
			t.Fatalf("expected ErrCgroupLimitCorrupted on zero period for v1, got limits=%+v, err=%v", limits, err)
		}
	})
}

func TestRootCgroupVsUnresolvedControllers(t *testing.T) {
	t.Parallel()

	t.Run("CgroupV2_ExplicitRoot", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		procFile := filepath.Join(tempDir, "proc_cgroup")
		requireNoError(t, os.WriteFile(procFile, []byte("0::/\n"), 0o600))
		requireNoError(t, os.WriteFile(filepath.Join(tempDir, "memory.max"), []byte("max\n"), 0o600))

		limits, err := ReadLimitsCustom(tempDir, procFile)
		requireNoError(t, err)
		if limits.CgroupVersion != VersionV2 {
			t.Errorf("expected VersionV2, got %d", limits.CgroupVersion)
		}
		if limits.MemoryLimitBytes != 0 {
			t.Errorf("expected 0 for max memory limit, got %d", limits.MemoryLimitBytes)
		}
	})

	t.Run("CgroupV2_MissingV2EntryInProcfs", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		procFile := filepath.Join(tempDir, "proc_cgroup")
		// Procfs has v1 entries only, no 0:: line
		requireNoError(t, os.WriteFile(procFile, []byte("1:net_cls:/\n"), 0o600))
		requireNoError(t, os.WriteFile(filepath.Join(tempDir, "memory.max"), []byte("1073741824\n"), 0o600))

		limits, err := ReadLimitsCustom(tempDir, procFile)
		requireNoError(t, err)
		if limits.CgroupVersion != VersionUnknown {
			t.Errorf("expected VersionUnknown when 0:: entry is missing, got %d", limits.CgroupVersion)
		}
		if limits.MemoryLimitBytes != 0 {
			t.Errorf("expected 0 memory limit when v2 entry is missing, got %d", limits.MemoryLimitBytes)
		}
	})

	t.Run("ResolveTargetDirectory_EmptyPathRejected", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		_, err := resolveTargetDirectory(tempDir, "")
		if !errors.Is(err, ErrCgroupHierarchyNotFound) {
			t.Errorf("expected ErrCgroupHierarchyNotFound on empty relPath, got %v", err)
		}

		resolved, err := resolveTargetDirectory(tempDir, "/")
		requireNoError(t, err)
		if resolved != tempDir {
			t.Errorf("expected %s on root relPath, got %s", tempDir, resolved)
		}
	})

	t.Run("CgroupV1_MissingMemoryController", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		procFile := filepath.Join(tempDir, "proc_cgroup")
		// Only CPU controller in procfs
		requireNoError(t, os.WriteFile(procFile, []byte("2:cpu:/\n"), 0o600))

		memDir := filepath.Join(tempDir, "memory")
		cpuDir := filepath.Join(tempDir, "cpu")
		requireNoError(t, os.MkdirAll(memDir, 0o755))
		requireNoError(t, os.MkdirAll(cpuDir, 0o755))
		requireNoError(t, os.WriteFile(filepath.Join(memDir, "memory.limit_in_bytes"), []byte("1073741824\n"), 0o600))
		requireNoError(t, os.WriteFile(filepath.Join(cpuDir, "cpu.cfs_quota_us"), []byte("200000\n"), 0o600))
		requireNoError(t, os.WriteFile(filepath.Join(cpuDir, "cpu.cfs_period_us"), []byte("100000\n"), 0o600))

		limits, err := ReadLimitsCustom(tempDir, procFile)
		requireNoError(t, err)
		if limits.CgroupVersion != VersionV1 {
			t.Errorf("expected VersionV1, got %d", limits.CgroupVersion)
		}
		// Memory must NOT be resolved to root because memory entry is absent in procfs
		if limits.MemoryLimitBytes != 0 {
			t.Errorf("expected 0 memory limit when memory controller is absent, got %d", limits.MemoryLimitBytes)
		}
		if limits.CPUs != 2 {
			t.Errorf("expected 2 CPUs, got %d", limits.CPUs)
		}
	})

	t.Run("CgroupV1_MissingCPUController", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		procFile := filepath.Join(tempDir, "proc_cgroup")
		// Only memory controller in procfs
		requireNoError(t, os.WriteFile(procFile, []byte("1:memory:/\n"), 0o600))

		memDir := filepath.Join(tempDir, "memory")
		cpuDir := filepath.Join(tempDir, "cpu")
		requireNoError(t, os.MkdirAll(memDir, 0o755))
		requireNoError(t, os.MkdirAll(cpuDir, 0o755))
		requireNoError(t, os.WriteFile(filepath.Join(memDir, "memory.limit_in_bytes"), []byte("1073741824\n"), 0o600))
		requireNoError(t, os.WriteFile(filepath.Join(cpuDir, "cpu.cfs_quota_us"), []byte("200000\n"), 0o600))
		requireNoError(t, os.WriteFile(filepath.Join(cpuDir, "cpu.cfs_period_us"), []byte("100000\n"), 0o600))

		limits, err := ReadLimitsCustom(tempDir, procFile)
		requireNoError(t, err)
		if limits.CgroupVersion != VersionV1 {
			t.Errorf("expected VersionV1, got %d", limits.CgroupVersion)
		}
		if limits.MemoryLimitBytes != 1073741824 {
			t.Errorf("expected 1GB memory limit, got %d", limits.MemoryLimitBytes)
		}
		// CPU must NOT be resolved to root because cpu entry is absent in procfs
		if limits.CPUQuota != 0 || limits.CPUs != 0 {
			t.Errorf("expected 0 CPUs when cpu controller is absent, got quota=%f, cpus=%d", limits.CPUQuota, limits.CPUs)
		}
	})

	t.Run("CgroupV1_MissingAllControllers", func(t *testing.T) {
		t.Parallel()
		tempDir := t.TempDir()
		procFile := filepath.Join(tempDir, "proc_cgroup")
		// Only net_cls controller
		requireNoError(t, os.WriteFile(procFile, []byte("5:net_cls:/\n"), 0o600))

		memDir := filepath.Join(tempDir, "memory")
		requireNoError(t, os.MkdirAll(memDir, 0o755))
		requireNoError(t, os.WriteFile(filepath.Join(memDir, "memory.limit_in_bytes"), []byte("1073741824\n"), 0o600))

		limits, err := ReadLimitsCustom(tempDir, procFile)
		requireNoError(t, err)
		if limits.CgroupVersion != VersionUnknown {
			t.Errorf("expected VersionUnknown when neither memory nor cpu is in procfs, got %d", limits.CgroupVersion)
		}
	})
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

