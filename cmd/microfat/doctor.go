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
	"github.com/EpicBlackWolfZ/microfat/internal/lifecycle"
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
	statCacheFunc                = os.Stat
	readFileFunc                 = os.ReadFile
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
	Policy    string          `json:"policy"`
	Scope     string          `json:"scope"`
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
	Passed                bool                         `json:"passed"`
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
	Available             bool                    `json:"available"`
	Passed                bool                    `json:"passed"`
	Phase                 string                  `json:"phase,omitempty"`
	Operation             string                  `json:"operation,omitempty"`
	Status                string                  `json:"status"`
	Kernel                string                  `json:"kernel,omitempty"`
	Seccomp               string                  `json:"seccomp,omitempty"`
	CreationStrategy      string                  `json:"creation_strategy,omitempty"`
	Mode                  *memfd.ModeObservation  `json:"mode,omitempty"`
	Seals                 *memfd.SealsObservation `json:"seals,omitempty"`
	Execution             string                  `json:"execution,omitempty"`
	Error                 string                  `json:"error,omitempty"`
	ErrorMessage          string                  `json:"error_message,omitempty"`
	ErrnoName             string                  `json:"errno_name,omitempty"`
	ErrnoValue            int                     `json:"errno_value,omitempty"`
	CandidateExplanations []string                `json:"candidate_explanations,omitempty"`
	Hint                  string                  `json:"hint,omitempty"`
	Cause                 error                   `json:"-"`
}

// CacheReport contains disk cache directory status and write permission details.
type CacheReport struct {
	Ready                 bool     `json:"ready"`
	Passed                bool     `json:"passed"`
	Phase                 string   `json:"phase,omitempty"`
	ResolvedPath          string   `json:"path"`
	Permissions           string   `json:"permissions"`
	Writable              bool     `json:"writable"`
	Execution             string   `json:"execution,omitempty"`
	Error                 string   `json:"error,omitempty"`
	ErrorMessage          string   `json:"error_message,omitempty"`
	ErrnoName             string   `json:"errno_name,omitempty"`
	ErrnoValue            int      `json:"errno_value,omitempty"`
	CandidateExplanations []string `json:"candidate_explanations,omitempty"`
	Hint                  string   `json:"hint,omitempty"`
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
		Short: "Verify host CPU, memfd_create, disk cache, and container cgroup environment prerequisites",
		Long: `doctor inspects the local runtime environment to verify host CPU microarchitecture level,
in-memory anonymous execution (memfd_create) prerequisites, disk cache fallback prerequisites, and container cgroup limits.

Doctor evaluates environment prerequisites. Runtime process execution is not tested during doctor probes.`,
		SilenceUsage: true,
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
	cmd.Flags().BoolVar(
		&opts.Strict,
		"strict",
		false,
		"Strict verification (requires both in-memory memfd_create and disk cache prerequisites to pass)",
	)

	return cmd
}

func runDoctor(opts DoctorOptions) *DoctorReport {
	hostInfo := microarchDetectFunc()
	downclockRisk := isAVX512DownclockingRiskFunc()

	cpuRep := CPUReport{
		Passed:              hostInfo.Level != "",
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

	var baseWarnings []string
	if downclockRisk {
		baseWarnings = append(baseWarnings, "Host CPU is subject to AVX-512 downclocking; consider MICROFAT_POLICY=safe_avx512")
	}

	policy := "normal"
	if opts.Strict {
		policy = "strict"
	}

	verdict := lifecycle.EvaluatePrerequisites(hostInfo.Level, memfdRep.Passed, cacheRep.Passed, opts.Strict, baseWarnings)

	return &DoctorReport{
		Ready:  verdict.Ready,
		Policy: policy,
		Scope:  "prerequisites",
		CPU:    cpuRep,
		Memfd:  memfdRep,
		Cache:  cacheRep,
		Execution: ExecutionReport{
			Status: "not_tested",
			Reason: "not_tested",
			Notice: "Doctor inspects environment prerequisites; runtime process execution is not tested during doctor probes.",
		},
		Cgroup:    cgroupRep,
		Toolchain: toolchainRep,
		Summary:   verdict.Summary,
		Warnings:  verdict.Warnings,
		Errors:    verdict.Errors,
	}
}

func probeCache(customDir string) CacheReport {
	const executionUntested = "unknown (prerequisite check only; payload execution was not tested)"

	resolved, err := format.ResolveCacheDir(customDir)
	if err != nil {
		hint := format.DiagnoseError(format.StageCacheDirInit, err)
		if hint == "" {
			hint = "Ensure $XDG_CACHE_HOME, $TMPDIR, or custom cache path is writable."
		}
		errnoName, errnoVal, explanations := lifecycle.ResolveCandidateExplanations(err)
		return CacheReport{
			Ready:                 false,
			Passed:                false,
			Phase:                 "resolution",
			Execution:             executionUnavailable,
			Error:                 err.Error(),
			ErrorMessage:          err.Error(),
			ErrnoName:             errnoName,
			ErrnoValue:            errnoVal,
			CandidateExplanations: explanations,
			Hint:                  hint,
		}
	}

	info, err := statCacheFunc(resolved)
	if err != nil {
		errnoName, errnoVal, explanations := lifecycle.ResolveCandidateExplanations(err)
		return CacheReport{
			Ready:                 false,
			Passed:                false,
			Phase:                 "stat",
			Execution:             executionUnavailable,
			ResolvedPath:          resolved,
			Error:                 err.Error(),
			ErrorMessage:          err.Error(),
			ErrnoName:             errnoName,
			ErrnoValue:            errnoVal,
			CandidateExplanations: explanations,
			Hint:                  "Cache directory stat failed.",
		}
	}

	permStr := fmt.Sprintf("%04o", info.Mode().Perm())

	tmpFile, err := createTempFileFunc(resolved, ".microfat-doctor-probe-*.tmp")
	if err != nil {
		hint := format.DiagnoseError(format.StageCacheCreateTemp, err)
		if hint == "" {
			hint = "Unable to create files in cache directory. Check write permissions."
		}
		errnoName, errnoVal, explanations := lifecycle.ResolveCandidateExplanations(err)
		return CacheReport{
			Ready:                 false,
			Passed:                false,
			Phase:                 "create_temp",
			Execution:             executionUnavailable,
			ResolvedPath:          resolved,
			Permissions:           permStr,
			Writable:              false,
			Error:                 err.Error(),
			ErrorMessage:          err.Error(),
			ErrnoName:             errnoName,
			ErrnoValue:            errnoVal,
			CandidateExplanations: explanations,
			Hint:                  hint,
		}
	}

	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	testPayload := []byte("microfat-doctor-probe-check")
	if _, err := tmpFile.Write(testPayload); err != nil {
		errnoName, errnoVal, explanations := lifecycle.ResolveCandidateExplanations(err)
		return CacheReport{
			Ready:                 false,
			Passed:                false,
			Phase:                 "write",
			Execution:             executionUnavailable,
			ResolvedPath:          resolved,
			Permissions:           permStr,
			Writable:              false,
			Error:                 fmt.Sprintf("writing test probe file: %v", err),
			ErrorMessage:          err.Error(),
			ErrnoName:             errnoName,
			ErrnoValue:            errnoVal,
			CandidateExplanations: explanations,
			Hint:                  "Write failed in cache directory.",
		}
	}

	if err := tmpFile.Sync(); err != nil {
		errnoName, errnoVal, explanations := lifecycle.ResolveCandidateExplanations(err)
		return CacheReport{
			Ready:                 false,
			Passed:                false,
			Phase:                 "sync",
			Execution:             executionUnavailable,
			ResolvedPath:          resolved,
			Permissions:           permStr,
			Writable:              false,
			Error:                 fmt.Sprintf("syncing test probe file: %v", err),
			ErrorMessage:          err.Error(),
			ErrnoName:             errnoName,
			ErrnoValue:            errnoVal,
			CandidateExplanations: explanations,
		}
	}

	_ = tmpFile.Close()

	// #nosec G304 -- reading temporary probe file created by test
	readData, err := readFileFunc(tmpPath)
	if err != nil || string(readData) != string(testPayload) {
		errStr := "verifying written probe file data failed"
		if err != nil {
			errStr = fmt.Sprintf("verifying written probe file data failed: %v", err)
		}
		errnoName, errnoVal, explanations := lifecycle.ResolveCandidateExplanations(err)
		return CacheReport{
			Ready:                 false,
			Passed:                false,
			Phase:                 "read",
			Execution:             executionUnavailable,
			ResolvedPath:          resolved,
			Permissions:           permStr,
			Writable:              false,
			Error:                 errStr,
			ErrorMessage:          errStr,
			ErrnoName:             errnoName,
			ErrnoValue:            errnoVal,
			CandidateExplanations: explanations,
		}
	}

	_ = os.Remove(tmpPath)

	return CacheReport{
		Ready:        true,
		Passed:       true,
		ResolvedPath: resolved,
		Permissions:  permStr,
		Writable:     true,
		Execution:    executionUntested,
	}
}

func appendLine(b *strings.Builder, a ...any) {
	_, _ = fmt.Fprintln(b, a...)
}

func appendFormat(b *strings.Builder, f string, a ...any) {
	_, _ = fmt.Fprintf(b, f, a...)
}

func printDoctorReport(w io.Writer, rep *DoctorReport) error {
	var b strings.Builder
	appendLine(&b, "=== Microfat Host Environment Doctor ===")
	appendLine(&b)

	printCPUSection(&b, &rep.CPU)
	printMemfdSection(&b, &rep.Memfd)
	printCacheSection(&b, &rep.Cache)
	printCgroupSection(&b, rep.Cgroup)
	printToolchainSection(&b, &rep.Toolchain)
	printIssuesAndSummary(&b, rep)

	_, err := io.WriteString(w, b.String())
	return err
}

func printCPUSection(b *strings.Builder, cpu *CPUReport) {
	cpuGlyph := glyphSuccess
	if cpu.Level == "" {
		cpuGlyph = glyphFailure
	} else if cpu.AVX512DownclockRisk {
		cpuGlyph = glyphWarning
	}
	appendFormat(b, "%s Host CPU Microarchitecture\n", cpuGlyph)
	appendFormat(b, "    • OS/Arch:        %s/%s\n", cpu.OS, cpu.Arch)
	appendFormat(b, "    • Detected Level: %s\n", cpu.Level)
	if len(cpu.Features) > 0 {
		appendFormat(b, "    • Key Features:   %s\n", strings.Join(cpu.Features, ", "))
	}
	if cpu.AVX512DownclockNotice != "" {
		appendFormat(b, "    • AVX-512 Status: %s\n", cpu.AVX512DownclockNotice)
	}
	appendLine(b)
}

func printMemfdSection(b *strings.Builder, memfd *MemfdReport) {
	memfdGlyph := glyphSuccess
	if !memfd.Passed {
		if runtime.GOOS == "linux" {
			memfdGlyph = glyphWarning
		} else {
			memfdGlyph = glyphInfo
		}
	}
	appendFormat(b, "%s In-Memory Execution (memfd_create) Prerequisites\n", memfdGlyph)
	if memfd.Kernel != "" {
		appendFormat(b, "    • Kernel Support:    %s (%s)\n", memfd.Status, memfd.Kernel)
	} else {
		appendFormat(b, "    • Kernel Support:    %s\n", memfd.Status)
	}
	if memfd.CreationStrategy != "" {
		appendFormat(b, "    • Creation Strategy: %s\n", memfd.CreationStrategy)
	}
	if memfd.Mode != nil {
		appendFormat(b, "    • Descriptor Mode:   %s (executable: %t)\n", memfd.Mode.ModeOctal, memfd.Mode.IsExecutable)
	}
	if memfd.Seals != nil {
		if memfd.Seals.Supported {
			appendFormat(b, "    • Mandatory Seals:   supported (matched: %t)\n", memfd.Seals.Matches)
		} else {
			appendFormat(b, "    • Mandatory Seals:   unsupported or blocked (%s)\n", memfd.Seals.Error)
		}
	}
	if memfd.Seccomp != "" {
		appendFormat(b, "    • Seccomp Filter:    %s\n", memfd.Seccomp)
	}
	if memfd.Execution != "" {
		appendFormat(b, "    • Execution Test:    %s\n", memfd.Execution)
	}
	printMemfdFailureDetails(b, memfd)
	appendLine(b)
}

func printMemfdFailureDetails(b *strings.Builder, memfd *MemfdReport) {
	if memfd.Phase != "" && !memfd.Passed {
		appendFormat(b, "    • Failed Phase:      %s\n", memfd.Phase)
	}
	if memfd.Operation != "" && !memfd.Passed {
		appendFormat(b, "    • Failed Operation:  %s\n", memfd.Operation)
	}
	if memfd.ErrnoName != "" {
		appendFormat(b, "    • Errno:             %s (%d)\n", memfd.ErrnoName, memfd.ErrnoValue)
	}
	for _, exp := range memfd.CandidateExplanations {
		appendFormat(b, "    • Possible Cause:    %s\n", exp)
	}
	if memfd.Hint != "" && !memfd.Passed {
		appendFormat(b, "    • Diagnostic:        %s\n", memfd.Hint)
	}
}

func printCacheSection(b *strings.Builder, cache *CacheReport) {
	cacheGlyph := glyphSuccess
	if !cache.Passed {
		cacheGlyph = glyphFailure
	}
	appendFormat(b, "%s Disk Cache Execution Fallback Prerequisites\n", cacheGlyph)
	if cache.ResolvedPath != "" {
		appendFormat(b, "    • Resolved Path:     %s\n", cache.ResolvedPath)
	}
	if cache.Permissions != "" {
		writableStr := "read/write OK"
		if !cache.Writable {
			writableStr = "unwritable"
		}
		appendFormat(b, "    • Permissions:       %s (%s)\n", cache.Permissions, writableStr)
	}
	if cache.Execution != "" {
		appendFormat(b, "    • Execution Test:    %s\n", cache.Execution)
	}
	if cache.Phase != "" && !cache.Passed {
		appendFormat(b, "    • Failed Phase:      %s\n", cache.Phase)
	}
	if cache.ErrnoName != "" {
		appendFormat(b, "    • Errno:             %s (%d)\n", cache.ErrnoName, cache.ErrnoValue)
	}
	for _, exp := range cache.CandidateExplanations {
		appendFormat(b, "    • Possible Cause:    %s\n", exp)
	}
	if cache.Error != "" {
		appendFormat(b, "    • Error:             %s\n", cache.Error)
	}
	if cache.Hint != "" {
		appendFormat(b, "    • Diagnostic:        %s\n", cache.Hint)
	}
	appendLine(b)
}

func printCgroupSection(b *strings.Builder, cg *CgroupReport) {
	if cg != nil && cg.Detected {
		appendFormat(b, "%s Container Resource Limits (cgroup v%d)\n", glyphSuccess, cg.Version)
		if cg.MemoryLimitBytes > 0 {
			appendFormat(b, "    • Memory Limit:   %s\n", formatBytes(cg.MemoryLimitBytes))
		} else {
			appendLine(b, "    • Memory Limit:   unlimited")
		}
		if cg.MemoryHighBytes > 0 {
			appendFormat(b, "    • Memory High:    %s\n", formatBytes(cg.MemoryHighBytes))
		}
		if cg.CPUQuota > 0 {
			appendFormat(b, "    • CFS CPU Quota:  %.2f cores\n", cg.CPUQuota)
		} else {
			appendLine(b, "    • CFS CPU Quota:  unlimited")
		}
		if cg.GOMEMLIMITStr != "" {
			constraintNote := ""
			if cg.ConstrainingLimit != "" {
				constraintNote = fmt.Sprintf(" [bounded by %s]", cg.ConstrainingLimit)
			}
			appendFormat(
				b,
				"    • Auto GOMEMLIMIT: %s (~%s)%s\n",
				cg.GOMEMLIMITStr,
				formatBytes(cg.GOMEMLIMITBytes),
				constraintNote,
			)
		}
		if cg.GOMAXPROCS > 0 {
			appendFormat(b, "    • Auto GOMAXPROCS: %d\n", cg.GOMAXPROCS)
		}
	} else {
		appendFormat(b, "%s Container Resource Limits\n", glyphInfo)
		appendLine(b, "    • Status:         No container cgroup limits detected (bare-metal / unconstrained host)")
	}
	appendLine(b)
}

func printToolchainSection(b *strings.Builder, tc *ToolchainReport) {
	appendFormat(b, "%s Toolchain & Version Metadata\n", glyphSuccess)
	appendFormat(b, "    • Version:        %s\n", tc.Version)
	appendFormat(b, "    • Commit:         %s\n", tc.Commit)
	if tc.Date != "" {
		appendFormat(b, "    • Build Date:     %s\n", tc.Date)
	}
	appendLine(b)
}

func printIssuesAndSummary(b *strings.Builder, rep *DoctorReport) {
	if len(rep.Warnings) > 0 {
		appendLine(b, "Warnings:")
		for _, warn := range rep.Warnings {
			appendFormat(b, "  [!] %s\n", warn)
		}
		appendLine(b)
	}
	if len(rep.Errors) > 0 {
		appendLine(b, "Errors:")
		for _, errStr := range rep.Errors {
			appendFormat(b, "  [✖] %s\n", errStr)
		}
		appendLine(b)
	}

	appendFormat(b, "Summary: %s\n", rep.Summary)
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
