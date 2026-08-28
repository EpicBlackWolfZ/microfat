package cgroup

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzCalculateGOMEMLIMIT(f *testing.F) {
	f.Add(int64(512*1024*1024), 0.90, int64(64*1024*1024))
	f.Add(int64(10*1024*1024), 0.80, int64(64*1024*1024))
	f.Add(int64(1024*1024*1024*1024), 0.95, int64(100*1024*1024))
	f.Add(int64(0), 0.90, int64(64*1024*1024))
	f.Add(int64(-1), 0.90, int64(64*1024*1024))
	f.Add(UnlimitedCgroupV1MemoryThreshold+100, 0.90, int64(64*1024*1024))

	f.Fuzz(func(t *testing.T, limitBytes int64, ratio float64, minHeadroomBytes int64) {
		res, ok := CalculateGOMEMLIMIT(limitBytes, ratio, minHeadroomBytes)
		if ok {
			if res <= 0 {
				t.Fatalf("CalculateGOMEMLIMIT returned true with non-positive result: %d", res)
			}
			if res > limitBytes {
				t.Fatalf("CalculateGOMEMLIMIT returned result (%d) greater than container limit (%d)", res, limitBytes)
			}
		}
	})
}

func FuzzCalculateGOMAXPROCS(f *testing.F) {
	f.Add(0.1)
	f.Add(0.9)
	f.Add(1.0)
	f.Add(1.5)
	f.Add(4.0)
	f.Add(128.9)
	f.Add(0.0)
	f.Add(-5.0)

	f.Fuzz(func(t *testing.T, quota float64) {
		cpus, ok := CalculateGOMAXPROCS(quota)
		if ok {
			if cpus < MinimumCPUs {
				t.Fatalf("CalculateGOMAXPROCS returned %d < MinimumCPUs", cpus)
			}
		}
	})
}

func FuzzParseGCProfile(f *testing.F) {
	f.Add("default")
	f.Add("latency_critical")
	f.Add("latency-critical")
	f.Add("memory_constrained")
	f.Add("batch_etl")
	f.Add("adaptive")
	f.Add("unknown_profile")
	f.Add("")

	f.Fuzz(func(t *testing.T, profileStr string) {
		_, _ = ParseGCProfile(profileStr)
	})
}

func FuzzParseByteSize(f *testing.F) {
	f.Add("128MB")
	f.Add("1GB")
	f.Add("512KiB")
	f.Add("1048576")
	f.Add("invalid")
	f.Add("-10MB")
	f.Add("99999999999999999999999999TB")

	f.Fuzz(func(t *testing.T, input string) {
		_, _ = ParseByteSize(input)
	})
}

func FuzzReadLimitsFrom(f *testing.F) {
	f.Add("max\n", "200000 100000\n")
	f.Add("1073741824\n", "max 100000\n")
	f.Add("invalid\n", "invalid\n")
	f.Add("0\n", "0 0\n")
	f.Add("-1\n", "-1 -1\n")

	f.Fuzz(func(t *testing.T, memContent string, cpuContent string) {
		tempDir := t.TempDir()
		memPath := filepath.Join(tempDir, "memory.max")
		cpuPath := filepath.Join(tempDir, "cpu.max")

		_ = os.WriteFile(memPath, []byte(memContent), 0o600)
		_ = os.WriteFile(cpuPath, []byte(cpuContent), 0o600)

		limits, err := ReadLimitsFrom(tempDir)
		if err == nil {
			if limits.CgroupVersion != VersionV2 {
				t.Fatalf("expected VersionV2, got %d", limits.CgroupVersion)
			}
		}
	})
}
