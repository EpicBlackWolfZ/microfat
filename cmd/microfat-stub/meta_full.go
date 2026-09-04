//go:build !minimal

// Package main implements developer launcher stub meta-command routing and CLI handlers.
package main

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

const (
	flagInfo          = "--microfat:info"
	flagOptimize      = "--microfat:optimize"
	flagOptimizeTo    = "--microfat:optimize-to"
	flagTrim          = "--microfat:trim"
	flagTrimTo        = "--microfat:trim-to"
	flagSpecialize    = "--microfat:specialize"
	flagSpecializeTo  = "--microfat:specialize-to"
	flagPrewarm       = "--microfat:prewarm"
	flagHelp          = "--microfat:help"
	minOptimizeToArgs = 2
)

func handleMetaCommand(
	arg1 string,
	selfPath string,
	selfFile *os.File,
	statSize int64,
	idx *format.Index,
	hostInfo microarch.Info,
	selectedEntry *format.VariantEntry,
	policyRes microarch.PolicyResult,
) (bool, error) {
	switch {
	case arg1 == flagHelp:
		printHelp(idx, hostInfo, selectedEntry, policyRes)
		return true, nil
	case isPrefixOrExact(arg1, flagInfo):
		jsonOutput := arg1 == flagInfo+"=json" || hasJSONFlag(os.Args[2:])
		return true, printInfo(idx, hostInfo, selectedEntry, policyRes, statSize, jsonOutput)
	case arg1 == flagTrim || arg1 == flagSpecialize:
		return true, handleTrimInPlace(selfPath, selfFile, statSize, selectedEntry.Level)
	case isPrefixOrExact(arg1, flagTrimTo) || isPrefixOrExact(arg1, flagSpecializeTo):
		return true, handleTrimTo(arg1, selfFile, statSize, selectedEntry.Level)
	case arg1 == flagOptimize:
		return true, handleOptimizeInPlace(selfPath, selfFile, statSize, selectedEntry, idx)
	case isPrefixOrExact(arg1, flagOptimizeTo):
		return true, handleOptimizeTo(arg1, selfFile, selectedEntry, idx)
	case isPrefixOrExact(arg1, flagPrewarm):
		jsonOutput := strings.HasSuffix(arg1, "=json") || hasJSONFlag(os.Args[2:])
		verifyMode := strings.Contains(arg1, "verify") || hasVerifyFlag(os.Args[2:])
		return true, prewarmStub(selfFile, statSize, idx, selectedEntry.Level, arg1, jsonOutput, verifyMode)
	default:
		return false, nil
	}
}

func handleTrimInPlace(selfPath string, selfFile *os.File, statSize int64, selectedLevel string) error {
	fmt.Printf("[microfat] Trimming binary in-place to variant '%s' (keeping launcher stub & auto-tuning)...\n", selectedLevel)
	if err := trimInPlace(selfPath, selfFile, statSize, selectedLevel); err != nil {
		return err
	}
	newStat, _ := os.Stat(selfPath)
	var newSize int64
	if newStat != nil {
		newSize = newStat.Size()
	}
	fmt.Printf("[microfat] Successfully trimmed '%s' (%d bytes -> %d bytes)\n", selfPath, statSize, newSize)
	return nil
}

func handleTrimTo(arg1 string, selfFile *os.File, statSize int64, selectedLevel string) error {
	targetPath, err := extractTargetPath(arg1, flagTrimTo, flagSpecializeTo)
	if err != nil {
		return err
	}
	cleanTarget := filepath.Clean(targetPath)
	fmt.Printf("[microfat] Trimming variant '%s' to '%s' (single-variant fat binary)...\n", selectedLevel, cleanTarget)
	if err := trimTo(cleanTarget, selfFile, statSize, selectedLevel); err != nil {
		return err
	}
	// #nosec G703 -- stat user-supplied path for size reporting
	newStat, _ := os.Stat(cleanTarget)
	var newSize int64
	if newStat != nil {
		newSize = newStat.Size()
	}
	fmt.Printf("[microfat] Successfully created trimmed fat binary '%s' (%d bytes)\n", cleanTarget, newSize)
	return nil
}

func handleOptimizeInPlace(
	selfPath string, selfFile *os.File, statSize int64, selectedEntry *format.VariantEntry, idx *format.Index,
) error {
	fmt.Printf("[microfat] Optimizing binary in-place to variant '%s' (raw uncompressed ELF)...\n", selectedEntry.Level)
	if err := optimizeInPlace(selfPath, selfFile, selectedEntry, idx); err != nil {
		return err
	}
	fmt.Printf("[microfat] Successfully replaced '%s' with %s binary (%d bytes -> %d bytes)\n",
		selfPath, selectedEntry.Level, statSize, selectedEntry.UncompressedSize)
	return nil
}

func handleOptimizeTo(arg1 string, selfFile *os.File, selectedEntry *format.VariantEntry, idx *format.Index) error {
	targetPath, err := extractTargetPath(arg1, flagOptimizeTo, "")
	if err != nil {
		return err
	}
	fmt.Printf("[microfat] Extracting variant '%s' to '%s'...\n", selectedEntry.Level, targetPath)
	if err := optimizeTo(targetPath, selfFile, selectedEntry, idx); err != nil {
		return err
	}
	fmt.Printf("[microfat] Successfully materialized '%s' (%d bytes)\n", targetPath, selectedEntry.UncompressedSize)
	return nil
}

func prewarmStub(
	selfFile *os.File,
	statSize int64,
	idx *format.Index,
	defaultLevel string,
	arg string,
	jsonOutput bool,
	verifyOnly bool,
) error {
	targetLevels, parsedJSON, parsedVerify, err := parsePrewarmArgs(arg, defaultLevel, idx)
	if err != nil {
		return err
	}
	if parsedJSON {
		jsonOutput = true
	}
	if parsedVerify {
		verifyOnly = true
	}

	cacheDir, err := resolveCacheDirFunc("")
	if err != nil {
		return fmt.Errorf("resolving cache directory: %w", err)
	}

	if verifyOnly {
		return handlePrewarmVerify(selfFile, statSize, idx, targetLevels, cacheDir, jsonOutput)
	}
	return handlePrewarmExtract(selfFile, statSize, idx, targetLevels, cacheDir, jsonOutput)
}

func parsePrewarmArgs(arg, defaultLevel string, idx *format.Index) ([]string, bool, bool, error) {
	var targetLevels []string
	var jsonOutput, verifyOnly bool
	seen := make(map[string]struct{})

	addLevel := func(lvl string) {
		if _, exists := seen[lvl]; !exists {
			seen[lvl] = struct{}{}
			targetLevels = append(targetLevels, lvl)
		}
	}

	if strings.HasPrefix(arg, flagPrewarm+"=") {
		val := strings.TrimPrefix(arg, flagPrewarm+"=")
		tokens := strings.Split(val, ",")
		for _, token := range tokens {
			token = strings.TrimSpace(token)
			switch {
			case strings.EqualFold(token, "verify"):
				verifyOnly = true
			case strings.EqualFold(token, "all"):
				if idx != nil {
					for _, lvl := range idx.VariantLevels() {
						addLevel(lvl)
					}
				}
			case strings.EqualFold(token, "json"):
				jsonOutput = true
			case token != "":
				if idx == nil {
					return nil, false, false, fmt.Errorf("variant level %q not found in binary manifest", token)
				}
				entry, found := idx.FindVariant(token)
				if !found {
					return nil, false, false, fmt.Errorf("variant level %q not found in binary manifest", token)
				}
				addLevel(entry.Level)
			}
		}
	}
	if len(targetLevels) == 0 && defaultLevel != "" {
		canonicalDefault := defaultLevel
		if idx != nil {
			if entry, found := idx.FindVariant(defaultLevel); found {
				canonicalDefault = entry.Level
			}
		}
		targetLevels = []string{canonicalDefault}
	}
	return targetLevels, jsonOutput, verifyOnly, nil
}

func handlePrewarmVerify(
	selfFile *os.File,
	statSize int64,
	idx *format.Index,
	targetLevels []string,
	cacheDir string,
	jsonOutput bool,
) error {
	_, results, err := pack.VerifyCacheBinary(selfFile, statSize, targetLevels, cacheDir)
	if err != nil {
		return err
	}

	allValid := true
	for _, r := range results {
		if !r.Valid {
			allValid = false
			break
		}
	}

	if jsonOutput || strings.EqualFold(os.Getenv(format.EnvLog), "json") {
		telem := format.PrewarmTelemetry{
			Event:             format.EventPrewarm,
			TimestampUnixNano: time.Now().UnixNano(),
			AppName:           idx.AppName,
			CacheDir:          cacheDir,
			Results:           results,
		}
		if err := json.MarshalWrite(os.Stdout, telem, jsontext.WithIndent("  ")); err != nil {
			return fmt.Errorf("encoding json: %w", err)
		}
		fmt.Println()
		if !allValid {
			return errors.New("cache verification failed for one or more variants")
		}
		return nil
	}

	for _, r := range results {
		if r.Valid {
			fmt.Printf("[microfat] Cache valid for variant '%s' at '%s' (%d bytes, sha256: %.12s...)\n",
				r.Level, r.CachedPath, r.UncompressedSize, r.SHA256)
		} else {
			fmt.Printf("[microfat] Cache invalid for variant '%s' at '%s': status=%s (%s)\n",
				r.Level, r.CachedPath, r.Status, r.Error)
		}
	}

	if !allValid {
		return errors.New("cache verification failed for one or more variants")
	}
	return nil
}

func handlePrewarmExtract(
	selfFile *os.File,
	statSize int64,
	idx *format.Index,
	targetLevels []string,
	cacheDir string,
	jsonOutput bool,
) error {
	_, results, err := pack.PrewarmBinary(selfFile, statSize, targetLevels, cacheDir)
	if err != nil {
		return err
	}

	if jsonOutput || strings.EqualFold(os.Getenv(format.EnvLog), "json") {
		telem := format.PrewarmTelemetry{
			Event:             format.EventPrewarm,
			TimestampUnixNano: time.Now().UnixNano(),
			AppName:           idx.AppName,
			CacheDir:          cacheDir,
			Results:           results,
		}
		if err := json.MarshalWrite(os.Stdout, telem, jsontext.WithIndent("  ")); err != nil {
			return fmt.Errorf("encoding json: %w", err)
		}
		fmt.Println()
		return nil
	}

	for _, r := range results {
		if r.AlreadyCached {
			fmt.Printf("[microfat] Variant '%s' already cached at '%s'\n", r.Level, r.CachedPath)
		} else {
			fmt.Printf("[microfat] Prewarmed variant '%s' to cache at '%s' (%d bytes in %d µs)\n",
				r.Level, r.CachedPath, r.UncompressedSize, r.DecompressionUs)
		}
	}
	return nil
}

func hasJSONFlag(args []string) bool {
	for _, a := range args {
		if a == "--json" || a == "-json" {
			return true
		}
	}
	return false
}

func hasVerifyFlag(args []string) bool {
	for _, a := range args {
		if a == "--verify" || a == "-verify" {
			return true
		}
	}
	return false
}

func isPrefixOrExact(arg, flag string) bool {
	if flag == "" {
		return false
	}
	return arg == flag || strings.HasPrefix(arg, flag+"=")
}

func extractTargetPath(arg, primaryFlag, aliasFlag string) (string, error) {
	if strings.HasPrefix(arg, primaryFlag+"=") {
		return strings.TrimPrefix(arg, primaryFlag+"="), nil
	}
	if aliasFlag != "" && strings.HasPrefix(arg, aliasFlag+"=") {
		return strings.TrimPrefix(arg, aliasFlag+"="), nil
	}
	if len(os.Args) > minOptimizeToArgs {
		return os.Args[minOptimizeToArgs], nil
	}
	return "", fmt.Errorf("%s requires a destination path", primaryFlag)
}

func printHelp(idx *format.Index, hostInfo microarch.Info, selected *format.VariantEntry, policyRes microarch.PolicyResult) {
	fmt.Printf("Microfat Universal Launcher\n")
	fmt.Printf("Application:   %s\n", idx.AppName)
	fmt.Printf("Target Arch:   %s/%s\n", idx.TargetOS, idx.TargetArch)
	fmt.Printf("Host CPU:      %s (%s level %s)\n", hostInfo.Arch, idx.TargetArch, hostInfo.Level)
	fmt.Printf("Auto-Selected: %s\n", selected.Level)
	if policyRes.PolicyApplied != "" {
		fmt.Printf("Policy Override: %s (%s)\n", policyRes.PolicyApplied, policyRes.OverrideReason)
	}
	fmt.Println()
	fmt.Printf("Meta-Commands:\n")
	fmt.Printf("  --microfat:info [--json]     Print host CPU capabilities, cgroup auto-tuning limits, and variants\n")
	fmt.Printf("  --microfat:prewarm[=LVL]     Pre-extract optimal (or all) variants into cache and exit\n")
	fmt.Printf("  --microfat:trim              Trim unused variants on disk, keeping launcher stub & cgroup auto-tuning\n")
	fmt.Printf("  --microfat:trim-to PATH      Extract trimmed single-variant fat binary to a specific path\n")
	fmt.Printf("  --microfat:optimize          Permanently extract raw uncompressed variant ELF over this file\n")
	fmt.Printf("  --microfat:optimize-to PATH  Extract raw uncompressed variant ELF to a specific path\n")
	fmt.Printf("  --microfat:help              Show this launcher help message\n\n")
	fmt.Printf("Policy Environment Variables:\n")
	fmt.Printf("  MICROFAT_FORCE_LEVEL         Pin execution to a specific variant level (e.g. v1, v3)\n")
	fmt.Printf("  MICROFAT_MAX_LEVEL           Cap selection ceiling (e.g. v3)\n")
	fmt.Printf("  MICROFAT_DISABLE_VARIANTS    Comma-separated list of variants to skip (e.g. v4)\n")
	fmt.Printf("  MICROFAT_POLICY              Policy preset (e.g. safe_avx512, no_downclock)\n")
	fmt.Printf("  MICROFAT_AVX512_DOWNCLOCK_PROTECTION  Enable Intel Skylake-X/Cascade Lake downclock mitigation (1/true)\n")
	fmt.Printf("  MICROFAT_CACHE_DIR           Custom destination cache directory\n")
	fmt.Printf("  MICROFAT_EXEC_MODE           Execution mode: memfd (default) or cache\n\n")
	fmt.Printf("All other arguments and flags are forwarded directly to the application.\n")
}

func printInfo(
	idx *format.Index,
	hostInfo microarch.Info,
	selected *format.VariantEntry,
	policyRes microarch.PolicyResult,
	totalSize int64,
	jsonOutput bool,
) error {
	if jsonOutput {
		var cgInfo *format.CgroupInfo
		if limits, err := readCgroupLimitsFunc(); err == nil && limits.CgroupVersion != cgroup.VersionUnknown {
			cgInfo = &format.CgroupInfo{
				Version:          limits.CgroupVersion,
				MemoryLimitBytes: limits.MemoryLimitBytes,
				CPUQuota:         limits.CPUQuota,
				GOMAXPROCS:       limits.CPUs,
			}
			if limits.MemoryLimitBytes > 0 {
				if memLimit, ok := cgroup.CalculateGOMEMLIMIT(
					limits.MemoryLimitBytes, cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes); ok {
					cgInfo.GOMEMLIMIT = fmt.Sprintf("%dB", memLimit)
				}
			}
		}
		info := format.BinaryInfo{
			AppName:          idx.AppName,
			TargetOS:         idx.TargetOS,
			TargetArch:       idx.TargetArch,
			FatBinarySize:    totalSize,
			HostOS:           hostInfo.OS,
			HostArch:         hostInfo.Arch,
			HostLevel:        hostInfo.Level,
			SelectedVariant:  selected.Level,
			SelectedSize:     selected.UncompressedSize,
			ExecMode:         format.ExecModeMemfd,
			PolicyApplied:    policyRes.PolicyApplied,
			PolicyReason:     policyRes.OverrideReason,
			DictionaryOffset: idx.DictionaryOffset,
			DictionarySize:   idx.DictionarySize,
			DictionarySHA256: idx.DictionarySHA256,
			DictionaryID:     idx.DictionaryID,
			Cgroup:           cgInfo,
			Variants:         idx.Variants,
			HostFeatures:     hostInfo.Features,
		}
		if err := json.MarshalWrite(os.Stdout, info, jsontext.WithIndent("  ")); err != nil {
			return fmt.Errorf("encoding binary info json: %w", err)
		}
		fmt.Println()
		return nil
	}

	fmt.Printf("=== Microfat Binary Info ===\n")
	fmt.Printf("App Name:          %s\n", idx.AppName)
	fmt.Printf("Target Platform:   %s/%s\n", idx.TargetOS, idx.TargetArch)
	fmt.Printf("Fat Binary Size:   %d bytes\n", totalSize)
	if idx.DictionarySize > 0 {
		fmt.Printf("Shared Dictionary: %d bytes (offset %d, sha256: %.12s...)\n",
			idx.DictionarySize, idx.DictionaryOffset, idx.DictionarySHA256)
	}
	fmt.Printf("Host Platform:     %s/%s\n", hostInfo.OS, hostInfo.Arch)
	fmt.Printf("Host CPU Level:    %s\n", hostInfo.Level)
	fmt.Printf("Selected Variant:  %s (%d bytes uncompressed)\n", selected.Level, selected.UncompressedSize)
	if policyRes.PolicyApplied != "" {
		fmt.Printf("Policy Applied:    %s (%s)\n", policyRes.PolicyApplied, policyRes.OverrideReason)
	}
	fmt.Printf("Execution Mode:    Linux memfd_create (anonymous RAM, 0 disk I/O)\n")

	// Print cgroup auto-tuning info
	if limits, err := readCgroupLimitsFunc(); err == nil && limits.CgroupVersion != cgroup.VersionUnknown {
		fmt.Printf("\nContainer Auto-Tuning (cgroup v%d):\n", limits.CgroupVersion)
		if limits.MemoryLimitBytes > 0 {
			memLimit, ok := cgroup.CalculateGOMEMLIMIT(
				limits.MemoryLimitBytes, cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes)
			if ok {
				fmt.Printf("  • Memory Ceiling:  %d bytes -> Auto GOMEMLIMIT: %dB (90%%)\n", limits.MemoryLimitBytes, memLimit)
			}
		} else {
			fmt.Printf("  • Memory Ceiling:  unlimited\n")
		}
		if limits.CPUs > 0 {
			fmt.Printf("  • CPU CFS Quota:   %.2f cores -> Auto GOMAXPROCS: %d\n", limits.CPUQuota, limits.CPUs)
		} else {
			fmt.Printf("  • CPU CFS Quota:   unlimited\n")
		}
	}

	fmt.Printf("\nEmbedded Variants (%d total):\n", len(idx.Variants))
	for _, v := range idx.Variants {
		isSel := " "
		if v.Level == selected.Level {
			isSel = "*"
		}
		fmt.Printf(" %s %-6s [%s] (offset: %10d | comp: %10d B | uncomp: %10d B | sha256: %.12s...)\n",
			isSel, v.Level, v.Compression, v.Offset, v.CompressedSize, v.UncompressedSize, v.SHA256)
	}
	fmt.Printf("\nFeatures Detected on Host:\n  %s\n", strings.Join(hostInfo.Features, ", "))
	return nil
}
