package main

import (
	"bytes"
	json "encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/memfd"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"golang.org/x/sys/unix"
)

func TestDoctorCmdExecution(t *testing.T) {
	tempDir := t.TempDir()
	validCacheDir := filepath.Join(tempDir, "doctor_cache")

	t.Run("default text output", func(t *testing.T) {
		cmd := newDoctorCmd()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetArgs([]string{flagCacheDir, validCacheDir})

		err := cmd.Execute()
		if err != nil {
			t.Fatalf("expected doctor command to pass on healthy host: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "Microfat Host Environment Doctor") {
			t.Errorf("expected header in output, got:\n%s", out)
		}
		if !strings.Contains(out, "Host CPU Microarchitecture") {
			t.Errorf("expected Host CPU section, got:\n%s", out)
		}
		if !strings.Contains(out, "In-Memory Execution (memfd_create)") {
			t.Errorf("expected In-Memory section, got:\n%s", out)
		}
		if !strings.Contains(out, "Disk Cache Execution Fallback") {
			t.Errorf("expected Disk Cache section, got:\n%s", out)
		}
		if !strings.Contains(out, "Toolchain & Version Metadata") {
			t.Errorf("expected Toolchain section, got:\n%s", out)
		}
	})

	t.Run("json output schema compliance", func(t *testing.T) {
		cmd := newDoctorCmd()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetArgs([]string{flagJSON, flagCacheDir, validCacheDir})

		err := cmd.Execute()
		if err != nil {
			t.Fatalf("expected doctor --json command to pass: %v", err)
		}

		var report DoctorReport
		if err := json.Unmarshal(buf.Bytes(), &report); err != nil {
			t.Fatalf("failed to parse JSON doctor output: %v\nOutput: %s", err, buf.String())
		}

		if report.CPU.Arch == "" {
			t.Errorf("expected non-empty CPU arch in JSON report")
		}
		if report.CPU.OS == "" {
			t.Errorf("expected non-empty CPU OS in JSON report")
		}
		if !report.Cache.Ready {
			t.Errorf("expected cache to be ready for valid cache dir: %+v", report.Cache)
		}
		if report.Toolchain.Version == "" {
			t.Errorf("expected toolchain version in report")
		}
	})

	t.Run("strict mode on healthy environment", func(t *testing.T) {
		cmd := newDoctorCmd()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetArgs([]string{flagStrict, flagCacheDir, validCacheDir})

		_ = cmd.Execute()
	})

	t.Run("invalid cache directory triggers error", func(t *testing.T) {
		cmd := newDoctorCmd()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		invalidDir := "/dev/null/forbidden_cache_path"
		cmd.SetArgs([]string{flagCacheDir, invalidDir, flagStrict})

		err := cmd.Execute()
		if err == nil {
			t.Errorf("expected error when cache dir is invalid in strict mode")
		}
	})
}

func TestProbeCache(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("valid temp directory", func(t *testing.T) {
		validDir := filepath.Join(tempDir, "probe_cache_ok")
		res := probeCache(validDir)
		if !res.Ready || !res.Writable {
			t.Errorf("expected cache probe to succeed on valid dir, got: %+v", res)
		}
		if res.ResolvedPath != validDir {
			t.Errorf("expected resolved path %s, got %s", validDir, res.ResolvedPath)
		}
	})

	t.Run("unresolvable invalid path", func(t *testing.T) {
		invalidDir := "/dev/null/cannot_create_dir/sub"
		res := probeCache(invalidDir)
		if res.Ready || res.Writable {
			t.Errorf("expected cache probe to fail on invalid dir, got: %+v", res)
		}
		if res.Error == "" {
			t.Errorf("expected error message in probe result")
		}
	})

	t.Run("default environment resolution", func(t *testing.T) {
		t.Setenv(format.EnvCacheDir, filepath.Join(tempDir, "env_cache"))
		res := probeCache("")
		if !res.Ready || !res.Writable {
			t.Errorf("expected cache probe to succeed with MICROFAT_CACHE_DIR, got: %+v", res)
		}
		t.Setenv(format.EnvCacheDir, "")
	})
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{2048, "2048 B (2.00 KiB)"},
		{5 * 1024 * 1024, "5242880 B (5.00 MiB)"},
		{2 * 1024 * 1024 * 1024, "2147483648 B (2.00 GiB)"},
		{3 * int64(1024) * 1024 * 1024 * 1024, "3298534883328 B (3.00 TiB)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := formatBytes(tt.input)
			if got != tt.expected {
				t.Errorf("formatBytes(%d) = %q, expected %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestPrintDoctorReportVariations(t *testing.T) {
	t.Run("all green with cgroup v2", func(t *testing.T) {
		report := &DoctorReport{
			Ready: true,
			CPU: CPUReport{
				OS:                    testOSLinux,
				Arch:                  testArchAMD64,
				Level:                 "v3",
				Features:              []string{"avx", "avx2", "bmi2"},
				AVX512DownclockNotice: "not present (no downclock risk)",
			},
			Memfd: MemfdReport{
				Available: true,
				Status:    "Available",
				Kernel:    "Linux 6.8.0",
				Seccomp:   "Permitted",
			},
			Cache: CacheReport{
				Ready:        true,
				ResolvedPath: "/home/user/.cache/microfat",
				Permissions:  "0700",
				Writable:     true,
			},
			Cgroup: &CgroupReport{
				Detected:         true,
				Version:          cgroup.VersionV2,
				MemoryLimitBytes: 2147483648,
				CPUQuota:         4.0,
				GOMEMLIMITBytes:  1932735283,
				GOMEMLIMITStr:    "1932735283B",
				GOMAXPROCS:       4,
			},
			Toolchain: ToolchainReport{
				Version: "v0.2.0",
				Commit:  "abcdef1",
				Date:    "2026-08-27T00:00:00Z",
				BuiltBy: "test",
			},
			Summary: "Environment is fully ready for high-performance Microfat dispatch!",
		}

		var buf bytes.Buffer
		printDoctorReport(&buf, report)
		out := buf.String()

		if !strings.Contains(out, "[✔] Host CPU Microarchitecture") {
			t.Errorf("missing CPU success glyph in output:\n%s", out)
		}
		if !strings.Contains(out, "Container Resource Limits (cgroup v2)") {
			t.Errorf("missing cgroup v2 section in output:\n%s", out)
		}
	})

	t.Run("degraded with memfd restricted and warnings", func(t *testing.T) {
		report := &DoctorReport{
			Ready: true,
			CPU: CPUReport{
				OS:                    testOSLinux,
				Arch:                  testArchAMD64,
				Level:                 "v4",
				Features:              []string{"avx512f", "avx512dq"},
				AVX512DownclockRisk:   true,
				AVX512DownclockNotice: "Skylake-X / Cascade Lake detected",
			},
			Memfd: MemfdReport{
				Available: false,
				Status:    "Blocked by host seccomp/security profile",
				Kernel:    "Linux 6.8.0",
				Seccomp:   "Restricted (EPERM/EACCES)",
				Hint:      "memfd_create was blocked by host seccomp profile.",
			},
			Cache: CacheReport{
				Ready:        true,
				ResolvedPath: "/home/user/.cache/microfat",
				Permissions:  "0700",
				Writable:     true,
			},
			Cgroup: &CgroupReport{
				Detected: false,
				Version:  0,
			},
			Toolchain: ToolchainReport{
				Version: "v0.2.0",
				Commit:  "abcdef1",
			},
			Warnings: []string{"In-memory memfd_create is unavailable: Blocked by host seccomp/security profile"},
			Summary:  "Environment is ready with warnings for Microfat dispatch.",
		}

		var buf bytes.Buffer
		printDoctorReport(&buf, report)
		out := buf.String()

		if !strings.Contains(out, "[!] In-Memory Execution (memfd_create)") {
			t.Errorf("missing memfd warning glyph in output:\n%s", out)
		}
		if !strings.Contains(out, "Warnings:") {
			t.Errorf("missing Warnings section in output:\n%s", out)
		}
	})

	t.Run("failed execution with errors", func(t *testing.T) {
		report := &DoctorReport{
			Ready: false,
			CPU: CPUReport{
				OS:    testOSLinux,
				Arch:  testArchAMD64,
				Level: "",
			},
			Memfd: MemfdReport{
				Available: false,
				Status:    "Unsupported",
			},
			Cache: CacheReport{
				Ready:        false,
				ResolvedPath: "/read-only",
				Permissions:  "0555",
				Writable:     false,
				Error:        "permission denied",
				Hint:         "Ensure $TMPDIR is writable.",
			},
			Toolchain: ToolchainReport{
				Version: "v0.2.0",
			},
			Errors:  []string{"Disk cache is unavailable and memfd_create is unavailable"},
			Summary: "Environment is NOT ready for Microfat execution.",
		}

		var buf bytes.Buffer
		printDoctorReport(&buf, report)
		out := buf.String()

		if !strings.Contains(out, "[✖] Host CPU Microarchitecture") {
			t.Errorf("missing CPU failure glyph in output:\n%s", out)
		}
		if !strings.Contains(out, "[✖] Disk Cache Execution Fallback") {
			t.Errorf("missing Cache failure glyph in output:\n%s", out)
		}
		if !strings.Contains(out, "Errors:") {
			t.Errorf("missing Errors section in output:\n%s", out)
		}
	})
}

func TestProbeMemfdMocking(t *testing.T) {
	origMemfd := memfdProbeSyscall
	origFstat := memfdProbeFstat
	origFcntl := memfdProbeFcntl
	origClose := memfdProbeClose
	origUname := unameSyscall
	defer func() {
		memfdProbeSyscall = origMemfd
		memfdProbeFstat = origFstat
		memfdProbeFcntl = origFcntl
		memfdProbeClose = origClose
		unameSyscall = origUname
	}()

	t.Run("successful memfd create", func(t *testing.T) {
		memfdProbeSyscall = func(name string, flags int) (int, error) {
			return 42, nil
		}
		memfdProbeFstat = func(fd int, stat *unix.Stat_t) error {
			stat.Mode = unix.S_IFREG | 0o700
			return nil
		}
		memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) {
			if cmd == unix.F_GET_SEALS {
				return memfd.TargetSeals, nil
			}
			return 0, nil
		}
		memfdProbeClose = func(fd int) error {
			return nil
		}
		unameSyscall = func(buf *unix.Utsname) error {
			copy(buf.Release[:], []byte("6.10.0-custom\x00"))
			return nil
		}

		rep := probeMemfd()
		if !rep.Available || rep.Status != "Available" {
			t.Errorf("expected available memfd probe, got: %+v", rep)
		}
		if !strings.Contains(rep.Kernel, "6.10.0-custom") {
			t.Errorf("expected kernel release string, got %s", rep.Kernel)
		}
		if rep.Mode == nil || !rep.Mode.IsExecutable {
			t.Errorf("expected executable mode in probe result, got: %+v", rep.Mode)
		}
		if rep.Seals == nil || !rep.Seals.Matches {
			t.Errorf("expected matching seals in probe result, got: %+v", rep.Seals)
		}
		if rep.Execution != "not_tested" {
			t.Errorf("expected not_tested execution status, got: %s", rep.Execution)
		}
		if rep.Seccomp != "Unknown (not independently tested)" {
			t.Errorf("expected Unknown (not independently tested) seccomp status, got: %s", rep.Seccomp)
		}
	})

	t.Run("creation allowed sealing denied", func(t *testing.T) {
		memfdProbeSyscall = func(name string, flags int) (int, error) {
			return 42, nil
		}
		memfdProbeFstat = func(fd int, stat *unix.Stat_t) error {
			stat.Mode = unix.S_IFREG | 0o700
			return nil
		}
		memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) {
			if cmd == unix.F_ADD_SEALS {
				return -1, syscall.EPERM
			}
			return 0, nil
		}
		memfdProbeClose = func(fd int) error {
			return nil
		}

		rep := probeMemfd()
		if rep.Available {
			t.Errorf("expected unavailable memfd probe when sealing is denied")
		}
		if !strings.Contains(rep.Status, "Seal application denied (F_ADD_SEALS failed)") {
			t.Errorf("expected Seal application denied status, got: %s", rep.Status)
		}
		if rep.Operation != "add_seals" {
			t.Errorf("expected add_seals operation, got: %s", rep.Operation)
		}
		if rep.Seccomp != "Unknown (not independently tested)" {
			t.Errorf("expected Unknown (not independently tested) seccomp status, got: %s", rep.Seccomp)
		}
		if rep.Seals == nil || rep.Seals.Supported {
			t.Errorf("expected seals to be reported as unsupported/blocked, got: %+v", rep.Seals)
		}
	})

	t.Run("creation allowed non-executable descriptor", func(t *testing.T) {
		memfdProbeSyscall = func(name string, flags int) (int, error) {
			return 42, nil
		}
		memfdProbeFstat = func(fd int, stat *unix.Stat_t) error {
			stat.Mode = unix.S_IFREG | 0o600 // lacking execute permissions
			return nil
		}
		memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) {
			if cmd == unix.F_GET_SEALS {
				return memfd.TargetSeals, nil
			}
			return 0, nil
		}
		memfdProbeClose = func(fd int) error {
			return nil
		}

		rep := probeMemfd()
		if rep.Available {
			t.Errorf("expected unavailable memfd probe when descriptor lacks execute permissions")
		}
		if !strings.Contains(rep.Status, "Descriptor non-executable") {
			t.Errorf("expected Descriptor non-executable status, got: %s", rep.Status)
		}
		if rep.Operation != "mode" {
			t.Errorf("expected mode operation, got: %s", rep.Operation)
		}
		if rep.Mode == nil || rep.Mode.IsExecutable {
			t.Errorf("expected Mode.IsExecutable == false, got: %+v", rep.Mode)
		}
	})

	t.Run("memfd blocked with EPERM", func(t *testing.T) {
		memfdProbeSyscall = func(name string, flags int) (int, error) {
			return -1, syscall.EPERM
		}
		unameSyscall = func(buf *unix.Utsname) error {
			return errors.New("uname error")
		}

		rep := probeMemfd()
		if rep.Available {
			t.Errorf("expected unavailable memfd probe on EPERM")
		}
		if !strings.Contains(rep.Status, "Creation denied") {
			t.Errorf("expected Creation denied status on EPERM, got %s", rep.Status)
		}
		if rep.Operation != "create" {
			t.Errorf("expected create operation, got %s", rep.Operation)
		}
		if rep.ErrnoName != "EPERM" || rep.ErrnoValue != int(syscall.EPERM) {
			t.Errorf("expected EPERM errno, got %s (%d)", rep.ErrnoName, rep.ErrnoValue)
		}
		if rep.Seccomp != "Unknown (not independently tested)" {
			t.Errorf("expected Unknown (not independently tested) seccomp status, got %s", rep.Seccomp)
		}
	})

	t.Run("memfd unsupported with ENOSYS", func(t *testing.T) {
		memfdProbeSyscall = func(name string, flags int) (int, error) {
			return -1, syscall.ENOSYS
		}

		rep := probeMemfd()
		if rep.Available {
			t.Errorf("expected unavailable memfd probe on ENOSYS")
		}
		if !strings.Contains(rep.Status, "Creation unsupported") {
			t.Errorf("expected Creation unsupported status on ENOSYS, got %s", rep.Status)
		}
		if rep.Operation != "create" {
			t.Errorf("expected create operation, got %s", rep.Operation)
		}
		if rep.ErrnoName != "ENOSYS" {
			t.Errorf("expected ENOSYS errno, got %s", rep.ErrnoName)
		}
	})

	t.Run("memfd generic error", func(t *testing.T) {
		memfdProbeSyscall = func(name string, flags int) (int, error) {
			return -1, errors.New("generic syscall error")
		}

		rep := probeMemfd()
		if rep.Available {
			t.Errorf("expected unavailable memfd probe on generic error")
		}
	})
}

func TestProbeCgroupMocking(t *testing.T) {
	origCgroup := readCgroupLimitsFunc
	defer func() {
		readCgroupLimitsFunc = origCgroup
	}()

	t.Run("cgroup v2 limits detected", func(t *testing.T) {
		readCgroupLimitsFunc = func() (cgroup.Limits, error) {
			return cgroup.Limits{
				CgroupVersion:    cgroup.VersionV2,
				MemoryLimitBytes: 1024 * 1024 * 1024,
				CPUQuota:         2.5,
				CPUs:             2,
			}, nil
		}

		rep := probeCgroup()
		if !rep.Detected || rep.Version != cgroup.VersionV2 {
			t.Errorf("expected detected cgroup v2, got: %+v", rep)
		}
		if rep.GOMAXPROCS != 2 {
			t.Errorf("expected GOMAXPROCS 2, got %d", rep.GOMAXPROCS)
		}
		if rep.GOMEMLIMITStr == "" {
			t.Errorf("expected non-empty GOMEMLIMITStr")
		}
	})

	t.Run("cgroup read error or unknown version", func(t *testing.T) {
		readCgroupLimitsFunc = func() (cgroup.Limits, error) {
			return cgroup.Limits{CgroupVersion: cgroup.VersionUnknown}, errors.New("cgroup unreadable")
		}

		rep := probeCgroup()
		if rep.Detected || rep.Version != 0 {
			t.Errorf("expected undetected cgroup on error, got: %+v", rep)
		}
	})
}

func TestRunDoctorEdgeCases(t *testing.T) {
	tempDir := t.TempDir()
	validCacheDir := filepath.Join(tempDir, "doctor_edge_cache")

	origDetect := microarchDetectFunc
	origDownclock := isAVX512DownclockingRiskFunc
	origMemfd := memfdProbeSyscall
	origTemp := createTempFileFunc
	defer func() {
		microarchDetectFunc = origDetect
		isAVX512DownclockingRiskFunc = origDownclock
		memfdProbeSyscall = origMemfd
		createTempFileFunc = origTemp
	}()

	t.Run("non-strict with valid cache", func(t *testing.T) {
		rep := runDoctor(DoctorOptions{
			CacheDir: validCacheDir,
			Strict:   false,
		})
		if !rep.Ready {
			t.Errorf("expected report to be ready: %+v", rep)
		}
		if rep.Summary == "" {
			t.Errorf("expected summary in report")
		}
	})

	t.Run("downclock risk true on AMD64", func(t *testing.T) {
		isAVX512DownclockingRiskFunc = func() bool { return true }
		rep := runDoctor(DoctorOptions{CacheDir: validCacheDir})
		if !rep.CPU.AVX512DownclockRisk {
			t.Errorf("expected downclock risk true")
		}
		if !strings.Contains(rep.CPU.AVX512DownclockNotice, "Skylake-X") {
			t.Errorf("expected Skylake-X in notice, got %s", rep.CPU.AVX512DownclockNotice)
		}
		isAVX512DownclockingRiskFunc = origDownclock
	})

	t.Run("ARM64 non-AMD64 notice", func(t *testing.T) {
		microarchDetectFunc = func() microarch.Info {
			return microarch.Info{OS: testOSLinux, Arch: testArchARM64, Level: "v8.2", Features: []string{"neon", "aes"}}
		}
		isAVX512DownclockingRiskFunc = func() bool { return false }
		rep := runDoctor(DoctorOptions{CacheDir: validCacheDir})
		if !strings.Contains(rep.CPU.AVX512DownclockNotice, "N/A") {
			t.Errorf("expected N/A notice on ARM64, got %s", rep.CPU.AVX512DownclockNotice)
		}
		microarchDetectFunc = origDetect
	})

	t.Run("undetected host level", func(t *testing.T) {
		microarchDetectFunc = func() microarch.Info {
			return microarch.Info{OS: testOSLinux, Arch: "riscv64", Level: "", Features: nil}
		}
		rep := runDoctor(DoctorOptions{CacheDir: validCacheDir})
		if rep.Ready {
			t.Errorf("expected not ready when level is empty")
		}
		microarchDetectFunc = origDetect
	})

	t.Run("both memfd and cache fail", func(t *testing.T) {
		memfdProbeSyscall = func(name string, flags int) (int, error) {
			return -1, syscall.EPERM
		}
		rep := runDoctor(DoctorOptions{CacheDir: "/dev/null/unwritable"})
		if rep.Ready {
			t.Errorf("expected not ready when both memfd and cache fail")
		}
		if len(rep.Errors) == 0 {
			t.Errorf("expected errors in report when both fail")
		}
		memfdProbeSyscall = origMemfd
	})

	t.Run("strict mode fails when memfd is blocked", func(t *testing.T) {
		memfdProbeSyscall = func(name string, flags int) (int, error) {
			return -1, syscall.EPERM
		}
		rep := runDoctor(DoctorOptions{CacheDir: validCacheDir, Strict: true})
		if rep.Ready {
			t.Errorf("expected strict mode to fail when memfd is blocked")
		}
		memfdProbeSyscall = origMemfd
	})

	t.Run("createTempFile error in probeCache", func(t *testing.T) {
		createTempFileFunc = func(dir, pattern string) (*os.File, error) {
			return nil, errors.New("mock temp creation error")
		}
		res := probeCache(validCacheDir)
		if res.Ready || res.Writable {
			t.Errorf("expected probeCache to fail when createTempFile fails")
		}
		createTempFileFunc = origTemp
	})
}

func TestRunDoctorTruthTable(t *testing.T) {
	tempDir := t.TempDir()
	validCacheDir := filepath.Join(tempDir, "tt_cache_valid")
	invalidCacheDir := "/dev/null/forbidden_cache_path"

	origDetect := microarchDetectFunc
	origMemfd := memfdProbeSyscall
	origFstat := memfdProbeFstat
	origFcntl := memfdProbeFcntl
	origClose := memfdProbeClose
	defer func() {
		microarchDetectFunc = origDetect
		memfdProbeSyscall = origMemfd
		memfdProbeFstat = origFstat
		memfdProbeFcntl = origFcntl
		memfdProbeClose = origClose
	}()

	mockMemfdSuccess := func() {
		memfdProbeSyscall = func(name string, flags int) (int, error) { return 42, nil }
		memfdProbeFstat = func(fd int, stat *unix.Stat_t) error { stat.Mode = unix.S_IFREG | 0o700; return nil }
		memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) {
			if cmd == unix.F_GET_SEALS {
				return memfd.TargetSeals, nil
			}
			return 0, nil
		}
		memfdProbeClose = func(fd int) error { return nil }
	}

	mockMemfdBlocked := func() {
		memfdProbeSyscall = func(name string, flags int) (int, error) { return -1, syscall.EPERM }
	}

	mockMemfdFstatFailureWithSeals := func() {
		memfdProbeSyscall = func(name string, flags int) (int, error) { return 42, nil }
		memfdProbeFstat = func(fd int, stat *unix.Stat_t) error { return syscall.EBADF }
		memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) {
			if cmd == unix.F_GET_SEALS {
				return memfd.TargetSeals, nil
			}
			return 0, nil
		}
		memfdProbeClose = func(fd int) error { return nil }
	}

	tests := []struct {
		name         string
		cpuLevel     string
		memfdMock    func()
		cacheDir     string
		strict       bool
		expectReady  bool
		expectErrors bool
	}{
		{
			name:         "normal: CPU OK, Memfd OK, Cache OK -> Ready",
			cpuLevel:     "v3",
			memfdMock:    mockMemfdSuccess,
			cacheDir:     validCacheDir,
			strict:       false,
			expectReady:  true,
			expectErrors: false,
		},
		{
			name:         "strict: CPU OK, Memfd OK, Cache OK -> Ready",
			cpuLevel:     "v3",
			memfdMock:    mockMemfdSuccess,
			cacheDir:     validCacheDir,
			strict:       true,
			expectReady:  true,
			expectErrors: false,
		},
		{
			name:         "normal: CPU OK, Memfd blocked, Cache OK -> Ready with warning",
			cpuLevel:     "v3",
			memfdMock:    mockMemfdBlocked,
			cacheDir:     validCacheDir,
			strict:       false,
			expectReady:  true,
			expectErrors: false,
		},
		{
			name:         "strict: CPU OK, Memfd blocked, Cache OK -> Fails",
			cpuLevel:     "v3",
			memfdMock:    mockMemfdBlocked,
			cacheDir:     validCacheDir,
			strict:       true,
			expectReady:  false,
			expectErrors: true,
		},
		{
			name:         "normal: CPU OK, Memfd OK, Cache invalid -> Ready with warning",
			cpuLevel:     "v3",
			memfdMock:    mockMemfdSuccess,
			cacheDir:     invalidCacheDir,
			strict:       false,
			expectReady:  true,
			expectErrors: false,
		},
		{
			name:         "strict: CPU OK, Memfd OK, Cache invalid -> Fails",
			cpuLevel:     "v3",
			memfdMock:    mockMemfdSuccess,
			cacheDir:     invalidCacheDir,
			strict:       true,
			expectReady:  false,
			expectErrors: true,
		},
		{
			name:         "normal: CPU OK, Memfd blocked, Cache invalid -> Fails",
			cpuLevel:     "v3",
			memfdMock:    mockMemfdBlocked,
			cacheDir:     invalidCacheDir,
			strict:       false,
			expectReady:  false,
			expectErrors: true,
		},
		{
			name:         "normal: CPU empty level, Memfd OK, Cache OK -> Fails",
			cpuLevel:     "",
			memfdMock:    mockMemfdSuccess,
			cacheDir:     validCacheDir,
			strict:       false,
			expectReady:  false,
			expectErrors: true,
		},
		{
			name:         "regression: CPU OK, Memfd mode error + seals OK, Cache OK, Normal -> Ready with warning",
			cpuLevel:     "v3",
			memfdMock:    mockMemfdFstatFailureWithSeals,
			cacheDir:     validCacheDir,
			strict:       false,
			expectReady:  true,
			expectErrors: false,
		},
		{
			name:         "regression: CPU OK, Memfd mode error + seals OK, Cache OK, Strict -> Fails",
			cpuLevel:     "v3",
			memfdMock:    mockMemfdFstatFailureWithSeals,
			cacheDir:     validCacheDir,
			strict:       true,
			expectReady:  false,
			expectErrors: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			microarchDetectFunc = func() microarch.Info {
				return microarch.Info{
					OS:       testOSLinux,
					Arch:     testArchAMD64,
					Level:    tc.cpuLevel,
					Features: []string{"avx2"},
				}
			}
			tc.memfdMock()

			rep := runDoctor(DoctorOptions{
				CacheDir: tc.cacheDir,
				Strict:   tc.strict,
			})

			if rep.Ready != tc.expectReady {
				t.Errorf("expected Ready=%t, got Ready=%t (errors=%v)", tc.expectReady, rep.Ready, rep.Errors)
			}
			if tc.expectErrors && len(rep.Errors) == 0 {
				t.Errorf("expected errors in report, got none")
			}
			if !tc.expectErrors && len(rep.Errors) > 0 {
				t.Errorf("expected no errors in report, got: %v", rep.Errors)
			}
			if rep.Execution.Status != "not_tested" || rep.Execution.Reason != "not_tested" {
				t.Errorf("expected Execution {not_tested, not_tested}, got %+v", rep.Execution)
			}
		})
	}
}

type errWriter struct {
	failAfter int
	written   int
}

func (ew *errWriter) Write(p []byte) (n int, err error) {
	if ew.written >= ew.failAfter {
		return 0, errors.New("simulated write error")
	}
	ew.written += len(p)
	return len(p), nil
}

func TestPrintDoctorReport_WriterError(t *testing.T) {
	report := &DoctorReport{
		Ready: true,
		CPU: CPUReport{
			OS:    testOSLinux,
			Arch:  testArchAMD64,
			Level: "v3",
		},
		Memfd: MemfdReport{
			Available:        true,
			Status:           "Available",
			CreationStrategy: "MFD_EXEC",
			Mode: &memfd.ModeObservation{
				ModeOctal:    "0700",
				IsExecutable: true,
			},
			Seals: &memfd.SealsObservation{
				Supported: true,
				Matches:   true,
			},
			Execution: "unknown (prerequisite check only)",
		},
		Cache: CacheReport{
			Ready:        true,
			ResolvedPath: "/tmp/cache",
			Permissions:  "0700",
			Writable:     true,
			Execution:    "unknown (write-only probe)",
		},
		Summary: "Ready",
	}

	ew := &errWriter{failAfter: 0}
	err := printDoctorReport(ew, report)
	if err == nil {
		t.Errorf("expected printDoctorReport to fail when writer fails")
	}
}

func TestPrintSections_ComprehensiveWriterErrors(t *testing.T) {
	report := &DoctorReport{
		Ready: false,
		CPU: CPUReport{
			OS:                    testOSLinux,
			Arch:                  testArchAMD64,
			Level:                 "v3",
			Features:              []string{"avx2"},
			AVX512DownclockNotice: "not present",
		},
		Memfd: MemfdReport{
			Available:             false,
			Passed:                false,
			Phase:                 "fstat",
			Status:                "Failed",
			Kernel:                "Linux 6.8.0",
			CreationStrategy:      "MFD_EXEC",
			ErrnoName:             "EBADF",
			ErrnoValue:            9,
			CandidateExplanations: []string{"bad descriptor"},
			Hint:                  "check kernel",
		},
		Cache: CacheReport{
			Ready:                 false,
			Passed:                false,
			Phase:                 "write",
			ResolvedPath:          "/tmp/cache",
			Permissions:           "0700",
			Writable:              false,
			ErrnoName:             "EACCES",
			ErrnoValue:            13,
			CandidateExplanations: []string{"permission denied"},
			Error:                 "write error",
			Hint:                  "check permissions",
		},
		Cgroup: &CgroupReport{
			Detected:         true,
			Version:          2,
			MemoryLimitBytes: 1024,
			MemoryHighBytes:  512,
			CPUQuota:         2.0,
			GOMEMLIMITStr:    "1024B",
			GOMAXPROCS:       2,
		},
		Toolchain: ToolchainReport{
			Version: "v1.0.0",
			Commit:  "abc",
			Date:    "2026-09-25",
		},
		Warnings: []string{"warning 1"},
		Errors:   []string{"error 1"},
		Summary:  "Not ready",
	}

	for i := 0; i < 50; i++ {
		ew := &errWriter{failAfter: i}
		_ = printDoctorReport(ew, report)
	}
}

func TestProbeCache_FailureBranches(t *testing.T) {
	tempDir := t.TempDir()
	validCacheDir := filepath.Join(tempDir, "probe_cache_branch")
	_ = os.MkdirAll(validCacheDir, 0o700)

	origStat := statCacheFunc
	origTemp := createTempFileFunc
	origRead := readFileFunc
	defer func() {
		statCacheFunc = origStat
		createTempFileFunc = origTemp
		readFileFunc = origRead
	}()

	t.Run("stat failure", func(t *testing.T) {
		statCacheFunc = func(name string) (os.FileInfo, error) {
			return nil, syscall.EACCES
		}
		res := probeCache(validCacheDir)
		if res.Passed || res.Phase != "stat" || res.ErrnoName != "EACCES" {
			t.Errorf("expected stat failure, got: %+v", res)
		}
		statCacheFunc = origStat
	})

	t.Run("write probe file error", func(t *testing.T) {
		createTempFileFunc = func(dir, pattern string) (*os.File, error) {
			f, err := origTemp(dir, pattern)
			if err != nil {
				return nil, err
			}
			_ = f.Close()
			return f, nil
		}
		res := probeCache(validCacheDir)
		if res.Passed || res.Phase != "write" {
			t.Errorf("expected write failure, got: %+v", res)
		}
		createTempFileFunc = origTemp
	})

	t.Run("read probe file error", func(t *testing.T) {
		readFileFunc = func(name string) ([]byte, error) {
			return nil, syscall.EIO
		}
		res := probeCache(validCacheDir)
		if res.Passed || res.Phase != "read" || res.ErrnoName != "EIO" {
			t.Errorf("expected read failure, got: %+v", res)
		}
		readFileFunc = origRead
	})

	t.Run("read probe file content mismatch", func(t *testing.T) {
		readFileFunc = func(name string) ([]byte, error) {
			return []byte("corrupted"), nil
		}
		res := probeCache(validCacheDir)
		if res.Passed || res.Phase != "read" || !strings.Contains(res.Error, "verifying written probe") {
			t.Errorf("expected payload mismatch failure, got: %+v", res)
		}
		readFileFunc = origRead
	})
}

func TestDoctorCmd_WriterErrors(t *testing.T) {
	cmd := newDoctorCmd()
	cmd.SetArgs([]string{flagJSON})
	ew := &errWriter{failAfter: 0}
	cmd.SetOut(ew)
	if err := cmd.Execute(); err == nil {
		t.Errorf("expected error when JSON writer fails")
	}

	cmd = newDoctorCmd()
	cmd.SetArgs([]string{})
	cmd.SetOut(ew)
	if err := cmd.Execute(); err == nil {
		t.Errorf("expected error when plain text writer fails")
	}
}

func TestProbeMemfd_AdditionalBranches(t *testing.T) {
	origMemfd := memfdProbeSyscall
	origFstat := memfdProbeFstat
	origFcntl := memfdProbeFcntl
	origClose := memfdProbeClose
	origUname := unameSyscall
	defer func() {
		memfdProbeSyscall = origMemfd
		memfdProbeFstat = origFstat
		memfdProbeFcntl = origFcntl
		memfdProbeClose = origClose
		unameSyscall = origUname
	}()

	t.Run("uname failure release string", func(t *testing.T) {
		unameSyscall = func(buf *unix.Utsname) error {
			return errors.New("uname failed")
		}
		rep := probeMemfd()
		if rep.Kernel != "Linux (unknown release)" {
			t.Errorf("expected Linux (unknown release), got: %s", rep.Kernel)
		}
	})

	t.Run("fstat failure in probeMemfd", func(t *testing.T) {
		memfdProbeSyscall = func(name string, flags int) (int, error) { return 10, nil }
		memfdProbeFstat = func(fd int, stat *unix.Stat_t) error { return syscall.EBADF }
		memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) { return 0, nil }
		memfdProbeClose = func(fd int) error { return nil }

		rep := probeMemfd()
		if rep.Passed || rep.Phase != "fstat" {
			t.Errorf("expected fstat failure phase, got: %+v", rep)
		}
		if rep.Hint == "" {
			t.Errorf("expected hint for fstat failure")
		}
	})

	t.Run("seals failure with non-perm errno", func(t *testing.T) {
		memfdProbeSyscall = func(name string, flags int) (int, error) { return 10, nil }
		memfdProbeFstat = func(fd int, stat *unix.Stat_t) error { stat.Mode = unix.S_IFREG | 0o700; return nil }
		memfdProbeFcntl = func(fd uintptr, cmd int, arg int) (int, error) {
			if cmd == unix.F_ADD_SEALS {
				return -1, syscall.EINVAL
			}
			return 0, nil
		}
		memfdProbeClose = func(fd int) error { return nil }

		rep := probeMemfd()
		if rep.Passed || rep.Phase != "seals" {
			t.Errorf("expected seals failure phase, got: %+v", rep)
		}
		if rep.Operation != "add_seals" {
			t.Errorf("expected add_seals operation, got: %s", rep.Operation)
		}
		if rep.Seccomp != "Unknown (not independently tested)" {
			t.Errorf("expected Unknown (not independently tested), got: %s", rep.Seccomp)
		}
	})
}

func TestPrintSections_AllFormattingBranches(t *testing.T) {
	rep := &DoctorReport{
		Ready: false,
		CPU: CPUReport{
			OS:                    testOSLinux,
			Arch:                  testArchAMD64,
			Level:                 "", // triggers failure glyph
			Features:              nil,
			AVX512DownclockNotice: "",
		},
		Memfd: MemfdReport{
			Passed:                false,
			Kernel:                "", // empty kernel branch
			Status:                "Disabled",
			CreationStrategy:      "standard",
			Mode:                  &memfd.ModeObservation{ModeOctal: "0600", IsExecutable: false},
			Seals:                 &memfd.SealsObservation{Supported: false, Error: "not supported"},
			Seccomp:               "Restricted",
			Execution:             "unavailable",
			Phase:                 "creation",
			ErrnoName:             "ENOSYS",
			ErrnoValue:            38,
			CandidateExplanations: []string{"syscall missing"},
			Hint:                  "enable kernel option",
		},
		Cache: CacheReport{
			Passed:                false,
			ResolvedPath:          "/cache/dir",
			Permissions:           "0444",
			Writable:              false,
			Execution:             "untested",
			Phase:                 "write",
			ErrnoName:             "EROFS",
			ErrnoValue:            30,
			CandidateExplanations: []string{"read-only filesystem"},
			Error:                 "read-only",
			Hint:                  "remount rw",
		},
		Cgroup: &CgroupReport{
			Detected:          true,
			Version:           2,
			MemoryLimitBytes:  0, // unlimited branch
			MemoryHighBytes:   1024,
			CPUQuota:          0, // unlimited branch
			GOMEMLIMITStr:     "1024B",
			ConstrainingLimit: "memory.max",
			GOMAXPROCS:        4,
		},
		Toolchain: ToolchainReport{
			Version: "v1.0.0",
			Commit:  "abcdef",
			Date:    "2026-09-25",
		},
		Warnings: []string{"w1"},
		Errors:   []string{"e1"},
		Summary:  "Failure",
	}

	var buf bytes.Buffer
	if err := printDoctorReport(&buf, rep); err != nil {
		t.Fatalf("unexpected print error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "unlimited") {
		t.Errorf("expected unlimited in cgroup output: %s", out)
	}
}

func TestDoctorCmdFailingNonZeroExit(t *testing.T) {
	cmd := newDoctorCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	invalidDir := "/dev/null/forbidden_cache_path"
	cmd.SetArgs([]string{flagJSON, flagCacheDir, invalidDir, flagStrict})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected doctor command to return error on failing prerequisites")
	}

	var report DoctorReport
	if unmarshalErr := json.Unmarshal(buf.Bytes(), &report); unmarshalErr != nil {
		t.Fatalf("failed to parse JSON from failing doctor run: %v\nOutput: %s", unmarshalErr, buf.String())
	}
	if report.Ready {
		t.Errorf("expected report.Ready == false")
	}
	if report.Policy != "strict" {
		t.Errorf("expected report.Policy == strict, got %s", report.Policy)
	}
	if report.Scope != "prerequisites" {
		t.Errorf("expected report.Scope == prerequisites, got %s", report.Scope)
	}
}
