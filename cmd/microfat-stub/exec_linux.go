//go:build linux

package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/cache"
	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/memfd"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"golang.org/x/sys/unix"
)

const (
	privateCacheDirMode = 0o700
	privateExecMode     = 0o700
	extraEnvCapacity    = 16
	maxTempFileAttempts = 1000
	// memfdTargetSeals defines the mandatory Linux kernel memory file descriptor seals applied to
	// anonymous RAM payloads prior to execution via /proc/self/fd/<fd>.
	// - F_SEAL_WRITE: prevents any modification of the decompressed binary code in memory.
	// - F_SEAL_SHRINK & F_SEAL_GROW: prevents truncation or expansion of the memory region.
	// - F_SEAL_SEAL: permanently locks the seal set, preventing any further seals or unsealing.
	// This ensures payload integrity and memory safety against runtime tampering or write races.
	memfdTargetSeals = memfd.TargetSeals
)

var (
	execveFunc      = syscall.Exec
	memfdCreateFunc = unix.MemfdCreate
	memfdSealFunc   = func(fd int, seals int) error {
		_, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, seals)
		return err
	}
	readCgroupLimitsFunc = cgroup.ReadLimits
	resolveCacheDirFunc  = format.ResolveCacheDirFD
	userHomeDirFunc      = os.UserHomeDir
	cryptoRandReader     = rand.Reader
	openCachedBinaryFunc = func(path string) (int, error) {
		return cache.OpenFileFunc(path)
	}
	openCachedBinaryAtFunc = func(dirFD int, name string) (int, error) {
		return cache.OpenFileAtFunc(dirFD, name)
	}
	filepathAbsFunc = filepath.Abs
)

// extractVariantToWriter seeks to the variant offset and streams decompressed bytes to w,
// verifying the payload SHA-256 digest concurrently during decompression.
func extractVariantToWriter(selfFile *os.File, entry *format.VariantEntry, idx *format.Index, w io.Writer) error {
	var dictBytes []byte
	if idx != nil && idx.DictionarySize > 0 {
		if idx.DictionarySize > format.MaxDictionarySize || idx.DictionaryOffset < 0 {
			return fmt.Errorf("%w: dictionary size %d or offset %d out of bounds",
				format.ErrInvalidDictionary, idx.DictionarySize, idx.DictionaryOffset)
		}
		if idx.DictionarySHA256 == "" || !format.ValidateChecksum(idx.DictionarySHA256) {
			return fmt.Errorf("%w: dictionary missing or invalid sha256 checksum", format.ErrInvalidChecksum)
		}
		dictBytes = make([]byte, idx.DictionarySize)
		if _, err := selfFile.ReadAt(dictBytes, idx.DictionaryOffset); err != nil {
			return fmt.Errorf("reading shared dictionary: %w", err)
		}
		h := sha256.Sum256(dictBytes)
		actualHex := hex.EncodeToString(h[:])
		if actualHex != idx.DictionarySHA256 {
			return fmt.Errorf("%w: expected %s, got %s", format.ErrDictionaryCorrupted, idx.DictionarySHA256, actualHex)
		}
	}

	if entry.UncompressedSize <= 0 || entry.UncompressedSize > format.MaxPayloadSize {
		return fmt.Errorf("%w: variant %s uncompressed size %d invalid", format.ErrPayloadTooLarge, entry.Level, entry.UncompressedSize)
	}
	if entry.SHA256 == "" || !format.ValidateChecksum(entry.SHA256) {
		return fmt.Errorf("%w: variant %s missing or invalid sha256 checksum", format.ErrInvalidChecksum, entry.Level)
	}

	c, err := codec.Get(entry.Compression)
	if err != nil {
		return fmt.Errorf("lookup codec %q for variant %s: %w", entry.Compression, entry.Level, err)
	}

	secReader := io.NewSectionReader(selfFile, entry.Offset, entry.CompressedSize)
	hasher := sha256.New()
	mw := io.MultiWriter(w, hasher)
	if err := codec.DecompressWithOptionalDict(c, mw, secReader, entry.UncompressedSize, dictBytes); err != nil {
		return fmt.Errorf("decompressing variant payload: %w", err)
	}

	actualHex := hex.EncodeToString(hasher.Sum(nil))
	if actualHex != entry.SHA256 {
		return fmt.Errorf("%w: expected %s, got %s", format.ErrPayloadCorrupted, entry.SHA256, actualHex)
	}

	return nil
}

// executeVariant runs the selected variant payload in-memory using Linux memfd_create,
// falling back to user cache execution if memfd is restricted or if cache mode is explicitly requested.
func isPayloadCorruptionOrDecompressionError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, format.ErrMemfdExtract) ||
		errors.Is(err, format.ErrPayloadCorrupted) ||
		errors.Is(err, format.ErrDictionaryCorrupted) ||
		errors.Is(err, format.ErrInvalidDictionary) ||
		errors.Is(err, format.ErrInvalidChecksum) ||
		errors.Is(err, format.ErrPayloadTooLarge) ||
		errors.Is(err, codec.ErrDecompressionFailed) ||
		errors.Is(err, codec.ErrSizeMismatch) ||
		errors.Is(err, codec.ErrUnsupportedCodec)
}

func executeVariant(
	selfPath string,
	selfFile *os.File,
	entry *format.VariantEntry,
	idx *format.Index,
	args []string,
	baseEnv []string,
	hostInfo microarch.Info,
	policyRes microarch.PolicyResult,
	startTime time.Time,
) error {
	if selfPath == "" && selfFile != nil {
		selfPath = selfFile.Name()
	}

	// Check if cache dispatch mode is explicitly requested via environment
	requestedMode := os.Getenv(format.EnvExecMode)
	if requestedMode == "" {
		requestedMode = os.Getenv(format.EnvDispatchMode)
	}

	if strings.EqualFold(requestedMode, format.ExecModeCache) {
		return executeViaCache(selfPath, selfFile, entry, idx, args, baseEnv, hostInfo, policyRes, nil, startTime)
	}

	// 1. Try In-Memory memfd_create
	err := executeViaMemfd(selfPath, selfFile, entry, idx, args, baseEnv, hostInfo, policyRes, startTime)
	if err == nil {
		return nil
	}

	// If explicit memfd execution mode was requested, fail fast without fallback
	if strings.EqualFold(requestedMode, format.ExecModeMemfd) {
		return err
	}

	// If fat binary payload or dictionary is corrupted, fail fast without attempting fallback
	if isPayloadCorruptionOrDecompressionError(err) {
		return err
	}

	// 2. Fallback to cached file execution
	return executeViaCache(selfPath, selfFile, entry, idx, args, baseEnv, hostInfo, policyRes, err, startTime)
}

func upsertEnv(env []string, keyIndex map[string]int, key, val string) []string {
	entry := key + "=" + val
	if idx, exists := keyIndex[key]; exists {
		env[idx] = entry
		return env
	}
	keyIndex[key] = len(env)
	return append(env, entry)
}

func isMicrofatInternalEnv(k string) bool {
	switch k {
	case format.EnvOriginalExe,
		format.EnvSelectedVariant,
		format.EnvHostArch,
		format.EnvHostLevel,
		format.EnvExecMode,
		format.EnvDispatchMode,
		format.EnvSelectedSHA256,
		format.EnvSelectedSize,
		format.EnvPolicyApplied,
		format.EnvOverrideReason,
		format.EnvCgroupVersion,
		format.EnvCgroupLimitBytes,
		format.EnvCgroupHighBytes,
		format.EnvCgroupEffectiveLimitBytes,
		format.EnvCgroupCPUs,
		format.EnvCgroupGOMEMLIMIT,
		format.EnvCgroupGOMAXPROCS,
		format.EnvCgroupGOGC,
		format.EnvCgroupGCProfile, format.EnvCgroupGOGCSkippedReason:
		return true
	default:
		return false
	}
}

func buildAutoTunedEnviron(
	selfPath string,
	baseEnv []string,
	entry *format.VariantEntry,
	execMode string,
	hostInfo microarch.Info,
	policyRes microarch.PolicyResult,
) ([]string, *cgroup.Limits) {
	env := make([]string, 0, len(baseEnv)+extraEnvCapacity)
	keyIndex := make(map[string]int, len(baseEnv)+extraEnvCapacity)

	for _, e := range baseEnv {
		k, _, found := strings.Cut(e, "=")
		if !found || k == "" {
			env = append(env, e)
			continue
		}
		if isMicrofatInternalEnv(k) {
			// Strip any pre-existing internal microfat metadata from parent environment to prevent spoofing
			continue
		}
		if idx, exists := keyIndex[k]; exists {
			env[idx] = e
		} else {
			keyIndex[k] = len(env)
			env = append(env, e)
		}
	}

	if selfPath != "" {
		absPath, err := filepathAbsFunc(selfPath)
		if err != nil {
			absPath = filepath.Clean(selfPath)
		}
		env = upsertEnv(env, keyIndex, format.EnvOriginalExe, absPath)
	}

	env = upsertEnv(env, keyIndex, format.EnvSelectedVariant, entry.Level)
	env = upsertEnv(env, keyIndex, format.EnvHostArch, hostInfo.Arch)
	env = upsertEnv(env, keyIndex, format.EnvHostLevel, hostInfo.Level)
	env = upsertEnv(env, keyIndex, format.EnvExecMode, execMode)
	env = upsertEnv(env, keyIndex, format.EnvDispatchMode, execMode)
	env = upsertEnv(env, keyIndex, format.EnvSelectedSHA256, entry.SHA256)
	env = upsertEnv(env, keyIndex, format.EnvSelectedSize, strconv.FormatInt(entry.UncompressedSize, 10))

	if policyRes.PolicyApplied != "" {
		env = upsertEnv(env, keyIndex, format.EnvPolicyApplied, policyRes.PolicyApplied)
		env = upsertEnv(env, keyIndex, format.EnvOverrideReason, policyRes.OverrideReason)
	}

	limits, err := readCgroupLimitsFunc()
	if err != nil || limits.CgroupVersion == cgroup.VersionUnknown {
		if os.Getenv(format.EnvDebug) == "1" || strings.EqualFold(os.Getenv(format.EnvDebug), "true") {
			if err != nil {
				fmt.Fprintf(os.Stderr, "[microfat:debug] cgroup autotuning skipped: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "[microfat:debug] cgroup autotuning skipped: cgroup version unknown\n")
			}
		}
		return env, nil
	}

	env = populateCgroupEnviron(env, keyIndex, limits)

	gcProfile, _ := cgroup.ParseGCProfile(os.Getenv(format.EnvGCProfile))
	liveHeap, _ := cgroup.ParseByteSize(os.Getenv(format.EnvLiveHeapEstimate))

	if execMode == format.ExecModeMemfd {
		page := int64(os.Getpagesize())
		if entry.UncompressedSize > 0 && entry.UncompressedSize <= format.MaxPayloadSize {
			limits.RetainedExecutableBytes = (entry.UncompressedSize + page - 1) / page * page
		}
	}

	plan := cgroup.ResolveTuningPlanWithProfile(
		limits,
		os.Getenv(format.EnvMemRatio),
		cgroup.DefaultMemoryRatio,
		cgroup.DefaultMinHeadroomBytes,
		gcProfile,
		liveHeap,
	)

	env = resolveBatchGCEnviron(env, keyIndex, &plan)

	if plan.GOMEMLIMITStr != "" {
		env = upsertEnv(env, keyIndex, format.EnvCgroupGOMEMLIMIT, plan.GOMEMLIMITStr)
	}
	if plan.GOMAXPROCSStr != "" {
		env = upsertEnv(env, keyIndex, format.EnvCgroupGOMAXPROCS, plan.GOMAXPROCSStr)
	}
	if plan.GOGCStr != "" {
		env = upsertEnv(env, keyIndex, format.EnvCgroupGOGC, plan.GOGCStr)
	}
	if plan.GCProfile != cgroup.GCProfileDefault {
		env = upsertEnv(env, keyIndex, format.EnvCgroupGCProfile, string(plan.GCProfile))
	}

	// Check if user opted out of auto-tuning or requested dry-run simulation
	autoTuneOpt := strings.TrimSpace(os.Getenv(format.EnvAutotune))
	if autoTuneOpt == "0" || strings.EqualFold(autoTuneOpt, "false") {
		return env, &limits
	}
	dryRunOpt := strings.TrimSpace(os.Getenv(format.EnvDryRun))
	if dryRunOpt == "1" || strings.EqualFold(dryRunOpt, "true") {
		return env, &limits
	}

	env = applyRuntimeTuningPlan(env, keyIndex, plan)

	return env, &limits
}

func resolveBatchGCEnviron(env []string, keyIndex map[string]int, plan *cgroup.TuningPlan) []string {
	if index, exists := keyIndex["GOMEMLIMIT"]; exists {
		_, value, _ := strings.Cut(env[index], "=")
		plan.ResolveBatchGOGC(cgroup.RuntimeMemoryLimit(value))
	}
	if _, explicitGC := keyIndex["GOGC"]; !explicitGC && plan.GOGCSkippedReason != "" {
		env = upsertEnv(env, keyIndex, format.EnvCgroupGOGCSkippedReason, plan.GOGCSkippedReason)
	}
	return env
}

func populateCgroupEnviron(env []string, keyIndex map[string]int, limits cgroup.Limits) []string {
	env = upsertEnv(env, keyIndex, format.EnvCgroupVersion, strconv.Itoa(limits.CgroupVersion))
	env = upsertEnv(env, keyIndex, format.EnvCgroupLimitBytes, strconv.FormatInt(limits.MemoryLimitBytes, 10))
	if limits.MemoryHighBytes > 0 {
		env = upsertEnv(env, keyIndex, format.EnvCgroupHighBytes, strconv.FormatInt(limits.MemoryHighBytes, 10))
	}
	if limits.EffectiveMemoryLimitBytes > 0 {
		env = upsertEnv(env, keyIndex, format.EnvCgroupEffectiveLimitBytes, strconv.FormatInt(limits.EffectiveMemoryLimitBytes, 10))
	}
	return upsertEnv(env, keyIndex, format.EnvCgroupCPUs, fmt.Sprintf("%.2f", limits.CPUQuota))
}

func applyRuntimeTuningPlan(env []string, keyIndex map[string]int, plan cgroup.TuningPlan) []string {
	if _, hasMem := keyIndex["GOMEMLIMIT"]; !hasMem && plan.GOMEMLIMITStr != "" {
		env = upsertEnv(env, keyIndex, "GOMEMLIMIT", plan.GOMEMLIMITStr)
	}
	if _, hasProcs := keyIndex["GOMAXPROCS"]; !hasProcs && plan.GOMAXPROCSStr != "" {
		env = upsertEnv(env, keyIndex, "GOMAXPROCS", plan.GOMAXPROCSStr)
	}
	if _, hasGC := keyIndex["GOGC"]; !hasGC && plan.GOGCApplied && plan.GOGCStr != "" {
		env = upsertEnv(env, keyIndex, "GOGC", plan.GOGCStr)
	}
	return env
}

func logDiagnostics(
	entry *format.VariantEntry,
	execMode string,
	hostInfo microarch.Info,
	policyRes microarch.PolicyResult,
	env []string,
	limits *cgroup.Limits,
	decompDuration time.Duration,
	totalDuration time.Duration,
) {
	debugOpt := os.Getenv(format.EnvDebug)
	logOpt := os.Getenv(format.EnvLog)
	if debugOpt == "" && logOpt == "" {
		return
	}
	if debugOpt == "0" || strings.EqualFold(debugOpt, "false") {
		if logOpt == "" {
			return
		}
	}

	var memLimit, maxProcs, gogcVal, gcProfileVal string
	for _, e := range env {
		if after, ok := strings.CutPrefix(e, "GOMEMLIMIT="); ok {
			memLimit = after
		}
		if after, ok := strings.CutPrefix(e, "GOMAXPROCS="); ok {
			maxProcs = after
		}
		if after, ok := strings.CutPrefix(e, "GOGC="); ok {
			gogcVal = after
		}
		if after, ok := strings.CutPrefix(e, format.EnvCgroupGCProfile+"="); ok {
			gcProfileVal = after
		}
	}

	if strings.EqualFold(logOpt, "json") {
		d := format.DispatchTelemetry{
			Event:                   format.EventDispatch,
			TimestampUnixNano:       time.Now().UnixNano(),
			HostArch:                hostInfo.Arch,
			HostLevel:               hostInfo.Level,
			SelectedVariant:         entry.Level,
			SelectedSHA256:          entry.SHA256,
			SelectedSizeBytes:       entry.UncompressedSize,
			ExecMode:                execMode,
			PolicyApplied:           policyRes.PolicyApplied,
			PolicyReason:            policyRes.OverrideReason,
			GOMEMLIMIT:              memLimit,
			GOMAXPROCS:              maxProcs,
			GOGC:                    gogcVal,
			GCProfile:               gcProfileVal,
			DecompressionDurationUs: decompDuration.Microseconds(),
			TotalLauncherUs:         totalDuration.Microseconds(),
		}
		if limits != nil {
			d.CgroupVersion = limits.CgroupVersion
			d.CgroupMemLimitBytes = limits.MemoryLimitBytes
			d.CgroupCPUQuota = limits.CPUQuota
		}
		fmt.Fprintf(os.Stderr, "[microfat] %s\n", formatDispatchTelemetryJSON(d))
		return
	}

	policyStr := ""
	if policyRes.PolicyApplied != "" {
		policyStr = fmt.Sprintf(" policy=%s policy_reason=%q", policyRes.PolicyApplied, policyRes.OverrideReason)
	}

	gogcStr := ""
	if gogcVal != "" {
		gogcStr = fmt.Sprintf(" gogc=%s", gogcVal)
	}
	if gcProfileVal != "" {
		gogcStr += fmt.Sprintf(" gc_profile=%s", gcProfileVal)
	}

	fmt.Fprintf(
		os.Stderr,
		"[microfat:debug] host_arch=%s host_level=%s selected_variant=%s exec_mode=%s gomemlimit=%s gomaxprocs=%s%s%s "+
			"decompress_us=%d total_us=%d\n",
		hostInfo.Arch, hostInfo.Level, entry.Level, execMode, memLimit, maxProcs, gogcStr, policyStr,
		decompDuration.Microseconds(), totalDuration.Microseconds(),
	)
}

func logErrorDiagnostics(
	stage string,
	err error,
	hostInfo microarch.Info,
	entry *format.VariantEntry,
	policyRes microarch.PolicyResult,
	details string,
) {
	requestedMode := os.Getenv(format.EnvExecMode)
	if requestedMode == "" {
		requestedMode = os.Getenv(format.EnvDispatchMode)
	}
	if requestedMode == "" {
		requestedMode = format.ExecModeAuto
	}
	attemptedMode := format.ExecModeMemfd
	if strings.HasPrefix(stage, "cache") {
		attemptedMode = format.ExecModeCache
	}
	var attempts []format.ExecutionAttempt
	var dispErr *format.DispatchError
	if errors.As(err, &dispErr) {
		attempts = dispErr.Attempts
		if dispErr.RequestedMode != "" {
			requestedMode = dispErr.RequestedMode
		}
	}
	logErrorDiagnosticsWithAttempts(stage, err, hostInfo, entry, policyRes, requestedMode, attemptedMode, attempts, details)
}

func logErrorDiagnosticsWithAttempts(
	stage string,
	err error,
	hostInfo microarch.Info,
	entry *format.VariantEntry,
	policyRes microarch.PolicyResult,
	requestedMode string,
	attemptedMode string,
	attempts []format.ExecutionAttempt,
	details string,
) {
	hint := format.DiagnoseError(stage, err)
	errno, errnoName := format.ExtractErrno(err)

	logOpt := os.Getenv(format.EnvLog)
	if strings.EqualFold(logOpt, "json") {
		e := format.ErrorTelemetry{
			Event:             format.EventError,
			TimestampUnixNano: time.Now().UnixNano(),
			HostArch:          hostInfo.Arch,
			HostLevel:         hostInfo.Level,
			PolicyApplied:     policyRes.PolicyApplied,
			PolicyReason:      policyRes.OverrideReason,
			Stage:             stage,
			RequestedMode:     requestedMode,
			AttemptedMode:     attemptedMode,
			Error:             err.Error(),
			Errno:             errno,
			ErrnoName:         errnoName,
			Attempts:          attempts,
			Details:           details,
			Hint:              hint,
		}
		if entry != nil {
			e.SelectedVariant = entry.Level
		}
		fmt.Fprintf(os.Stderr, "[microfat] %s\n", formatErrorTelemetryJSON(e))
		return
	}

	debugOpt := os.Getenv(format.EnvDebug)
	if (debugOpt == "1" || strings.EqualFold(debugOpt, "true")) && hint != "" {
		fmt.Fprintf(os.Stderr, "[microfat:hint] %s\n", hint)
	}
}

func buildCombinedDispatchError(
	sentinel error,
	requestedMode string,
	primaryErr error,
	cacheStage string,
	cacheErr error,
) *format.DispatchError {
	memfdStage := format.StageMemfdCreate
	switch {
	case errors.Is(primaryErr, format.ErrExecve):
		memfdStage = format.StageMemfdExec
	case errors.Is(primaryErr, format.ErrMemfdSealingFailed):
		memfdStage = format.StageMemfdSeal
	case errors.Is(primaryErr, format.ErrMemfdExtract):
		memfdStage = format.StageMemfdExtract
	}

	memfdErrno, memfdErrnoName := format.ExtractErrno(primaryErr)
	cacheErrno, cacheErrnoName := format.ExtractErrno(cacheErr)

	attempts := []format.ExecutionAttempt{
		{
			Stage:         memfdStage,
			RequestedMode: requestedMode,
			AttemptedMode: format.ExecModeMemfd,
			Err:           primaryErr,
			Error:         primaryErr.Error(),
			Errno:         memfdErrno,
			ErrnoName:     memfdErrnoName,
		},
		{
			Stage:         cacheStage,
			RequestedMode: requestedMode,
			AttemptedMode: format.ExecModeCache,
			Err:           cacheErr,
			Error:         cacheErr.Error(),
			Errno:         cacheErrno,
			ErrnoName:     cacheErrnoName,
		},
	}

	var fallbackDesc string
	if cacheStage == format.StageCacheExec {
		fallbackDesc = "cache fallback execve failed: "
	}
	summary := fmt.Sprintf(
		"%v: launcher execution failed in %s mode: %s(primary memfd error: %v) "+
			"[attempt 1: memfd/%s: %v] [attempt 2: cache/%s: %v]",
		sentinel, requestedMode, fallbackDesc, primaryErr, memfdStage, primaryErr, cacheStage, cacheErr,
	)

	return &format.DispatchError{
		PrimarySentinel: sentinel,
		RequestedMode:   requestedMode,
		Attempts:        attempts,
		Summary:         summary,
	}
}

// executeViaMemfd creates an anonymous, in-memory ELF file descriptor using memfd_create with MFD_ALLOW_SEALING,
// decompresses the selected variant payload into RAM, verifies its SHA-256 payload digest, applies mandatory
// descriptor seals (F_SEAL_WRITE | F_SEAL_SHRINK | F_SEAL_GROW | F_SEAL_SEAL) to guarantee immutability,
// and replaces the process image via execve on /proc/self/fd/<fd>.
// If memfd creation or sealing fails (e.g. due to restrictive seccomp profiles or unsupported kernel versions),
// it returns an error allowing auto-dispatch to fall back to hardened descriptor-bound cache execution.
func executeViaMemfd(
	selfPath string,
	selfFile *os.File,
	entry *format.VariantEntry,
	idx *format.Index,
	args []string,
	baseEnv []string,
	hostInfo microarch.Info,
	policyRes microarch.PolicyResult,
	startTime time.Time,
) error {
	requestedMode := os.Getenv(format.EnvExecMode)
	if requestedMode == "" {
		requestedMode = os.Getenv(format.EnvDispatchMode)
	}
	if requestedMode == "" {
		requestedMode = format.ExecModeAuto
	}

	if err := checkExtractionBudget(entry, idx); err != nil {
		return err
	}

	if selfPath == "" && selfFile != nil {
		selfPath = selfFile.Name()
	}
	env, limits := buildAutoTunedEnviron(selfPath, baseEnv, entry, format.ExecModeMemfd, hostInfo, policyRes)

	fd, err := createExecutableMemfd()
	if err != nil {
		details := "falling back to disk cache"
		if strings.EqualFold(requestedMode, format.ExecModeMemfd) {
			details = "forced memfd mode: no cache fallback will be attempted"
		}
		logErrorDiagnostics(format.StageMemfdCreate, err, hostInfo, entry, policyRes, details)
		if strings.EqualFold(requestedMode, format.ExecModeMemfd) {
			return fmt.Errorf("%w: memfd_create failed (forced memfd mode): %w", format.ErrMemfdCreate, err)
		}
		return fmt.Errorf("%w: memfd_create failed: %w", format.ErrMemfdCreate, err)
	}

	memFile := os.NewFile(uintptr(fd), "microfat_payload")
	defer func() { _ = memFile.Close() }()

	decompStart := time.Now()
	if err := extractVariantToWriter(selfFile, entry, idx, memFile); err != nil {
		logErrorDiagnostics(format.StageMemfdExtract, err, hostInfo, entry, policyRes, "decompressing payload failed")
		return fmt.Errorf("%w: decompressing into memfd: %w", format.ErrMemfdExtract, err)
	}
	decompDuration := time.Since(decompStart)

	// Seal anonymous memory file descriptor to prevent tampering prior to execution
	if sealErr := memfdSealFunc(fd, memfdTargetSeals); sealErr != nil {
		logErrorDiagnostics(format.StageMemfdSeal, sealErr, hostInfo, entry, policyRes, "memfd sealing failed")
		return fmt.Errorf("%w: failed to seal memfd descriptor: %w", format.ErrMemfdSealingFailed, sealErr)
	}

	logDiagnostics(entry, format.ExecModeMemfd, hostInfo, policyRes, env, limits, decompDuration, time.Since(startTime))

	procPath := "/proc/self/fd/" + strconv.Itoa(fd)
	// #nosec G204, G702 -- launcher stub explicitly forwards process execution to the payload
	execErr := execveFunc(procPath, args, env)
	if execErr == nil {
		return nil
	}
	logErrorDiagnostics(format.StageMemfdExec, execErr, hostInfo, entry, policyRes, "execve failed on "+procPath)
	return fmt.Errorf("%w: execve on %s failed: %w", format.ErrExecve, procPath, execErr)
}

func handleCacheError(
	sentinel error,
	stage string,
	err error,
	primaryErr error,
	requestedMode string,
	hostInfo microarch.Info,
	entry *format.VariantEntry,
	policyRes microarch.PolicyResult,
	details string,
) error {
	if primaryErr != nil {
		dispErr := buildCombinedDispatchError(sentinel, requestedMode, primaryErr, stage, err)
		logErrorDiagnostics(stage, dispErr, hostInfo, entry, policyRes, details)
		return dispErr
	}
	var errOut error
	if stage == format.StageCacheExec {
		errOut = fmt.Errorf("%w: cache execve failed (%s): %w", sentinel, details, err)
	} else {
		errOut = fmt.Errorf("%w: %w", sentinel, err)
	}
	logErrorDiagnostics(stage, errOut, hostInfo, entry, policyRes, details)
	return errOut
}

func executeViaCache(
	selfPath string,
	selfFile *os.File,
	entry *format.VariantEntry,
	idx *format.Index,
	args []string,
	baseEnv []string,
	hostInfo microarch.Info,
	policyRes microarch.PolicyResult,
	primaryErr error,
	startTime time.Time,
) error {
	requestedMode := os.Getenv(format.EnvExecMode)
	if requestedMode == "" {
		requestedMode = os.Getenv(format.EnvDispatchMode)
	}
	if requestedMode == "" {
		requestedMode = format.ExecModeAuto
	}

	if selfPath == "" && selfFile != nil {
		selfPath = selfFile.Name()
	}
	env, limits := buildAutoTunedEnviron(selfPath, baseEnv, entry, format.ExecModeCache, hostInfo, policyRes)

	dirFD, cacheDir, err := resolveCacheDirFunc("")
	if err != nil {
		return handleCacheError(
			format.ErrCacheInit, format.StageCacheDirInit, err,
			primaryErr, requestedMode, hostInfo, entry, policyRes, "cache directory creation failed",
		)
	}
	if dirFD < 0 {
		return fmt.Errorf("%w: invalid cache directory descriptor", format.ErrCacheInit)
	}
	defer func() { _ = unix.Close(dirFD) }()

	if !format.ValidateChecksum(entry.SHA256) || entry.SHA256 == "" {
		rawErr := fmt.Errorf("invalid variant sha256 checksum format %q", entry.SHA256)
		return handleCacheError(
			format.ErrCacheWrite, format.StageCacheCreateTemp, rawErr,
			primaryErr, requestedMode, hostInfo, entry, policyRes, "invalid cache filename",
		)
	}

	cachedName := filepath.Clean(entry.SHA256)
	cachedBinary := filepath.Join(cacheDir, cachedName)
	var decompDuration time.Duration

	fd, openErr := openAndValidateCacheAtFD(dirFD, cachedName, entry)
	if openErr != nil &&
		(cache.IsSymlinkErr(openErr) || errors.Is(openErr, cache.ErrNonRegularFile) || errors.Is(openErr, cache.ErrUnsafeFile)) {
		reason := "refusal to execute unsafe cache entry"
		if cache.IsSymlinkErr(openErr) {
			reason = "refusal to execute symlink"
		}
		rawErr := fmt.Errorf("%s at %s: %w", reason, cachedBinary, openErr)
		return handleCacheError(
			format.ErrCacheWrite, format.StageCacheCreateTemp, rawErr,
			primaryErr, requestedMode, hostInfo, entry, policyRes, "symlink detected in cache",
		)
	}

	if openErr != nil {
		if err := checkExtractionBudget(entry, idx); err != nil {
			return fmt.Errorf("%w: %w", format.ErrCacheExtract, err)
		}
		decompStart := time.Now()
		cachedBinary, matErr := cache.MaterializeVariantAtFD(dirFD, cacheDir, entry, func(w io.Writer) error {
			return extractVariantToWriter(selfFile, entry, idx, w)
		})
		if matErr != nil {
			stage := format.StageCacheCreateTemp
			sentinel := format.ErrCacheWrite
			if errors.Is(matErr, format.ErrCacheExtract) || isPayloadCorruptionOrDecompressionError(matErr) {
				stage = format.StageCacheExtract
				sentinel = format.ErrCacheExtract
			}
			return handleCacheError(
				sentinel, stage, matErr,
				primaryErr, requestedMode, hostInfo, entry, policyRes, "materializing cache binary failed",
			)
		}
		decompDuration = time.Since(decompStart)

		// Re-open with canonical descriptor-bound primitive and validate before execve
		fd, openErr = openAndValidateCacheAtFD(dirFD, cachedName, entry)
		if openErr != nil {
			_ = unix.Unlinkat(dirFD, cachedName, 0)
			rawErr := fmt.Errorf("opening verified cache file %s: %w", cachedBinary, openErr)
			return handleCacheError(
				format.ErrCacheWrite, format.StageCacheCreateTemp, rawErr,
				primaryErr, requestedMode, hostInfo, entry, policyRes, "opening verified cache file failed",
			)
		}
	}

	defer func() { _ = unix.Close(fd) }()

	logDiagnostics(entry, format.ExecModeCache, hostInfo, policyRes, env, limits, decompDuration, time.Since(startTime))

	procPath := "/proc/self/fd/" + strconv.Itoa(fd)
	execErr := execveFunc(procPath, args, env)
	if execErr == nil {
		return nil
	}
	return handleCacheError(
		format.ErrExecve, format.StageCacheExec, execErr,
		primaryErr, requestedMode, hostInfo, entry, policyRes, procPath,
	)
}

func createTempFileAt(dirFD int, dirPath string) (int, string, string, error) {
	var rnd [8]byte
	for range maxTempFileAttempts {
		if _, err := io.ReadFull(cryptoRandReader, rnd[:]); err != nil {
			return -1, "", "", fmt.Errorf("generating random suffix: %w", err)
		}
		name := fmt.Sprintf(".exec-%s.tmp", hex.EncodeToString(rnd[:]))
		fd, err := unix.Openat(dirFD, name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC, privateExecMode)
		if err == nil {
			return fd, name, filepath.Join(dirPath, name), nil
		}
		if !errors.Is(err, unix.EEXIST) && !errors.Is(err, syscall.EEXIST) {
			return -1, "", "", err
		}
	}
	return -1, "", "", errors.New("failed to create temporary file in cache directory")
}

func openAndValidateCacheAtFD(dirFD int, name string, entry *format.VariantEntry) (int, error) {
	fd, err := cache.OpenAndValidateVariantAtFDWithOpener(dirFD, name, entry, true, openCachedBinaryAtFunc)
	if err != nil && (os.Getenv(format.EnvDebug) == "1" || strings.EqualFold(os.Getenv(format.EnvDebug), "true")) {
		if errors.Is(err, cache.ErrSizeMismatch) {
			fmt.Fprintf(
				os.Stderr,
				"[microfat:debug] truncated cache file detected (%s), re-extracting\n",
				name,
			)
		} else if errors.Is(err, format.ErrPayloadCorrupted) {
			fmt.Fprintf(
				os.Stderr,
				"[microfat:debug] corrupted cache file detected (checksum mismatch in %s), re-extracting\n",
				name,
			)
		}
	}
	return fd, err
}

// openAndValidateCacheFD opens the cached binary path and delegates to the canonical descriptor-bound
// security primitive in internal/cache, ensuring identical invariants across launcher and pack subsystems:
// 1. Opens with O_RDONLY | O_CLOEXEC | O_NOFOLLOW to prevent symlink traversal.
// 2. Asserts via Fstat that the descriptor points to a regular file (S_IFREG) and matches entry.UncompressedSize.
// 3. Streams SHA-256 verification via Pread directly on the open descriptor, matching entry.SHA256.
// 4. On failure: closes descriptor, removes corrupted file from disk, and returns an explicit error.
// 5. On success: returns the pinned, validated descriptor ready for direct execve("/proc/self/fd/<fd>").
func openAndValidateCacheFD(path string, entry *format.VariantEntry) (int, error) {
	fd, err := cache.OpenAndValidateVariantFDWithOpener(path, entry, true, openCachedBinaryFunc)
	if err != nil && (os.Getenv(format.EnvDebug) == "1" || strings.EqualFold(os.Getenv(format.EnvDebug), "true")) {
		if errors.Is(err, cache.ErrSizeMismatch) {
			fmt.Fprintf(
				os.Stderr,
				"[microfat:debug] truncated cache file detected (%s), re-extracting\n",
				path,
			)
		} else if errors.Is(err, format.ErrPayloadCorrupted) {
			fmt.Fprintf(
				os.Stderr,
				"[microfat:debug] corrupted cache file detected (checksum mismatch in %s), re-extracting\n",
				path,
			)
		}
	}
	return fd, err
}
