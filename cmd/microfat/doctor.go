package main

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/memfd"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/version"
	"github.com/spf13/cobra"
)

const (
	bytesInKiB = 1024
	bytesInMiB = 1024 * 1024
	bytesInGiB = 1024 * 1024 * 1024
	bytesInTiB = int64(1024) * 1024 * 1024 * 1024

	glyphSuccess = "[✔]"
	glyphWarning = "[!]"
	glyphFailure = "[✖]"
	glyphInfo    = "[-]"

	executionUnavailable = "unavailable"
)

var (
	microarchDetectFunc          = microarch.Detect
	isAVX512DownclockingRiskFunc = microarch.IsAVX512DownclockingRisk
	createTempFileFunc           = os.CreateTemp
)

// DoctorOptions contains user-specified flags for the doctor command.
type DoctorOptions struct {
	JSONOutput bool
	CacheDir   string
	Strict     bool
	Out        io.Writer
}

// DoctorReport contains full diagnostic environment telemetry.
type DoctorReport struct {
	Ready     bool            `json:"ready"`
	CPU       CPUReport       `json:"cpu"`
	Memfd     MemfdReport     `json:"memfd"`
	Cache     CacheReport     `json:"cache"`
	Execution ExecutionReport `json:"execution"`
	Cgroup    *CgroupReport   `json:"cgroup,omitempty"`
	Toolchain ToolchainReport `json:"toolchain"`
	Summary   string          `json:"summary"`
	Warnings  []string        `json:"warnings,omitempty"`
	Errors    []string        `json:"errors,omitempty"`
}

// ExecutionReport clarifies execution status vs prerequisite verification.
type ExecutionReport struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
	Notice string `json:"notice"`
}

// CPUReport contains CPU microarchitecture level and vector capability details.
type CPUReport struct {
	OS                    string                       `json:"os"`
	Arch                  string                       `json:"arch"`
	Level                 string                       `json:"level"`
	Features              []string                     `json:"features"`
	Levels                []microarch.ARM64LevelStatus `json:"levels,omitempty"`
	AVX512DownclockRisk   bool                         `json:"avx512_downclock_risk,omitempty"`
	AVX512DownclockNotice string                       `json:"avx512_downclock_notice,omitempty"`
}

// MemfdReport contains in-memory anonymous file descriptor capability details.
type MemfdReport struct {
	Available        bool                    `json:"available"`
	Status           string                  `json:"status"`
	Kernel           string                  `json:"kernel,omitempty"`
	Seccomp          string                  `json:"seccomp,omitempty"`
	CreationStrategy string                  `json:"creation_strategy,omitempty"`
	Mode             *memfd.ModeObservation  `json:"mode,omitempty"`
	Seals            *memfd.SealsObservation `json:"seals,omitempty"`
	Execution        string                  `json:"execution,omitempty"`
	Error            string                  `json:"error,omitempty"`
	Hint             string                  `json:"hint,omitempty"`
}

// CacheReport contains disk cache directory status and write permission details.
type CacheReport struct {
	Ready        bool   `json:"ready"`
	ResolvedPath string `json:"path"`
	Permissions  string `json:"permissions"`
	Writable     bool   `json:"writable"`
	Execution    string `json:"execution,omitempty"`
	Error        string `json:"error,omitempty"`
	Hint         string `json:"hint,omitempty"`
}

// CgroupReport contains Linux container resource limits and computed Go tuning parameters.
type CgroupReport struct {
	Detected                  bool    `json:"detected"`
	Version                   int     `json:"version"`
	MemoryLimitBytes          int64   `json:"memory_limit_bytes"`
	MemoryHighBytes           int64   `json:"memory_high_bytes,omitempty"`
	EffectiveMemoryLimitBytes int64   `json:"effective_memory_limit_bytes,omitempty"`
	ConstrainingLimit         string  `json:"constraining_limit,omitempty"`
	CPUQuota                  float64 `json:"cpu_quota"`
	GOMEMLIMITBytes           int64   `json:"gomemlimit_bytes,omitempty"`
	GOMEMLIMITStr             string  `json:"gomemlimit_str,omitempty"`
	GOMAXPROCS                int     `json:"gomaxprocs,omitempty"`
}

// ToolchainReport contains binary build metadata.
type ToolchainReport struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	BuiltBy string `json:"built_by"`
}

func newDoctorCmd() *cobra.Command {
	var opts DoctorOptions

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Verify host CPU, memfd_create, disk cache, and container cgroup environment readiness",
		Long: `doctor inspects the local runtime environment to verify host CPU microarchitecture level,
in-memory anonymous execution (memfd_create), disk cache fallback permissions, and container cgroup limits.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.Out == nil {
				opts.Out = cmd.OutOrStdout()
			}

			report := runDoctor(opts)

			if opts.JSONOutput {
				if err := json.MarshalWrite(opts.Out, report, jsontext.WithIndent("  ")); err != nil {
					return fmt.Errorf("encoding json: %w", err)
				}
				if _, err := fmt.Fprintln(opts.Out); err != nil {
					return fmt.Errorf("writing newline: %w", err)
				}
			} else {
				if err := printDoctorReport(opts.Out, report); err != nil {
					return fmt.Errorf("printing doctor report: %w", err)
				}
			}

			if !report.Ready {
				return errors.New("environment verification failed")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&opts.JSONOutput, "json", false, "Output diagnostic results in JSON format")
	cmd.Flags().StringVar(&opts.CacheDir, "cache-dir", "", "Custom cache directory to verify")
	cmd.Flags().BoolVar(&opts.Strict, "strict", false, "Strict verification (fails if in-memory memfd is unavailable)")

	return cmd
}

func runDoctor(opts DoctorOptions) *DoctorReport {
	hostInfo := microarchDetectFunc()
	downclockRisk := isAVX512DownclockingRiskFunc()

	cpuRep := CPUReport{
		OS:                  hostInfo.OS,
		Arch:                hostInfo.Arch,
		Level:               hostInfo.Level,
		Features:            hostInfo.Features,
		Levels:              hostInfo.Levels,
		AVX512DownclockRisk: downclockRisk,
	}

	switch {
	case downclockRisk:
		cpuRep.AVX512DownclockNotice = "Skylake-X / Cascade Lake detected (AVX-512 downclock risk; safe_avx512 policy recommended)"
	case strings.EqualFold(hostInfo.Arch, microarch.ArchAMD64):
		cpuRep.AVX512DownclockNotice = "not present (no downclock risk)"
	default:
		cpuRep.AVX512DownclockNotice = "N/A (non-AMD64 architecture)"
	}

	memfdRep := probeMemfd()
	cacheRep := probeCache(opts.CacheDir)
	cgroupRep := probeCgroup()

	toolchainRep := ToolchainReport{
		Version: version.Version,
		Commit:  version.Commit,
		Date:    version.Date,
		BuiltBy: version.BuiltBy,
	}

	var warnings []string
	var errs []string

	if !memfdRep.Available {
		if runtime.GOOS == "linux" {
			warnings = append(warnings, fmt.Sprintf("In-memory memfd_create is unavailable: %s", memfdRep.Status))
		}
	}

	if downclockRisk {
		warnings = append(warnings, "Host CPU is subject to AVX-512 downclocking; consider MICROFAT_POLICY=safe_avx512")
	}

	if !cacheRep.Ready {
		if !memfdRep.Available {
			errs = append(errs, fmt.Sprintf("Disk cache is unavailable (%s) and memfd_create is unavailable", cacheRep.Error))
		} else {
			warnings = append(warnings, fmt.Sprintf("Disk cache is unavailable: %s (fallback will fail if memfd is restricted)", cacheRep.Error))
		}
	}

	if hostInfo.Level == "" {
		errs = append(errs, fmt.Sprintf("Host CPU level could not be detected for OS %q, arch %q", hostInfo.OS, hostInfo.Arch))
	}

	var ready bool
	if opts.Strict {
		ready = memfdRep.Available && cacheRep.Ready && hostInfo.Level != ""
		if !ready && len(errs) == 0 {
			errs = append(errs, "Strict mode requirement failed: in-memory memfd_create and disk cache must both be available")
		}
	} else {
		ready = (memfdRep.Available || cacheRep.Ready) && hostInfo.Level != ""
	}

	summary := "Environment is fully ready for high-performance Microfat dispatch!"
	if !ready {
		summary = "Environment is NOT ready for Microfat execution. Please resolve the errors above."
	} else if len(warnings) > 0 {
		summary = "Environment is ready with warnings for Microfat dispatch."
	}

	return &DoctorReport{
		Ready: ready,
		CPU:   cpuRep,
		Memfd: memfdRep,
		Cache: cacheRep,
		Execution: ExecutionReport{
			Status: "unknown",
			Reason: "not_tested",
			Notice: "Doctor inspects environment prerequisites; runtime process execution is not tested during doctor probes.",
		},
		Cgroup:    cgroupRep,
		Toolchain: toolchainRep,
		Summary:   summary,
		Warnings:  warnings,
		Errors:    errs,
	}
}

func probeCache(customDir string) CacheReport {
	resolved, err := format.ResolveCacheDir(customDir)
	if err != nil {
		hint := format.DiagnoseError(format.StageCacheDirInit, err)
		if hint == "" {
			hint = "Ensure $XDG_CACHE_HOME, $TMPDIR, or custom cache path is writable."
		}
		return CacheReport{
			Ready:     false,
			Execution: executionUnavailable,
			Error:     err.Error(),
			Hint:      hint,
		}
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return CacheReport{
			Ready:        false,
			Execution:    executionUnavailable,
			ResolvedPath: resolved,
			Error:        err.Error(),
			Hint:         "Cache directory stat failed.",
		}
	}

	permStr := fmt.Sprintf("%04o", info.Mode().Perm())

	tmpFile, err := createTempFileFunc(resolved, ".microfat-doctor-probe-*.tmp")
	if err != nil {
		hint := format.DiagnoseError(format.StageCacheCreateTemp, err)
		if hint == "" {
			hint = "Unable to create files in cache directory. Check write permissions."
		}
		return CacheReport{
			Ready:        false,
			Execution:    executionUnavailable,
			ResolvedPath: resolved,
			Permissions:  permStr,
			Writable:     false,
			Error:        err.Error(),
			Hint:         hint,
		}
	}

	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	testPayload := []byte("microfat-doctor-probe-check")
	if _, err := tmpFile.Write(testPayload); err != nil {
		return CacheReport{
			Ready:        false,
			Execution:    executionUnavailable,
			ResolvedPath: resolved,
			Permissions:  permStr,
			Writable:     false,
			Error:        fmt.Sprintf("writing test probe file: %v", err),
			Hint:         "Write failed in cache directory.",
		}
	}

	if err := tmpFile.Sync(); err != nil {
		return CacheReport{
			Ready:        false,
			Execution:    executionUnavailable,
			ResolvedPath: resolved,
			Permissions:  permStr,
			Writable:     false,
			Error:        fmt.Sprintf("syncing test probe file: %v", err),
		}
	}

	_ = tmpFile.Close()

	// #nosec G304 -- reading temporary probe file created by test
	readData, err := os.ReadFile(tmpPath)
	if err != nil || string(readData) != string(testPayload) {
		return CacheReport{
			Ready:        false,
			Execution:    executionUnavailable,
			ResolvedPath: resolved,
			Permissions:  permStr,
			Writable:     false,
			Error:        "verifying written probe file data failed",
		}
	}

	_ = os.Remove(tmpPath)

	return CacheReport{
		Ready:        true,
		ResolvedPath: resolved,
		Permissions:  permStr,
		Writable:     true,
		Execution:    "unknown (write-only probe; noexec mount not tested)",
	}
}

func writeLine(w io.Writer, a ...any) error {
	_, err := fmt.Fprintln(w, a...)
	return err
}

func writeFormat(w io.Writer, f string, a ...any) error {
	_, err := fmt.Fprintf(w, f, a...)
	return err
}

func printDoctorReport(w io.Writer, rep *DoctorReport) error {
	if err := writeLine(w, "=== Microfat Host Environment Doctor ==="); err != nil {
		return err
	}
	if err := writeLine(w); err != nil {
		return err
	}

	if err := printCPUSection(w, &rep.CPU); err != nil {
		return err
	}
	if err := printMemfdSection(w, &rep.Memfd); err != nil {
		return err
	}
	if err := printCacheSection(w, &rep.Cache); err != nil {
		return err
	}
	if err := printCgroupSection(w, rep.Cgroup); err != nil {
		return err
	}
	if err := printToolchainSection(w, &rep.Toolchain); err != nil {
		return err
	}
	return printIssuesAndSummary(w, rep)
}

func printCPUSection(w io.Writer, cpu *CPUReport) error {
	cpuGlyph := glyphSuccess
	if cpu.Level == "" {
		cpuGlyph = glyphFailure
	} else if cpu.AVX512DownclockRisk {
		cpuGlyph = glyphWarning
	}
	if err := writeFormat(w, "%s Host CPU Microarchitecture\n", cpuGlyph); err != nil {
		return err
	}
	if err := writeFormat(w, "    • OS/Arch:        %s/%s\n", cpu.OS, cpu.Arch); err != nil {
		return err
	}
	if err := writeFormat(w, "    • Detected Level: %s\n", cpu.Level); err != nil {
		return err
	}
	if len(cpu.Features) > 0 {
		if err := writeFormat(w, "    • Key Features:   %s\n", strings.Join(cpu.Features, ", ")); err != nil {
			return err
		}
	}
	if cpu.AVX512DownclockNotice != "" {
		if err := writeFormat(w, "    • AVX-512 Status: %s\n", cpu.AVX512DownclockNotice); err != nil {
			return err
		}
	}
	return writeLine(w)
}

func printMemfdSection(w io.Writer, memfd *MemfdReport) error {
	memfdGlyph := glyphSuccess
	if !memfd.Available {
		if runtime.GOOS == "linux" {
			memfdGlyph = glyphWarning
		} else {
			memfdGlyph = glyphInfo
		}
	}
	if err := writeFormat(w, "%s In-Memory Execution (memfd_create) Prerequisites\n", memfdGlyph); err != nil {
		return err
	}
	if memfd.Kernel != "" {
		if err := writeFormat(w, "    • Kernel Support:    %s (%s)\n", memfd.Status, memfd.Kernel); err != nil {
			return err
		}
	} else {
		if err := writeFormat(w, "    • Kernel Support:    %s\n", memfd.Status); err != nil {
			return err
		}
	}
	if memfd.CreationStrategy != "" {
		if err := writeFormat(w, "    • Creation Strategy: %s\n", memfd.CreationStrategy); err != nil {
			return err
		}
	}
	if memfd.Mode != nil {
		if err := writeFormat(w, "    • Descriptor Mode:   %s (executable: %t)\n", memfd.Mode.ModeOctal, memfd.Mode.IsExecutable); err != nil {
			return err
		}
	}
	if memfd.Seals != nil {
		if memfd.Seals.Supported {
			if err := writeFormat(w, "    • Mandatory Seals:   supported (matched: %t)\n", memfd.Seals.Matches); err != nil {
				return err
			}
		} else {
			if err := writeFormat(w, "    • Mandatory Seals:   unsupported or blocked (%s)\n", memfd.Seals.Error); err != nil {
				return err
			}
		}
	}
	if memfd.Seccomp != "" {
		if err := writeFormat(w, "    • Seccomp Filter:    %s\n", memfd.Seccomp); err != nil {
			return err
		}
	}
	if memfd.Execution != "" {
		if err := writeFormat(w, "    • Execution Test:    %s\n", memfd.Execution); err != nil {
			return err
		}
	}
	if memfd.Hint != "" && !memfd.Available {
		if err := writeFormat(w, "    • Diagnostic:        %s\n", memfd.Hint); err != nil {
			return err
		}
	}
	return writeLine(w)
}

func printCacheSection(w io.Writer, cache *CacheReport) error {
	cacheGlyph := glyphSuccess
	if !cache.Ready {
		cacheGlyph = glyphFailure
	}
	if err := writeFormat(w, "%s Disk Cache Execution Fallback Prerequisites\n", cacheGlyph); err != nil {
		return err
	}
	if cache.ResolvedPath != "" {
		if err := writeFormat(w, "    • Resolved Path:     %s\n", cache.ResolvedPath); err != nil {
			return err
		}
	}
	if cache.Permissions != "" {
		writableStr := "read/write OK"
		if !cache.Writable {
			writableStr = "unwritable"
		}
		if err := writeFormat(w, "    • Permissions:       %s (%s)\n", cache.Permissions, writableStr); err != nil {
			return err
		}
	}
	if cache.Execution != "" {
		if err := writeFormat(w, "    • Execution Test:    %s\n", cache.Execution); err != nil {
			return err
		}
	}
	if cache.Error != "" {
		if err := writeFormat(w, "    • Error:             %s\n", cache.Error); err != nil {
			return err
		}
	}
	if cache.Hint != "" {
		if err := writeFormat(w, "    • Diagnostic:        %s\n", cache.Hint); err != nil {
			return err
		}
	}
	return writeLine(w)
}

func printCgroupSection(w io.Writer, cg *CgroupReport) error {
	if cg != nil && cg.Detected {
		if err := writeFormat(w, "%s Container Resource Limits (cgroup v%d)\n", glyphSuccess, cg.Version); err != nil {
			return err
		}
		if cg.MemoryLimitBytes > 0 {
			if err := writeFormat(w, "    • Memory Limit:   %s\n", formatBytes(cg.MemoryLimitBytes)); err != nil {
				return err
			}
		} else {
			if err := writeLine(w, "    • Memory Limit:   unlimited"); err != nil {
				return err
			}
		}
		if cg.MemoryHighBytes > 0 {
			if err := writeFormat(w, "    • Memory High:    %s\n", formatBytes(cg.MemoryHighBytes)); err != nil {
				return err
			}
		}
		if cg.CPUQuota > 0 {
			if err := writeFormat(w, "    • CFS CPU Quota:  %.2f cores\n", cg.CPUQuota); err != nil {
				return err
			}
		} else {
			if err := writeLine(w, "    • CFS CPU Quota:  unlimited"); err != nil {
				return err
			}
		}
		if cg.GOMEMLIMITStr != "" {
			constraintNote := ""
			if cg.ConstrainingLimit != "" {
				constraintNote = fmt.Sprintf(" [bounded by %s]", cg.ConstrainingLimit)
			}
			if err := writeFormat(
				w,
				"    • Auto GOMEMLIMIT: %s (~%s)%s\n",
				cg.GOMEMLIMITStr,
				formatBytes(cg.GOMEMLIMITBytes),
				constraintNote,
			); err != nil {
				return err
			}
		}
		if cg.GOMAXPROCS > 0 {
			if err := writeFormat(w, "    • Auto GOMAXPROCS: %d\n", cg.GOMAXPROCS); err != nil {
				return err
			}
		}
	} else {
		if err := writeFormat(w, "%s Container Resource Limits\n", glyphInfo); err != nil {
			return err
		}
		if err := writeLine(w, "    • Status:         No container cgroup limits detected (bare-metal / unconstrained host)"); err != nil {
			return err
		}
	}
	return writeLine(w)
}

func printToolchainSection(w io.Writer, tc *ToolchainReport) error {
	if err := writeFormat(w, "%s Toolchain & Version Metadata\n", glyphSuccess); err != nil {
		return err
	}
	if err := writeFormat(w, "    • Version:        %s\n", tc.Version); err != nil {
		return err
	}
	if err := writeFormat(w, "    • Commit:         %s\n", tc.Commit); err != nil {
		return err
	}
	if tc.Date != "" {
		if err := writeFormat(w, "    • Build Date:     %s\n", tc.Date); err != nil {
			return err
		}
	}
	return writeLine(w)
}

func printIssuesAndSummary(w io.Writer, rep *DoctorReport) error {
	if len(rep.Warnings) > 0 {
		if err := writeLine(w, "Warnings:"); err != nil {
			return err
		}
		for _, warn := range rep.Warnings {
			if err := writeFormat(w, "  [!] %s\n", warn); err != nil {
				return err
			}
		}
		if err := writeLine(w); err != nil {
			return err
		}
	}
	if len(rep.Errors) > 0 {
		if err := writeLine(w, "Errors:"); err != nil {
			return err
		}
		for _, errStr := range rep.Errors {
			if err := writeFormat(w, "  [✖] %s\n", errStr); err != nil {
				return err
			}
		}
		if err := writeLine(w); err != nil {
			return err
		}
	}

	return writeFormat(w, "Summary: %s\n", rep.Summary)
}

func formatBytes(bytes int64) string {
	if bytes >= bytesInTiB {
		return fmt.Sprintf("%d B (%.2f TiB)", bytes, float64(bytes)/float64(bytesInTiB))
	}
	if bytes >= bytesInGiB {
		return fmt.Sprintf("%d B (%.2f GiB)", bytes, float64(bytes)/float64(bytesInGiB))
	}
	if bytes >= bytesInMiB {
		return fmt.Sprintf("%d B (%.2f MiB)", bytes, float64(bytes)/float64(bytesInMiB))
	}
	if bytes >= bytesInKiB {
		return fmt.Sprintf("%d B (%.2f KiB)", bytes, float64(bytes)/float64(bytesInKiB))
	}
	return fmt.Sprintf("%d B", bytes)
}
