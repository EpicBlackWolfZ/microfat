package env

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

const (
	testBytes1GB   = int64(1073741824)
	testBytes512MB = int64(536870912)
	testBytes2MB   = int64(2000000)
	testBytes1MB   = int64(1000000)
	testQuota200ms = int64(200000)
	testPeriod100ms = int64(100000)
	testQuota50ms  = int64(50000)
	testQuota40ms  = int64(40000)
	testFreq800MHz = uint64(800000)
	testFreq42GHz  = uint64(4200000)
	testMem16GBKB  = uint64(16384000)
	testMem12GBKB  = uint64(12288000)
	testMemScaleKB = uint64(1024)
	testCoresCount = 4
	testSockCount  = 2
	testNumaCount  = 2
	testGOMAXPROCS = 6
)

func TestDetectHost(t *testing.T) {
	t.Parallel()

	snap, err := Detect()
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}

	if snap.Host.OS != runtime.GOOS {
		t.Errorf("expected OS %s, got %s", runtime.GOOS, snap.Host.OS)
	}
	if snap.Host.Arch != runtime.GOARCH {
		t.Errorf("expected Arch %s, got %s", runtime.GOARCH, snap.Host.Arch)
	}
	if snap.Process.PID != os.Getpid() {
		t.Errorf("expected PID %d, got %d", os.Getpid(), snap.Process.PID)
	}
	if snap.Process.ExecutablePath == "" {
		t.Error("expected non-empty ExecutablePath")
	}
	if snap.Process.GoVersion != runtime.Version() {
		t.Errorf("expected GoVersion %s, got %s", runtime.Version(), snap.Process.GoVersion)
	}
	if snap.Process.GOMAXPROCSEffective <= 0 {
		t.Errorf("expected positive GOMAXPROCSEffective, got %d", snap.Process.GOMAXPROCSEffective)
	}

	if valErr := schema.ValidateResourceLimit(snap.Host.Cgroup.MemoryMaxBytes); valErr != nil {
		t.Errorf("invalid MemoryMaxBytes resource limit: %v", valErr)
	}
	if valErr := schema.ValidateResourceLimit(snap.Host.Cgroup.CPUQuotaUs); valErr != nil {
		t.Errorf("invalid CPUQuotaUs resource limit: %v", valErr)
	}
}

func TestCustomDetector_MockCgroupV2_FiniteLimits(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cgroupRoot := filepath.Join(tmpDir, "cgroup")
	procDir := filepath.Join(tmpDir, "proc")
	sysDir := filepath.Join(tmpDir, "sys")

	if err := os.MkdirAll(cgroupRoot, 0o755); err != nil {
		t.Fatalf("mkdir cgroupRoot: %v", err)
	}
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatalf("mkdir procDir: %v", err)
	}
	if err := os.MkdirAll(sysDir, 0o755); err != nil {
		t.Fatalf("mkdir sysDir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(cgroupRoot, cgroupV2ControllersFile), []byte("cpu memory\n"), 0o600); err != nil {
		t.Fatalf("writing controllers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cgroupRoot, cgroupV2MemoryMaxFile), []byte("1073741824\n"), 0o600); err != nil {
		t.Fatalf("writing memory.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cgroupRoot, cgroupV2CPUMaxFile), []byte("200000 100000\n"), 0o600); err != nil {
		t.Fatalf("writing cpu.max: %v", err)
	}

	procCgroupPath := filepath.Join(procDir, "self_cgroup")
	if err := os.WriteFile(procCgroupPath, []byte("0::/\n"), 0o600); err != nil {
		t.Fatalf("writing proc cgroup: %v", err)
	}

	detector := NewCustomDetector(cgroupRoot, procCgroupPath, procDir, sysDir)
	snap, err := detector.Detect()
	if err != nil {
		t.Fatalf("detect failed: %v", err)
	}

	if snap.Host.Cgroup.Version != cgroupVersionV2 {
		t.Errorf("expected version %s, got %s", cgroupVersionV2, snap.Host.Cgroup.Version)
	}
	if snap.Host.Cgroup.MemoryMaxBytes.State != schema.LimitStateFinite {
		t.Errorf("expected finite memory limit, got %s", snap.Host.Cgroup.MemoryMaxBytes.State)
	}
	if snap.Host.Cgroup.MemoryMaxBytes.Value == nil || *snap.Host.Cgroup.MemoryMaxBytes.Value != testBytes1GB {
		t.Errorf("expected memory limit %d, got %v", testBytes1GB, snap.Host.Cgroup.MemoryMaxBytes.Value)
	}

	if snap.Host.Cgroup.CPUQuotaUs.State != schema.LimitStateFinite {
		t.Errorf("expected finite cpu quota, got %s", snap.Host.Cgroup.CPUQuotaUs.State)
	}
	if snap.Host.Cgroup.CPUQuotaUs.Value == nil || *snap.Host.Cgroup.CPUQuotaUs.Value != testQuota200ms {
		t.Errorf("expected cpu quota %d, got %v", testQuota200ms, snap.Host.Cgroup.CPUQuotaUs.Value)
	}

	if snap.Host.Cgroup.CPUPeriodUs == nil || *snap.Host.Cgroup.CPUPeriodUs != testPeriod100ms {
		t.Errorf("expected cpu period %d, got %v", testPeriod100ms, snap.Host.Cgroup.CPUPeriodUs)
	}
}

func TestCustomDetector_MockCgroupV2_Unlimited(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cgroupRoot := filepath.Join(tmpDir, "cgroup")
	procDir := filepath.Join(tmpDir, "proc")
	sysDir := filepath.Join(tmpDir, "sys")

	if err := os.MkdirAll(cgroupRoot, 0o755); err != nil {
		t.Fatalf("mkdir cgroupRoot: %v", err)
	}
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatalf("mkdir procDir: %v", err)
	}
	if err := os.MkdirAll(sysDir, 0o755); err != nil {
		t.Fatalf("mkdir sysDir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(cgroupRoot, cgroupV2ControllersFile), []byte("cpu memory\n"), 0o600); err != nil {
		t.Fatalf("writing controllers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cgroupRoot, cgroupV2MemoryMaxFile), []byte("max\n"), 0o600); err != nil {
		t.Fatalf("writing memory.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cgroupRoot, cgroupV2CPUMaxFile), []byte("max 100000\n"), 0o600); err != nil {
		t.Fatalf("writing cpu.max: %v", err)
	}

	procCgroupPath := filepath.Join(procDir, "self_cgroup")
	if err := os.WriteFile(procCgroupPath, []byte("0::/\n"), 0o600); err != nil {
		t.Fatalf("writing proc cgroup: %v", err)
	}

	detector := NewCustomDetector(cgroupRoot, procCgroupPath, procDir, sysDir)
	snap, err := detector.Detect()
	if err != nil {
		t.Fatalf("detect failed: %v", err)
	}

	if snap.Host.Cgroup.Version != cgroupVersionV2 {
		t.Errorf("expected version %s, got %s", cgroupVersionV2, snap.Host.Cgroup.Version)
	}
	if snap.Host.Cgroup.MemoryMaxBytes.State != schema.LimitStateUnlimited {
		t.Errorf("expected unlimited memory limit, got %s", snap.Host.Cgroup.MemoryMaxBytes.State)
	}
	if snap.Host.Cgroup.MemoryMaxBytes.Value != nil {
		t.Errorf("expected nil memory value, got %v", snap.Host.Cgroup.MemoryMaxBytes.Value)
	}

	if snap.Host.Cgroup.CPUQuotaUs.State != schema.LimitStateUnlimited {
		t.Errorf("expected unlimited cpu quota, got %s", snap.Host.Cgroup.CPUQuotaUs.State)
	}
	if snap.Host.Cgroup.CPUQuotaUs.Value != nil {
		t.Errorf("expected nil cpu quota value, got %v", snap.Host.Cgroup.CPUQuotaUs.Value)
	}

	if snap.Host.Cgroup.CPUPeriodUs == nil || *snap.Host.Cgroup.CPUPeriodUs != testPeriod100ms {
		t.Errorf("expected cpu period %d, got %v", testPeriod100ms, snap.Host.Cgroup.CPUPeriodUs)
	}
}

func TestCustomDetector_MockCgroupV2_HierarchyInheritance(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cgroupRoot := filepath.Join(tmpDir, "cgroup")
	subgroupDir := filepath.Join(cgroupRoot, "subgroup")
	procDir := filepath.Join(tmpDir, "proc")
	sysDir := filepath.Join(tmpDir, "sys")

	if err := os.MkdirAll(subgroupDir, 0o755); err != nil {
		t.Fatalf("mkdir subgroup: %v", err)
	}
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatalf("mkdir procDir: %v", err)
	}
	if err := os.MkdirAll(sysDir, 0o755); err != nil {
		t.Fatalf("mkdir sysDir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(cgroupRoot, cgroupV2ControllersFile), []byte("cpu memory\n"), 0o600); err != nil {
		t.Fatalf("writing root controllers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cgroupRoot, cgroupV2MemoryMaxFile), []byte("2000000\n"), 0o600); err != nil {
		t.Fatalf("writing root memory.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cgroupRoot, cgroupV2CPUMaxFile), []byte("max 100000\n"), 0o600); err != nil {
		t.Fatalf("writing root cpu.max: %v", err)
	}

	if err := os.WriteFile(filepath.Join(subgroupDir, cgroupV2MemoryMaxFile), []byte("1000000\n"), 0o600); err != nil {
		t.Fatalf("writing subgroup memory.max: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subgroupDir, cgroupV2CPUMaxFile), []byte("50000 100000\n"), 0o600); err != nil {
		t.Fatalf("writing subgroup cpu.max: %v", err)
	}

	procCgroupPath := filepath.Join(procDir, "self_cgroup")
	if err := os.WriteFile(procCgroupPath, []byte("0::/subgroup\n"), 0o600); err != nil {
		t.Fatalf("writing proc cgroup: %v", err)
	}

	detector := NewCustomDetector(cgroupRoot, procCgroupPath, procDir, sysDir)
	snap, err := detector.Detect()
	if err != nil {
		t.Fatalf("detect failed: %v", err)
	}

	if snap.Host.Cgroup.MemoryMaxBytes.Value == nil || *snap.Host.Cgroup.MemoryMaxBytes.Value != testBytes1MB {
		t.Errorf("expected memory limit %d, got %v", testBytes1MB, snap.Host.Cgroup.MemoryMaxBytes.Value)
	}
	if snap.Host.Cgroup.CPUQuotaUs.Value == nil || *snap.Host.Cgroup.CPUQuotaUs.Value != testQuota50ms {
		t.Errorf("expected cpu quota %d, got %v", testQuota50ms, snap.Host.Cgroup.CPUQuotaUs.Value)
	}
}

func TestCustomDetector_MockCgroupV1(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cgroupRoot := filepath.Join(tmpDir, "cgroup")
	memDir := filepath.Join(cgroupRoot, "memory", "sub")
	cpuDir := filepath.Join(cgroupRoot, "cpu", "sub")
	procDir := filepath.Join(tmpDir, "proc")
	sysDir := filepath.Join(tmpDir, "sys")

	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("mkdir memDir: %v", err)
	}
	if err := os.MkdirAll(cpuDir, 0o755); err != nil {
		t.Fatalf("mkdir cpuDir: %v", err)
	}
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatalf("mkdir procDir: %v", err)
	}
	if err := os.MkdirAll(sysDir, 0o755); err != nil {
		t.Fatalf("mkdir sysDir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(memDir, cgroupV1MemoryLimitFile), []byte("536870912\n"), 0o600); err != nil {
		t.Fatalf("writing v1 memory.limit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cpuDir, cgroupV1CPUQuotaFile), []byte("40000\n"), 0o600); err != nil {
		t.Fatalf("writing v1 cpu quota: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cpuDir, cgroupV1CPUPeriodFile), []byte("100000\n"), 0o600); err != nil {
		t.Fatalf("writing v1 cpu period: %v", err)
	}

	procCgroupPath := filepath.Join(procDir, "self_cgroup")
	if err := os.WriteFile(procCgroupPath, []byte("2:memory:/sub\n1:cpu:/sub\n"), 0o600); err != nil {
		t.Fatalf("writing proc cgroup: %v", err)
	}

	detector := NewCustomDetector(cgroupRoot, procCgroupPath, procDir, sysDir)
	snap, err := detector.Detect()
	if err != nil {
		t.Fatalf("detect failed: %v", err)
	}

	if snap.Host.Cgroup.Version != cgroupVersionV1 {
		t.Errorf("expected version %s, got %s", cgroupVersionV1, snap.Host.Cgroup.Version)
	}
	if snap.Host.Cgroup.MemoryMaxBytes.State != schema.LimitStateFinite {
		t.Errorf("expected finite memory limit, got %s", snap.Host.Cgroup.MemoryMaxBytes.State)
	}
	if snap.Host.Cgroup.MemoryMaxBytes.Value == nil || *snap.Host.Cgroup.MemoryMaxBytes.Value != testBytes512MB {
		t.Errorf("expected memory limit %d, got %v", testBytes512MB, snap.Host.Cgroup.MemoryMaxBytes.Value)
	}

	if snap.Host.Cgroup.CPUQuotaUs.State != schema.LimitStateFinite {
		t.Errorf("expected finite cpu quota, got %s", snap.Host.Cgroup.CPUQuotaUs.State)
	}
	if snap.Host.Cgroup.CPUQuotaUs.Value == nil || *snap.Host.Cgroup.CPUQuotaUs.Value != testQuota40ms {
		t.Errorf("expected cpu quota %d, got %v", testQuota40ms, snap.Host.Cgroup.CPUQuotaUs.Value)
	}
	if snap.Host.Cgroup.CPUPeriodUs == nil || *snap.Host.Cgroup.CPUPeriodUs != testPeriod100ms {
		t.Errorf("expected cpu period %d, got %v", testPeriod100ms, snap.Host.Cgroup.CPUPeriodUs)
	}
}

func TestCustomDetector_MockCgroupV1_Unlimited(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cgroupRoot := filepath.Join(tmpDir, "cgroup")
	memDir := filepath.Join(cgroupRoot, "memory")
	cpuDir := filepath.Join(cgroupRoot, "cpu,cpuacct")
	procDir := filepath.Join(tmpDir, "proc")
	sysDir := filepath.Join(tmpDir, "sys")

	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("mkdir memDir: %v", err)
	}
	if err := os.MkdirAll(cpuDir, 0o755); err != nil {
		t.Fatalf("mkdir cpuDir: %v", err)
	}
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatalf("mkdir procDir: %v", err)
	}
	if err := os.MkdirAll(sysDir, 0o755); err != nil {
		t.Fatalf("mkdir sysDir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(memDir, cgroupV1MemoryLimitFile), []byte("-1\n"), 0o600); err != nil {
		t.Fatalf("writing memory.limit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cpuDir, cgroupV1CPUQuotaFile), []byte("-1\n"), 0o600); err != nil {
		t.Fatalf("writing cpu quota: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cpuDir, cgroupV1CPUPeriodFile), []byte("100000\n"), 0o600); err != nil {
		t.Fatalf("writing cpu period: %v", err)
	}

	procCgroupPath := filepath.Join(procDir, "self_cgroup")
	if err := os.WriteFile(procCgroupPath, []byte("2:memory:/\n1:cpu,cpuacct:/\n"), 0o600); err != nil {
		t.Fatalf("writing proc cgroup: %v", err)
	}

	detector := NewCustomDetector(cgroupRoot, procCgroupPath, procDir, sysDir)
	snap, err := detector.Detect()
	if err != nil {
		t.Fatalf("detect failed: %v", err)
	}

	if snap.Host.Cgroup.Version != cgroupVersionV1 {
		t.Errorf("expected version %s, got %s", cgroupVersionV1, snap.Host.Cgroup.Version)
	}
	if snap.Host.Cgroup.MemoryMaxBytes.State != schema.LimitStateUnlimited {
		t.Errorf("expected unlimited memory limit, got %s", snap.Host.Cgroup.MemoryMaxBytes.State)
	}
	if snap.Host.Cgroup.CPUQuotaUs.State != schema.LimitStateUnlimited {
		t.Errorf("expected unlimited cpu quota, got %s", snap.Host.Cgroup.CPUQuotaUs.State)
	}
}

func TestCustomDetector_HybridCgroup_PrefersV1WhenV1MountPresent(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cgroupRoot := filepath.Join(tmpDir, "cgroup")
	memDir := filepath.Join(cgroupRoot, "memory", "sub")
	cpuDir := filepath.Join(cgroupRoot, "cpu", "sub")
	procDir := filepath.Join(tmpDir, "proc")
	sysDir := filepath.Join(tmpDir, "sys")

	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("mkdir memDir: %v", err)
	}
	if err := os.MkdirAll(cpuDir, 0o755); err != nil {
		t.Fatalf("mkdir cpuDir: %v", err)
	}
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatalf("mkdir procDir: %v", err)
	}
	if err := os.MkdirAll(sysDir, 0o755); err != nil {
		t.Fatalf("mkdir sysDir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(memDir, cgroupV1MemoryLimitFile), []byte("536870912\n"), 0o600); err != nil {
		t.Fatalf("writing v1 memory.limit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cpuDir, cgroupV1CPUQuotaFile), []byte("40000\n"), 0o600); err != nil {
		t.Fatalf("writing v1 cpu quota: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cpuDir, cgroupV1CPUPeriodFile), []byte("100000\n"), 0o600); err != nil {
		t.Fatalf("writing v1 cpu period: %v", err)
	}

	// Contains both cgroup v2 (0::) and v1 controller hierarchies
	procCgroupPath := filepath.Join(procDir, "self_cgroup")
	if err := os.WriteFile(procCgroupPath, []byte("2:memory:/sub\n1:cpu:/sub\n0::/sub\n"), 0o600); err != nil {
		t.Fatalf("writing proc cgroup: %v", err)
	}

	detector := NewCustomDetector(cgroupRoot, procCgroupPath, procDir, sysDir)
	snap, err := detector.Detect()
	if err != nil {
		t.Fatalf("detect failed: %v", err)
	}

	if snap.Host.Cgroup.Version != cgroupVersionV1 {
		t.Errorf("expected version %s for hybrid system with v1 controllers, got %s",
			cgroupVersionV1, snap.Host.Cgroup.Version)
	}
	if snap.Host.Cgroup.MemoryMaxBytes.State != schema.LimitStateFinite {
		t.Errorf("expected finite memory limit, got %s", snap.Host.Cgroup.MemoryMaxBytes.State)
	}
	if snap.Host.Cgroup.MemoryMaxBytes.Value == nil || *snap.Host.Cgroup.MemoryMaxBytes.Value != testBytes512MB {
		t.Errorf("expected memory limit %d, got %v", testBytes512MB, snap.Host.Cgroup.MemoryMaxBytes.Value)
	}
	if snap.Host.Cgroup.CPUQuotaUs.State != schema.LimitStateFinite {
		t.Errorf("expected finite cpu quota, got %s", snap.Host.Cgroup.CPUQuotaUs.State)
	}
	if snap.Host.Cgroup.CPUQuotaUs.Value == nil || *snap.Host.Cgroup.CPUQuotaUs.Value != testQuota40ms {
		t.Errorf("expected cpu quota %d, got %v", testQuota40ms, snap.Host.Cgroup.CPUQuotaUs.Value)
	}
}

func TestCustomDetector_InaccessibleAndCorruptedCgroup(t *testing.T) {
	t.Parallel()

	t.Run("cgroup_mount_does_not_exist", func(t *testing.T) {
		t.Parallel()
		d := NewCustomDetector("/nonexistent/cgroup/mount", "/nonexistent/proc", "/nonexistent/proc", "/nonexistent/sys")
		snap, err := d.Detect()
		if err != nil {
			t.Fatalf("expected no fatal error, got %v", err)
		}
		if snap.Host.Cgroup.Version != cgroupVersionUnavailable {
			t.Errorf("expected %s, got %s", cgroupVersionUnavailable, snap.Host.Cgroup.Version)
		}
		if snap.Host.Cgroup.MemoryMaxBytes.State != schema.LimitStateUnavailable {
			t.Errorf("expected unavailable memory limit, got %s", snap.Host.Cgroup.MemoryMaxBytes.State)
		}
		if snap.Host.Cgroup.CPUQuotaUs.State != schema.LimitStateUnavailable {
			t.Errorf("expected unavailable cpu quota, got %s", snap.Host.Cgroup.CPUQuotaUs.State)
		}
		if len(snap.Warnings) == 0 {
			t.Error("expected non-empty warnings")
		}
	})

	t.Run("cgroup_hierarchy_not_detected", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		d := NewCustomDetector(tmpDir, filepath.Join(tmpDir, "proc_cgroup"), tmpDir, tmpDir)
		snap, err := d.Detect()
		if err != nil {
			t.Fatalf("expected no fatal error, got %v", err)
		}
		if snap.Host.Cgroup.Version != cgroupVersionUnavailable {
			t.Errorf("expected %s, got %s", cgroupVersionUnavailable, snap.Host.Cgroup.Version)
		}
	})

	t.Run("cgroup_v2_corrupted_limits", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmpDir, cgroupV2ControllersFile), []byte("cpu memory\n"), 0o600); err != nil {
			t.Fatalf("write controllers: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, cgroupV2MemoryMaxFile), []byte("not_a_number\n"), 0o600); err != nil {
			t.Fatalf("write memory.max: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, cgroupV2CPUMaxFile), []byte("invalid_format\n"), 0o600); err != nil {
			t.Fatalf("write cpu.max: %v", err)
		}

		d := NewCustomDetector(tmpDir, filepath.Join(tmpDir, "nonexistent_proc_cgroup"), tmpDir, tmpDir)
		snap, err := d.Detect()
		if err != nil {
			t.Fatalf("detect failed: %v", err)
		}
		if snap.Host.Cgroup.MemoryMaxBytes.State != schema.LimitStateUnavailable {
			t.Errorf("expected unavailable memory limit, got %s", snap.Host.Cgroup.MemoryMaxBytes.State)
		}
		if snap.Host.Cgroup.CPUQuotaUs.State != schema.LimitStateUnavailable {
			t.Errorf("expected unavailable cpu quota, got %s", snap.Host.Cgroup.CPUQuotaUs.State)
		}
	})

	t.Run("cgroup_v1_corrupted_limits", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		memDir := filepath.Join(tmpDir, "memory")
		cpuDir := filepath.Join(tmpDir, "cpu")
		if err := os.MkdirAll(memDir, 0o755); err != nil {
			t.Fatalf("mkdir memDir: %v", err)
		}
		if err := os.MkdirAll(cpuDir, 0o755); err != nil {
			t.Fatalf("mkdir cpuDir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(memDir, cgroupV1MemoryLimitFile), []byte("not_a_number\n"), 0o600); err != nil {
			t.Fatalf("write memory.limit: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cpuDir, cgroupV1CPUQuotaFile), []byte("bad_quota\n"), 0o600); err != nil {
			t.Fatalf("write cpu quota: %v", err)
		}

		procCgroupPath := filepath.Join(tmpDir, "proc_cgroup")
		if err := os.WriteFile(procCgroupPath, []byte("2:memory:/\n1:cpu:/\n"), 0o600); err != nil {
			t.Fatalf("write proc cgroup: %v", err)
		}

		d := NewCustomDetector(tmpDir, procCgroupPath, tmpDir, tmpDir)
		snap, err := d.Detect()
		if err != nil {
			t.Fatalf("detect failed: %v", err)
		}
		if snap.Host.Cgroup.MemoryMaxBytes.State != schema.LimitStateUnavailable {
			t.Errorf("expected unavailable memory limit, got %s", snap.Host.Cgroup.MemoryMaxBytes.State)
		}
		if snap.Host.Cgroup.CPUQuotaUs.State != schema.LimitStateUnavailable {
			t.Errorf("expected unavailable cpu quota, got %s", snap.Host.Cgroup.CPUQuotaUs.State)
		}
	})
}

func TestCustomDetector_MockHardwareDiscovery(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	procDir := filepath.Join(tmpDir, "proc")
	sysDir := filepath.Join(tmpDir, "sys")
	cgroupDir := filepath.Join(tmpDir, "cgroup")

	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatalf("mkdir proc: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(procDir, "sys/kernel"), 0o755); err != nil {
		t.Fatalf("mkdir sys/kernel: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sysDir, "devices/system/node/node0"), 0o755); err != nil {
		t.Fatalf("mkdir node0: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sysDir, "devices/system/node/node1"), 0o755); err != nil {
		t.Fatalf("mkdir node1: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(sysDir, "devices/system/cpu/cpu0/cpufreq"), 0o755); err != nil {
		t.Fatalf("mkdir cpufreq: %v", err)
	}
	if err := os.MkdirAll(cgroupDir, 0o755); err != nil {
		t.Fatalf("mkdir cgroup: %v", err)
	}

	if err := os.WriteFile(filepath.Join(procDir, "sys/kernel/osrelease"), []byte("6.8.0-test\n"), 0o600); err != nil {
		t.Fatalf("write osrelease: %v", err)
	}

	cpuinfoContent := `processor	: 0
model name	: Test Virtual CPU
physical id	: 0
cpu cores	: 4
flags		: fpu vme de sse sse2 avx avx2

processor	: 1
model name	: Test Virtual CPU
physical id	: 1
cpu cores	: 4
flags		: fpu vme de sse sse2 avx avx2
`
	if err := os.WriteFile(filepath.Join(procDir, "cpuinfo"), []byte(cpuinfoContent), 0o600); err != nil {
		t.Fatalf("write cpuinfo: %v", err)
	}

	meminfoContent := `MemTotal:       16384000 kB
MemFree:         8192000 kB
MemAvailable:   12288000 kB
`
	if err := os.WriteFile(filepath.Join(procDir, "meminfo"), []byte(meminfoContent), 0o600); err != nil {
		t.Fatalf("write meminfo: %v", err)
	}

	freqDir := filepath.Join(sysDir, "devices/system/cpu/cpu0/cpufreq")
	if err := os.WriteFile(filepath.Join(freqDir, "scaling_governor"), []byte("performance\n"), 0o600); err != nil {
		t.Fatalf("write governor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(freqDir, "scaling_min_freq"), []byte("800000\n"), 0o600); err != nil {
		t.Fatalf("write min_freq: %v", err)
	}
	if err := os.WriteFile(filepath.Join(freqDir, "scaling_max_freq"), []byte("4200000\n"), 0o600); err != nil {
		t.Fatalf("write max_freq: %v", err)
	}

	detector := NewCustomDetector(cgroupDir, filepath.Join(procDir, "self_cgroup"), procDir, sysDir)
	snap, err := detector.Detect()
	if err != nil {
		t.Fatalf("detect failed: %v", err)
	}

	if snap.Host.KernelRelease != "6.8.0-test" {
		t.Errorf("expected kernel release 6.8.0-test, got %s", snap.Host.KernelRelease)
	}
	if snap.Host.CPU.ModelName != "Test Virtual CPU" {
		t.Errorf("expected model name 'Test Virtual CPU', got %s", snap.Host.CPU.ModelName)
	}
	if snap.Host.CPU.Cores == nil || *snap.Host.CPU.Cores != testCoresCount {
		t.Errorf("expected %d cores, got %v", testCoresCount, snap.Host.CPU.Cores)
	}
	if snap.Host.CPU.Sockets == nil || *snap.Host.CPU.Sockets != testSockCount {
		t.Errorf("expected %d sockets, got %v", testSockCount, snap.Host.CPU.Sockets)
	}
	if snap.Host.CPU.NUMANodes == nil || *snap.Host.CPU.NUMANodes != testNumaCount {
		t.Errorf("expected %d numa nodes, got %v", testNumaCount, snap.Host.CPU.NUMANodes)
	}

	if snap.Host.CPU.ScalingGovernor == nil || *snap.Host.CPU.ScalingGovernor != "performance" {
		t.Errorf("expected governor 'performance', got %v", snap.Host.CPU.ScalingGovernor)
	}
	if snap.Host.CPU.MinFreqKHz == nil || *snap.Host.CPU.MinFreqKHz != testFreq800MHz {
		t.Errorf("expected min freq %d, got %v", testFreq800MHz, snap.Host.CPU.MinFreqKHz)
	}
	if snap.Host.CPU.MaxFreqKHz == nil || *snap.Host.CPU.MaxFreqKHz != testFreq42GHz {
		t.Errorf("expected max freq %d, got %v", testFreq42GHz, snap.Host.CPU.MaxFreqKHz)
	}

	expectedTotal := testMem16GBKB * testMemScaleKB
	if snap.Host.Memory.TotalBytes == nil || *snap.Host.Memory.TotalBytes != expectedTotal {
		t.Errorf("expected total memory %d, got %v", expectedTotal, snap.Host.Memory.TotalBytes)
	}
	expectedAvail := testMem12GBKB * testMemScaleKB
	if snap.Host.Memory.AvailableBytes == nil || *snap.Host.Memory.AvailableBytes != expectedAvail {
		t.Errorf("expected available memory %d, got %v", expectedAvail, snap.Host.Memory.AvailableBytes)
	}
}

func TestProcessContext_EnvAndAffinity(t *testing.T) {
	t.Setenv("GOMAXPROCS", "6")
	t.Setenv("GOMEMLIMIT", "4GiB")
	t.Setenv("MICROFAT_AFFINITY", "0,2,4")
	t.Setenv("GODEBUG", "gctrace=1")

	detector := NewDetector()
	snap, err := detector.Detect()
	if err != nil {
		t.Fatalf("detect failed: %v", err)
	}

	if snap.Process.GOMAXPROCSConfigured == nil || *snap.Process.GOMAXPROCSConfigured != testGOMAXPROCS {
		t.Errorf("expected configured GOMAXPROCS %d, got %v", testGOMAXPROCS, snap.Process.GOMAXPROCSConfigured)
	}
	if snap.Process.GOMEMLIMITConfigured == nil || *snap.Process.GOMEMLIMITConfigured != "4GiB" {
		t.Errorf("expected configured GOMEMLIMIT '4GiB', got %v", snap.Process.GOMEMLIMITConfigured)
	}

	expectedAffinity := []int{0, 2, 4}
	if len(snap.Process.RequestedAffinity) != len(expectedAffinity) {
		t.Errorf("expected affinity %v, got %v", expectedAffinity, snap.Process.RequestedAffinity)
	} else {
		for i, v := range expectedAffinity {
			if snap.Process.RequestedAffinity[i] != v {
				t.Errorf("expected affinity at %d = %d, got %d", i, v, snap.Process.RequestedAffinity[i])
			}
		}
	}

	foundGODEBUG := false
	for _, ev := range snap.Process.EnvironmentVariables {
		if ev.Name == "GODEBUG" {
			foundGODEBUG = true
			if !ev.IsSet {
				t.Error("expected GODEBUG to be set")
			}
			if ev.Value == nil || *ev.Value != "gctrace=1" {
				t.Errorf("expected GODEBUG value 'gctrace=1', got %v", ev.Value)
			}
		}
	}
	if !foundGODEBUG {
		t.Error("expected GODEBUG in whitelisted environment variables")
	}
}

func TestContainerDetection(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	procDir := filepath.Join(tmpDir, "proc")
	if err := os.MkdirAll(filepath.Join(procDir, "1"), 0o755); err != nil {
		t.Fatalf("mkdir proc/1: %v", err)
	}

	initCgroup := filepath.Join(procDir, "1/cgroup")
	if err := os.WriteFile(initCgroup, []byte("0::/docker/abc123def456\n"), 0o600); err != nil {
		t.Fatalf("write init cgroup: %v", err)
	}

	detector := NewCustomDetector(tmpDir, filepath.Join(procDir, "self_cgroup"), procDir, tmpDir)
	if !detector.isInContainer() {
		t.Error("expected isInContainer to be true")
	}
}

func TestCustomDetector_CPUFreqFallback(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	procDir := filepath.Join(tmpDir, "proc")
	sysDir := filepath.Join(tmpDir, "sys")
	freqDir := filepath.Join(sysDir, "devices/system/cpu/cpu0/cpufreq")
	if err := os.MkdirAll(freqDir, 0o755); err != nil {
		t.Fatalf("mkdir freqDir: %v", err)
	}
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatalf("mkdir procDir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(freqDir, "cpuinfo_min_freq"), []byte("900000\n"), 0o600); err != nil {
		t.Fatalf("write cpuinfo_min_freq: %v", err)
	}
	if err := os.WriteFile(filepath.Join(freqDir, "cpuinfo_max_freq"), []byte("3800000\n"), 0o600); err != nil {
		t.Fatalf("write cpuinfo_max_freq: %v", err)
	}

	d := NewCustomDetector(tmpDir, filepath.Join(procDir, "self_cgroup"), procDir, sysDir)
	gov, minF, maxF, warn := d.detectCPUFreq()
	if gov != nil {
		t.Errorf("expected nil governor, got %v", *gov)
	}
	if minF == nil || *minF != uint64(900000) {
		t.Errorf("expected min freq 900000, got %v", minF)
	}
	if maxF == nil || *maxF != uint64(3800000) {
		t.Errorf("expected max freq 3800000, got %v", maxF)
	}
	if len(warn) == 0 {
		t.Error("expected warnings for missing scaling_governor")
	}
}

func TestCustomDetector_MemoryTelemetryErrors(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	procDir := filepath.Join(tmpDir, "proc")
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatalf("mkdir procDir: %v", err)
	}

	// Corrupted meminfo (no numbers)
	if err := os.WriteFile(filepath.Join(procDir, "meminfo"), []byte("MemTotal: invalid\nMemAvailable: bad\n"), 0o600); err != nil {
		t.Fatalf("write meminfo: %v", err)
	}

	d := NewCustomDetector(tmpDir, filepath.Join(procDir, "self_cgroup"), procDir, tmpDir)
	memInfo, warn := d.detectMemory()
	if memInfo.TotalBytes != nil || memInfo.AvailableBytes != nil {
		t.Errorf("expected nil memory pointers on corrupted meminfo")
	}
	if len(warn) < 2 {
		t.Errorf("expected at least 2 warnings, got %v", warn)
	}
}

func TestCustomDetector_CPUInfoVariants(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	procDir := filepath.Join(tmpDir, "proc")
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatalf("mkdir proc: %v", err)
	}

	cpuinfoARM := `Processor	: AArch64 Processor rev 0 (aarch64)
Hardware	: BCM2835
Features	: fp asimd evtstrm crc32
processor	: 0
processor	: 1
`
	path := filepath.Join(procDir, "cpuinfo")
	if err := os.WriteFile(path, []byte(cpuinfoARM), 0o600); err != nil {
		t.Fatalf("write cpuinfo: %v", err)
	}

	model, flags, cores, sockets := parseLinuxCPUInfo(path)
	if model != "AArch64 Processor rev 0 (aarch64)" {
		t.Errorf("expected model name 'AArch64 Processor rev 0 (aarch64)', got %s", model)
	}
	if len(flags) != 4 {
		t.Errorf("expected 4 flags, got %v", flags)
	}
	if cores == nil || *cores != 2 {
		t.Errorf("expected 2 cores, got %v", cores)
	}
	if sockets != nil {
		t.Errorf("expected nil sockets, got %v", sockets)
	}
}

func TestParseRequestedAffinity_EdgeCases(t *testing.T) {
	t.Setenv(envMicrofatAffinity, "0, , -1, abc, 3")
	aff := parseRequestedAffinity()
	expected := []int{0, 3}
	if len(aff) != len(expected) || aff[0] != 0 || aff[1] != 3 {
		t.Errorf("expected %v, got %v", expected, aff)
	}
}

func TestReadTrimmedLine_Empty(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	emptyFile := filepath.Join(tmpDir, "empty.txt")
	if err := os.WriteFile(emptyFile, []byte(""), 0o600); err != nil {
		t.Fatalf("write empty: %v", err)
	}

	_, err := readTrimmedLine(emptyFile)
	if err == nil {
		t.Error("expected error for empty file")
	}
}

func TestCustomDetector_CPUFreqPolicy0Fallback(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	procDir := filepath.Join(tmpDir, "proc")
	sysDir := filepath.Join(tmpDir, "sys")
	policy0Dir := filepath.Join(sysDir, "devices/system/cpu/cpufreq/policy0")
	if err := os.MkdirAll(policy0Dir, 0o755); err != nil {
		t.Fatalf("mkdir policy0Dir: %v", err)
	}
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatalf("mkdir procDir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(policy0Dir, "scaling_governor"), []byte("schedutil\n"), 0o600); err != nil {
		t.Fatalf("write scaling_governor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(policy0Dir, "scaling_min_freq"), []byte("1200000\n"), 0o600); err != nil {
		t.Fatalf("write min_freq: %v", err)
	}
	if err := os.WriteFile(filepath.Join(policy0Dir, "scaling_max_freq"), []byte("4800000\n"), 0o600); err != nil {
		t.Fatalf("write max_freq: %v", err)
	}

	d := NewCustomDetector(tmpDir, filepath.Join(procDir, "self_cgroup"), procDir, sysDir)
	gov, minF, maxF, _ := d.detectCPUFreq()
	if gov == nil || *gov != "schedutil" {
		t.Errorf("expected governor schedutil, got %v", gov)
	}
	if minF == nil || *minF != uint64(1200000) {
		t.Errorf("expected min freq 1200000, got %v", minF)
	}
	if maxF == nil || *maxF != uint64(4800000) {
		t.Errorf("expected max freq 4800000, got %v", maxF)
	}
}

func TestCustomDetector_MultiSocketCoreID(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	procDir := filepath.Join(tmpDir, "proc")
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatalf("mkdir procDir: %v", err)
	}

	cpuinfoMultiSocket := `processor	: 0
physical id	: 0
core id		: 0
cpu cores	: 2

processor	: 1
physical id	: 0
core id		: 1
cpu cores	: 2

processor	: 2
physical id	: 1
core id		: 0
cpu cores	: 2

processor	: 3
physical id	: 1
core id		: 1
cpu cores	: 2
`
	path := filepath.Join(procDir, "cpuinfo")
	if err := os.WriteFile(path, []byte(cpuinfoMultiSocket), 0o600); err != nil {
		t.Fatalf("write cpuinfo: %v", err)
	}

	_, _, cores, sockets := parseLinuxCPUInfo(path)
	if sockets == nil || *sockets != 2 {
		t.Errorf("expected 2 sockets, got %v", sockets)
	}
	if cores == nil || *cores != 4 {
		t.Errorf("expected 4 total cores across sockets, got %v", cores)
	}
}

func TestCustomDetector_CoreIDWithoutPhysicalID(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	procDir := filepath.Join(tmpDir, "proc")
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatalf("mkdir procDir: %v", err)
	}

	cpuinfoSingleSocketVM := `processor	: 0
core id		: 0

processor	: 1
core id		: 0

processor	: 2
core id		: 1

processor	: 3
core id		: 1
`
	path := filepath.Join(procDir, "cpuinfo")
	if err := os.WriteFile(path, []byte(cpuinfoSingleSocketVM), 0o600); err != nil {
		t.Fatalf("write cpuinfo: %v", err)
	}

	_, _, cores, sockets := parseLinuxCPUInfo(path)
	if sockets != nil {
		t.Errorf("expected nil sockets, got %v", sockets)
	}
	if cores == nil || *cores != 2 {
		t.Errorf("expected 2 unique cores, got %v", cores)
	}
}


