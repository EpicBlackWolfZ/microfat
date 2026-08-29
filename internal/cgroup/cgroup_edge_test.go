package cgroup

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCalculateGOMAXPROCSEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		quota    float64
		wantCPUs int
		wantOK   bool
	}{
		{"ZeroQuota", 0.0, 0, false},
		{"NegativeQuota", -2.5, 0, false},
		{"MicroContainerFractional0_1", 0.1, 1, true},
		{"MicroContainerFractional0_5", 0.5, 1, true},
		{"MicroContainerFractional0_99", 0.99, 1, true},
		{"ExactSingleCore", 1.0, 1, true},
		{"SlightlyAboveSingleCore1_01", 1.01, 1, true},
		{"OneAndHalfCores1_5", 1.5, 1, true},
		{"AlmostTwoCores1_99", 1.99, 1, true},
		{"ExactTwoCores", 2.0, 2, true},
		{"Fractional3_99", 3.99, 3, true},
		{"HighCore64_75", 64.75, 64, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotCPUs, gotOK := CalculateGOMAXPROCS(tt.quota)
			if gotOK != tt.wantOK {
				t.Fatalf("CalculateGOMAXPROCS(%v) ok = %v, want %v", tt.quota, gotOK, tt.wantOK)
			}
			if gotCPUs != tt.wantCPUs {
				t.Fatalf("CalculateGOMAXPROCS(%v) cpus = %v, want %v", tt.quota, gotCPUs, tt.wantCPUs)
			}
		})
	}
}

func TestCalculateGOMEMLIMITEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		limitBytes  int64
		ratio       float64
		headroom    int64
		wantLimitGT int64
		wantOK      bool
	}{
		{"ZeroLimit", 0, 0.90, 64 * 1024 * 1024, 0, false},
		{"NegativeLimit", -1024, 0.90, 64 * 1024 * 1024, 0, false},
		{"PetabyteLimitUnlimited", UnlimitedCgroupV1MemoryThreshold + 100, 0.90, 64 * 1024 * 1024, 0, false},
		{"TinyContainer10MB", 10 * 1024 * 1024, 0.90, 64 * 1024 * 1024, 0, true},
		{"SmallContainer64MB", 64 * 1024 * 1024, 0.90, 64 * 1024 * 1024, 0, true},
		{"Standard512MB", 512 * 1024 * 1024, 0.90, 64 * 1024 * 1024, 400 * 1024 * 1024, true},
		{"CustomZeroRatioDefaultsTo90Percent", 512 * 1024 * 1024, 0.0, 64 * 1024 * 1024, 400 * 1024 * 1024, true},
		{"CustomZeroHeadroomDefaultsTo64MB", 512 * 1024 * 1024, 0.90, 0, 400 * 1024 * 1024, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotBytes, gotOK := CalculateGOMEMLIMIT(tt.limitBytes, tt.ratio, tt.headroom)
			if gotOK != tt.wantOK {
				t.Fatalf("CalculateGOMEMLIMIT(%v) ok = %v, want %v", tt.limitBytes, gotOK, tt.wantOK)
			}
			if tt.wantOK && gotBytes <= tt.wantLimitGT {
				t.Fatalf("CalculateGOMEMLIMIT(%v) bytes = %v, expected > %v", tt.limitBytes, gotBytes, tt.wantLimitGT)
			}
		})
	}
}

func TestCgroupV2MalformedFiles(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	procFile := filepath.Join(tempDir, "proc_cgroup")
	if err := os.WriteFile(procFile, []byte("0::/\n"), 0o600); err != nil {
		t.Fatalf("failed to write procFile: %v", err)
	}

	// 1. memory.max with whitespace and trailing text
	memPath := filepath.Join(tempDir, "memory.max")
	if err := os.WriteFile(memPath, []byte("   104857600   \n"), 0o600); err != nil {
		t.Fatalf("failed to write memory.max: %v", err)
	}

	// 2. cpu.max with single parameter (malformed)
	cpuPath := filepath.Join(tempDir, "cpu.max")
	if err := os.WriteFile(cpuPath, []byte("200000\n"), 0o600); err != nil {
		t.Fatalf("failed to write cpu.max: %v", err)
	}

	limits, err := ReadLimitsCustom(tempDir, procFile)
	if err == nil {
		t.Fatalf("expected error on malformed single-token cpu.max")
	}
	if limits.CgroupVersion != VersionUnknown {
		t.Fatalf("expected VersionUnknown on malformed cpu.max, got %d", limits.CgroupVersion)
	}
	if !errors.Is(err, ErrCgroupLimitCorrupted) {
		t.Fatalf("expected ErrCgroupLimitCorrupted, got %v", err)
	}
}

func TestCgroupV1MalformedFiles(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	procFile := filepath.Join(tempDir, "proc_cgroup")
	if err := os.WriteFile(procFile, []byte("1:memory:/\n2:cpu:/\n"), 0o600); err != nil {
		t.Fatalf("failed to write procFile: %v", err)
	}

	memDir := filepath.Join(tempDir, "memory")
	cpuDir := filepath.Join(tempDir, "cpu")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("failed to create memory dir: %v", err)
	}
	if err := os.MkdirAll(cpuDir, 0o755); err != nil {
		t.Fatalf("failed to create cpu dir: %v", err)
	}

	// 1. memory.limit_in_bytes
	if err := os.WriteFile(filepath.Join(memDir, "memory.limit_in_bytes"), []byte("209715200\n"), 0o600); err != nil {
		t.Fatalf("failed to write memory limit: %v", err)
	}

	// 2. zero period (divide by zero guard)
	if err := os.WriteFile(filepath.Join(cpuDir, "cpu.cfs_quota_us"), []byte("100000\n"), 0o600); err != nil {
		t.Fatalf("failed to write cfs_quota: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cpuDir, "cpu.cfs_period_us"), []byte("0\n"), 0o600); err != nil {
		t.Fatalf("failed to write cfs_period: %v", err)
	}

	limits, err := ReadLimitsCustom(tempDir, procFile)
	if err == nil {
		t.Fatalf("expected error on zero cpu.cfs_period_us")
	}
	if limits.CgroupVersion != VersionUnknown {
		t.Fatalf("expected VersionUnknown on corrupted period, got %d", limits.CgroupVersion)
	}
	if !errors.Is(err, ErrCgroupLimitCorrupted) {
		t.Fatalf("expected ErrCgroupLimitCorrupted, got %v", err)
	}
}

func TestCgroupCorruptedLimitValues(t *testing.T) {
	t.Parallel()

	t.Run("CgroupV2_NonNumericMemory", func(t *testing.T) {
		tempDir := t.TempDir()
		procFile := filepath.Join(tempDir, "proc_cgroup")
		_ = os.WriteFile(procFile, []byte("0::/\n"), 0o600)
		_ = os.WriteFile(filepath.Join(tempDir, "memory.max"), []byte("invalid_non_numeric\n"), 0o600)

		limits, err := ReadLimitsCustom(tempDir, procFile)
		if err == nil || limits.CgroupVersion != VersionUnknown || !errors.Is(err, ErrCgroupLimitCorrupted) {
			t.Fatalf("expected VersionUnknown + ErrCgroupLimitCorrupted, got limits=%+v, err=%v", limits, err)
		}
	})

	t.Run("CgroupV2_NegativeMemory", func(t *testing.T) {
		tempDir := t.TempDir()
		procFile := filepath.Join(tempDir, "proc_cgroup")
		_ = os.WriteFile(procFile, []byte("0::/\n"), 0o600)
		_ = os.WriteFile(filepath.Join(tempDir, "memory.max"), []byte("-104857600\n"), 0o600)

		limits, err := ReadLimitsCustom(tempDir, procFile)
		if err == nil || limits.CgroupVersion != VersionUnknown || !errors.Is(err, ErrCgroupLimitCorrupted) {
			t.Fatalf("expected VersionUnknown + ErrCgroupLimitCorrupted, got limits=%+v, err=%v", limits, err)
		}
	})

	t.Run("CgroupV2_ZeroPeriodCPU", func(t *testing.T) {
		tempDir := t.TempDir()
		procFile := filepath.Join(tempDir, "proc_cgroup")
		_ = os.WriteFile(procFile, []byte("0::/\n"), 0o600)
		_ = os.WriteFile(filepath.Join(tempDir, "memory.max"), []byte("max\n"), 0o600)
		_ = os.WriteFile(filepath.Join(tempDir, "cpu.max"), []byte("max 0\n"), 0o600)

		limits, err := ReadLimitsCustom(tempDir, procFile)
		if err == nil || limits.CgroupVersion != VersionUnknown || !errors.Is(err, ErrCgroupLimitCorrupted) {
			t.Fatalf("expected VersionUnknown + ErrCgroupLimitCorrupted, got limits=%+v, err=%v", limits, err)
		}
	})

	t.Run("CgroupV1_NonNumericMemory", func(t *testing.T) {
		tempDir := t.TempDir()
		procFile := filepath.Join(tempDir, "proc_cgroup")
		_ = os.WriteFile(procFile, []byte("1:memory:/\n"), 0o600)
		memDir := filepath.Join(tempDir, "memory")
		_ = os.MkdirAll(memDir, 0o755)
		_ = os.WriteFile(filepath.Join(memDir, "memory.limit_in_bytes"), []byte("corrupt\n"), 0o600)

		limits, err := ReadLimitsCustom(tempDir, procFile)
		if err == nil || limits.CgroupVersion != VersionUnknown || !errors.Is(err, ErrCgroupLimitCorrupted) {
			t.Fatalf("expected VersionUnknown + ErrCgroupLimitCorrupted, got limits=%+v, err=%v", limits, err)
		}
	})

	t.Run("CgroupV1_InvalidCPUQuota", func(t *testing.T) {
		tempDir := t.TempDir()
		procFile := filepath.Join(tempDir, "proc_cgroup")
		_ = os.WriteFile(procFile, []byte("1:memory:/\n2:cpu:/\n"), 0o600)
		memDir := filepath.Join(tempDir, "memory")
		cpuDir := filepath.Join(tempDir, "cpu")
		_ = os.MkdirAll(memDir, 0o755)
		_ = os.MkdirAll(cpuDir, 0o755)
		_ = os.WriteFile(filepath.Join(memDir, "memory.limit_in_bytes"), []byte("104857600\n"), 0o600)
		_ = os.WriteFile(filepath.Join(cpuDir, "cpu.cfs_quota_us"), []byte("not_a_number\n"), 0o600)
		_ = os.WriteFile(filepath.Join(cpuDir, "cpu.cfs_period_us"), []byte("100000\n"), 0o600)

		limits, err := ReadLimitsCustom(tempDir, procFile)
		if err == nil || limits.CgroupVersion != VersionUnknown || !errors.Is(err, ErrCgroupLimitCorrupted) {
			t.Fatalf("expected VersionUnknown + ErrCgroupLimitCorrupted, got limits=%+v, err=%v", limits, err)
		}
	})
}
