//go:build linux

package main

import (
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
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"golang.org/x/sys/unix"
)

const (
	privateCacheDirMode = 0o700
	privateExecMode     = 0o700
	extraEnvCapacity    = 16
	// memfdTargetSeals defines the mandatory Linux kernel memory file descriptor seals applied to
	// anonymous RAM payloads prior to execution via /proc/self/fd/<fd>.
	// - F_SEAL_WRITE: prevents any modification of the decompressed binary code in memory.
	// - F_SEAL_SHRINK & F_SEAL_GROW: prevents truncation or expansion of the memory region.
	// - F_SEAL_SEAL: permanently locks the seal set, preventing any further seals or unsealing.
	// This ensures payload integrity and memory safety against runtime tampering or write races.
	memfdTargetSeals = unix.F_SEAL_WRITE | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_SEAL
)

var (
	execveFunc           = syscall.Exec
	memfdCreateFunc      = unix.MemfdCreate
	memfdSealFunc        = func(fd int, seals int) error {
		_, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, seals)
		return err
	}
	readCgroupLimitsFunc = cgroup.ReadLimits
	resolveCacheDirFunc  = format.ResolveCacheDir
	userHomeDirFunc      = os.UserHomeDir
	openCachedBinaryFunc = func(path string) (int, error) {
		return cache.OpenFileFunc(path)
	}
)

// extractVariantToWriter seeks to the variant offset and streams decompressed bytes to w,
// verifying the payload SHA-256 digest concurrently during decompression.
func extractVariantToWriter(selfFile *os.File, entry *format.VariantEntry, idx *format.Index, w io.Writer) error {
	c, err := codec.Get(entry.Compression)
	if err != nil {
		return fmt.Errorf("lookup codec %q for variant %s: %w", entry.Compression, entry.Level, err)
	}

	var dictBytes []byte
	if idx != nil && idx.DictionarySize > 0 {
		if idx.DictionarySize > format.MaxDictionarySize || idx.DictionaryOffset < 0 {
			return fmt.Errorf("%w: dictionary size %d or offset %d out of bounds",
				format.ErrInvalidDictionary, idx.DictionarySize, idx.DictionaryOffset)
		}
		dictBytes = make([]byte, idx.DictionarySize)
		if _, err := selfFile.ReadAt(dictBytes, idx.DictionaryOffset); err != nil {
			return fmt.Errorf("reading shared dictionary: %w", err)
		}
		if idx.DictionarySHA256 != "" {
			h := sha256.Sum256(dictBytes)
			actualHex := hex.EncodeToString(h[:])
			if actualHex != idx.DictionarySHA256 {
				return fmt.Errorf("%w: expected %s, got %s", format.ErrDictionaryCorrupted, idx.DictionarySHA256, actualHex)
			}
		}
	}

	secReader := io.NewSectionReader(selfFile, entry.Offset, entry.CompressedSize)
	hasher := sha256.New()
	mw := io.MultiWriter(w, hasher)
	if err := codec.DecompressWithOptionalDict(c, mw, secReader, entry.UncompressedSize, dictBytes); err != nil {
		return fmt.Errorf("decompressing variant payload: %w", err)
	}

	if entry.SHA256 != "" {
		actualHex := hex.EncodeToString(hasher.Sum(nil))
		if actualHex != entry.SHA256 {
			return fmt.Errorf("%w: expected %s, got %s", format.ErrPayloadCorrupted, entry.SHA256, actualHex)
		}
	}

	return nil
}

// executeVariant runs the selected variant payload in-memory using Linux memfd_create,
// falling back to user cache execution if memfd is restricted or if cache mode is explicitly requested.
func executeVariant(
	selfFile *os.File,
	entry *format.VariantEntry,
	idx *format.Index,
	args []string,
	baseEnv []string,
	hostInfo microarch.Info,
	policyRes microarch.PolicyResult,
	startTime time.Time,
) error {
	// Check if cache dispatch mode is explicitly requested via environment
	requestedMode := os.Getenv(format.EnvExecMode)
	if requestedMode == "" {
		requestedMode = os.Getenv(format.EnvDispatchMode)
	}

	if strings.EqualFold(requestedMode, format.ExecModeCache) {
		return executeViaCache(selfFile, entry, idx, args, baseEnv, hostInfo, policyRes, nil, startTime)
	}

	// 1. Try In-Memory memfd_create
	err := executeViaMemfd(selfFile, entry, idx, args, baseEnv, hostInfo, policyRes, startTime)
	if err == nil {
		return nil
	}

	// If explicit memfd execution mode was requested, fail fast without fallback
	if strings.EqualFold(requestedMode, format.ExecModeMemfd) {
		return err
	}

	// If fat binary payload or dictionary is corrupted, fail fast without attempting fallback
	if errors.Is(err, format.ErrPayloadCorrupted) || errors.Is(err, format.ErrDictionaryCorrupted) {
		return err
	}

	// 2. Fallback to cached file execution
	return executeViaCache(selfFile, entry, idx, args, baseEnv, hostInfo, policyRes, err, startTime)
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

func buildAutoTunedEnviron(
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
		if idx, exists := keyIndex[k]; exists {
			env[idx] = e
		} else {
			keyIndex[k] = len(env)
			env = append(env, e)
		}
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

	env = upsertEnv(env, keyIndex, format.EnvCgroupVersion, strconv.Itoa(limits.CgroupVersion))
	env = upsertEnv(env, keyIndex, format.EnvCgroupLimitBytes, strconv.FormatInt(limits.MemoryLimitBytes, 10))
	env = upsertEnv(env, keyIndex, format.EnvCgroupCPUs, fmt.Sprintf("%.2f", limits.CPUQuota))

	gcProfile, _ := cgroup.ParseGCProfile(os.Getenv(format.EnvGCProfile))
	liveHeap, _ := cgroup.ParseByteSize(os.Getenv(format.EnvLiveHeapEstimate))

	plan := cgroup.ResolveTuningPlanWithProfile(
		limits,
		os.Getenv(format.EnvMemRatio),
		cgroup.DefaultMemoryRatio,
		cgroup.DefaultMinHeadroomBytes,
		gcProfile,
		liveHeap,
	)
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

	// Check if user opted out of auto-tuning
	autoTuneOpt := os.Getenv(format.EnvAutotune)
	if autoTuneOpt == "0" || strings.EqualFold(autoTuneOpt, "false") {
		return env, &limits
	}

	if _, hasMem := keyIndex["GOMEMLIMIT"]; !hasMem && plan.GOMEMLIMITStr != "" {
		env = upsertEnv(env, keyIndex, "GOMEMLIMIT", plan.GOMEMLIMITStr)
	}
	if _, hasProcs := keyIndex["GOMAXPROCS"]; !hasProcs && plan.GOMAXPROCSStr != "" {
		env = upsertEnv(env, keyIndex, "GOMAXPROCS", plan.GOMAXPROCSStr)
	}
	if _, hasGC := keyIndex["GOGC"]; !hasGC && plan.GOGCApplied && plan.GOGCStr != "" {
		env = upsertEnv(env, keyIndex, "GOGC", plan.GOGCStr)
	}

	return env, &limits
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
		if strings.HasPrefix(e, "GOMEMLIMIT=") {
			memLimit = strings.TrimPrefix(e, "GOMEMLIMIT=")
		}
		if strings.HasPrefix(e, "GOMAXPROCS=") {
			maxProcs = strings.TrimPrefix(e, "GOMAXPROCS=")
		}
		if strings.HasPrefix(e, "GOGC=") {
			gogcVal = strings.TrimPrefix(e, "GOGC=")
		}
		if strings.HasPrefix(e, format.EnvCgroupGCProfile+"=") {
			gcProfileVal = strings.TrimPrefix(e, format.EnvCgroupGCProfile+"=")
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
	hint := format.DiagnoseError(stage, err)

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
			Error:             err.Error(),
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

// executeViaMemfd creates an anonymous, in-memory ELF file descriptor using memfd_create with MFD_ALLOW_SEALING,
// decompresses the selected variant payload into RAM, verifies its SHA-256 payload digest, applies mandatory
// descriptor seals (F_SEAL_WRITE | F_SEAL_SHRINK | F_SEAL_GROW | F_SEAL_SEAL) to guarantee immutability,
// and replaces the process image via execve on /proc/self/fd/<fd>.
// If memfd creation or sealing fails (e.g. due to restrictive seccomp profiles or unsupported kernel versions),
// it returns an error allowing auto-dispatch to fall back to hardened descriptor-bound cache execution.
func executeViaMemfd(
	selfFile *os.File,
	entry *format.VariantEntry,
	idx *format.Index,
	args []string,
	baseEnv []string,
	hostInfo microarch.Info,
	policyRes microarch.PolicyResult,
	startTime time.Time,
) error {
	env, limits := buildAutoTunedEnviron(baseEnv, entry, format.ExecModeMemfd, hostInfo, policyRes)

	fd, err := memfdCreateFunc("microfat_payload", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		logErrorDiagnostics(format.StageMemfdCreate, err, hostInfo, entry, policyRes, "falling back to disk cache")
		return fmt.Errorf("%w: memfd_create failed: %w", format.ErrMemfdCreate, err)
	}

	memFile := os.NewFile(uintptr(fd), "microfat_payload")
	defer func() { _ = memFile.Close() }()

	decompStart := time.Now()
	if err := extractVariantToWriter(selfFile, entry, idx, memFile); err != nil {
		logErrorDiagnostics(format.StageMemfdExtract, err, hostInfo, entry, policyRes, "decompressing payload failed")
		return fmt.Errorf("decompressing into memfd: %w", err)
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

func executeViaCache(
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
	env, limits := buildAutoTunedEnviron(baseEnv, entry, format.ExecModeCache, hostInfo, policyRes)

	cacheDir, err := resolveCacheDirFunc("")
	if err != nil {
		errOut := fmt.Errorf("%w: launcher execution failed: unable to initialize cache: %w (primary memfd error: %v)",
			format.ErrCacheInit, err, primaryErr)
		logErrorDiagnostics(format.StageCacheDirInit, errOut, hostInfo, entry, policyRes, "cache directory creation failed")
		return errOut
	}

	if !format.ValidateChecksum(entry.SHA256) || entry.SHA256 == "" {
		errOut := fmt.Errorf("%w: launcher execution failed: invalid variant sha256 checksum format %q",
			format.ErrCacheWrite, entry.SHA256)
		logErrorDiagnostics(format.StageCacheCreateTemp, errOut, hostInfo, entry, policyRes, "invalid cache filename")
		return errOut
	}

	cachedBinary := filepath.Join(cacheDir, filepath.Clean(entry.SHA256))
	var decompDuration time.Duration

	fd, openErr := openAndValidateCacheFD(cachedBinary, entry)
	if openErr != nil && (errors.Is(openErr, syscall.ELOOP) || errors.Is(openErr, unix.ELOOP)) {
		errOut := fmt.Errorf("%w: refusal to execute symlink at %s: %w", format.ErrCacheWrite, cachedBinary, openErr)
		logErrorDiagnostics(format.StageCacheCreateTemp, errOut, hostInfo, entry, policyRes, "symlink detected in cache")
		return errOut
	}

	if openErr != nil {
		tmpFile, err := os.CreateTemp(cacheDir, ".exec-*.tmp")
		if err != nil {
			errOut := fmt.Errorf("%w: launcher execution failed: cannot create temp file in %s: %w (primary memfd error: %v)",
				format.ErrCacheWrite, cacheDir, err, primaryErr)
			logErrorDiagnostics(format.StageCacheCreateTemp, errOut, hostInfo, entry, policyRes, "temp file creation failed in "+cacheDir)
			return errOut
		}
		tmpPath := tmpFile.Name()
		defer func() {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}()

		decompStart := time.Now()
		if err := extractVariantToWriter(selfFile, entry, idx, tmpFile); err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
			logErrorDiagnostics(format.StageCacheExtract, err, hostInfo, entry, policyRes, "decompressing payload to cache failed")
			return fmt.Errorf("%w: extracting to cache fallback: %w (primary memfd error: %v)", format.ErrCacheExtract, err, primaryErr)
		}
		decompDuration = time.Since(decompStart)

		if err := tmpFile.Chmod(format.PrivateExecMode); err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
			errOut := fmt.Errorf("%w: setting permissions on temp cache file %s: %w (primary memfd error: %v)",
				format.ErrCacheWrite, tmpPath, err, primaryErr)
			logErrorDiagnostics(format.StageCacheCreateTemp, errOut, hostInfo, entry, policyRes, "chmod temp cache file failed")
			return errOut
		}
		if err := tmpFile.Sync(); err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
			errOut := fmt.Errorf("%w: syncing temp cache file %s: %w (primary memfd error: %v)",
				format.ErrCacheWrite, tmpPath, err, primaryErr)
			logErrorDiagnostics(format.StageCacheCreateTemp, errOut, hostInfo, entry, policyRes, "syncing temp cache file failed")
			return errOut
		}
		if err := tmpFile.Close(); err != nil {
			_ = os.Remove(tmpPath)
			errOut := fmt.Errorf("%w: closing temp cache file %s: %w (primary memfd error: %v)",
				format.ErrCacheWrite, tmpPath, err, primaryErr)
			logErrorDiagnostics(format.StageCacheCreateTemp, errOut, hostInfo, entry, policyRes, "closing temp cache file failed")
			return errOut
		}
		if err := os.Rename(tmpPath, cachedBinary); err != nil {
			_ = os.Remove(tmpPath)
			errOut := fmt.Errorf("%w: renaming temp cache file %s to %s: %w (primary memfd error: %v)",
				format.ErrCacheWrite, tmpPath, cachedBinary, err, primaryErr)
			logErrorDiagnostics(format.StageCacheCreateTemp, errOut, hostInfo, entry, policyRes, "renaming temp cache file failed")
			return errOut
		}

		// Re-open with canonical descriptor-bound primitive and validate before execve
		fd, openErr = openAndValidateCacheFD(cachedBinary, entry)
		if openErr != nil {
			_ = os.Remove(cachedBinary)
			errOut := fmt.Errorf("%w: opening verified cache file %s: %w (primary memfd error: %v)",
				format.ErrCacheWrite, cachedBinary, openErr, primaryErr)
			logErrorDiagnostics(format.StageCacheCreateTemp, errOut, hostInfo, entry, policyRes, "opening verified cache file failed")
			return errOut
		}
	}

	defer func() { _ = unix.Close(fd) }()

	logDiagnostics(entry, format.ExecModeCache, hostInfo, policyRes, env, limits, decompDuration, time.Since(startTime))

	procPath := "/proc/self/fd/" + strconv.Itoa(fd)
	execErr := execveFunc(procPath, args, env)
	if execErr == nil {
		return nil
	}
	logErrorDiagnostics(format.StageCacheExec, execErr, hostInfo, entry, policyRes, "execve failed on cached binary "+procPath)
	if primaryErr != nil {
		return fmt.Errorf("%w: cache fallback execve failed (%s): %w (primary memfd error: %v)",
			format.ErrExecve, procPath, execErr, primaryErr)
	}
	return fmt.Errorf("%w: cache execve failed (%s): %w", format.ErrExecve, procPath, execErr)
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

