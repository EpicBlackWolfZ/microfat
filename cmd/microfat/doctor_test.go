package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
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
		cmd.SetArgs([]string{"--cache-dir", validCacheDir})

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
		cmd.SetArgs([]string{"--json", "--cache-dir", validCacheDir})

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
		cmd.SetArgs([]string{"--strict", "--cache-dir", validCacheDir})

		_ = cmd.Execute()
	})

	t.Run("invalid cache directory triggers error", func(t *testing.T) {
		cmd := newDoctorCmd()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		invalidDir := "/dev/null/forbidden_cache_path"
		cmd.SetArgs([]string{"--cache-dir", invalidDir, "--strict"})

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
	origUname := unameSyscall
	defer func() {
		memfdProbeSyscall = origMemfd
		unameSyscall = origUname
	}()

	t.Run("successful memfd create", func(t *testing.T) {
		memfdProbeSyscall = func(name string, flags int) (int, error) {
			return 42, nil
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
		if !strings.Contains(rep.Status, "seccomp") {
			t.Errorf("expected seccomp status on EPERM, got %s", rep.Status)
		}
		if rep.Seccomp != "Restricted (EPERM/EACCES)" {
			t.Errorf("expected Restricted seccomp status, got %s", rep.Seccomp)
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
		if !strings.Contains(rep.Status, "Unsupported") {
			t.Errorf("expected Unsupported status on ENOSYS, got %s", rep.Status)
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

