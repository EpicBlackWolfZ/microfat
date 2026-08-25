// Package main provides the microfat launcher stub.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghostnetorg/microfat/internal/format"
	"github.com/ghostnetorg/pkg/cgroup"
	"github.com/ghostnetorg/pkg/microarch"
)

const (
	flagInfo          = "--microfat:info"
	flagOptimize      = "--microfat:optimize"
	flagOptimizeTo    = "--microfat:optimize-to"
	flagHelp          = "--microfat:help"
	minOptimizeToArgs = 2
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "[microfat] error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	selfPath, err := getSelfExecutablePath()
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}

	// #nosec G304 -- launcher opens its own binary image to read payload index
	selfFile, err := os.Open(filepath.Clean(selfPath))
	if err != nil {
		return fmt.Errorf("opening executable %s: %w", selfPath, err)
	}
	defer func() { _ = selfFile.Close() }()

	stat, err := selfFile.Stat()
	if err != nil {
		return fmt.Errorf("stat executable: %w", err)
	}

	if !format.IsFatBinary(selfFile, stat.Size()) {
		return fmt.Errorf("file %s is not a valid microfat fat binary (missing magic trailer)", selfPath)
	}

	idx, err := format.ReadTrailerAndIndex(selfFile, stat.Size())
	if err != nil {
		return fmt.Errorf("reading binary manifest: %w", err)
	}

	hostInfo := microarch.Detect()
	selectedLevel, err := microarch.BestMatchingVariantFor(idx.TargetArch, hostInfo.Level, idx.VariantLevels())
	if err != nil {
		return fmt.Errorf("selecting compatible CPU microarchitecture variant: %w", err)
	}

	selectedEntry, found := idx.FindVariant(selectedLevel)
	if !found {
		return fmt.Errorf("internal error: matched variant %s not present in index", selectedLevel)
	}

	// Check for meta-commands
	if len(os.Args) > 1 {
		arg1 := os.Args[1]
		switch {
		case arg1 == flagHelp:
			printHelp(idx, hostInfo, selectedEntry)
			return nil
		case arg1 == flagInfo:
			printInfo(idx, hostInfo, selectedEntry, stat.Size())
			return nil
		case arg1 == flagOptimize:
			fmt.Printf("[microfat] Optimizing binary in-place to variant '%s'...\n", selectedLevel)
			if err := optimizeInPlace(selfPath, selfFile, selectedEntry); err != nil {
				return err
			}
			fmt.Printf("[microfat] Successfully replaced '%s' with %s binary (%d bytes -> %d bytes)\n",
				selfPath, selectedLevel, stat.Size(), selectedEntry.UncompressedSize)
			return nil
		case strings.HasPrefix(arg1, flagOptimizeTo+"=") || arg1 == flagOptimizeTo:
			var targetPath string
			switch {
			case strings.HasPrefix(arg1, flagOptimizeTo+"="):
				targetPath = strings.TrimPrefix(arg1, flagOptimizeTo+"=")
			case len(os.Args) > minOptimizeToArgs:
				targetPath = os.Args[minOptimizeToArgs]
			default:
				return fmt.Errorf("%s requires a destination path", flagOptimizeTo)
			}
			fmt.Printf("[microfat] Extracting variant '%s' to '%s'...\n", selectedLevel, targetPath)
			if err := optimizeTo(targetPath, selfFile, selectedEntry); err != nil {
				return err
			}
			fmt.Printf("[microfat] Successfully materialized '%s' (%d bytes)\n", targetPath, selectedEntry.UncompressedSize)
			return nil
		}
	}

	// Standard transparent execution
	return executeVariant(selfFile, selectedEntry, os.Args, os.Environ())
}

func getSelfExecutablePath() (string, error) {
	// On Linux, /proc/self/exe is the most reliable
	if fi, err := os.Lstat("/proc/self/exe"); err == nil && (fi.Mode()&os.ModeSymlink != 0) {
		return "/proc/self/exe", nil
	}
	return os.Executable()
}

func printHelp(idx *format.Index, hostInfo microarch.Info, selected *format.VariantEntry) {
	fmt.Printf("Microfat Universal Launcher\n")
	fmt.Printf("Application:   %s\n", idx.AppName)
	fmt.Printf("Target Arch:   %s/%s\n", idx.TargetOS, idx.TargetArch)
	fmt.Printf("Host CPU:      %s (%s level %s)\n", hostInfo.Arch, idx.TargetArch, hostInfo.Level)
	fmt.Printf("Auto-Selected: %s\n\n", selected.Level)
	fmt.Printf("Meta-Commands:\n")
	fmt.Printf("  --microfat:info              Print host CPU capabilities and embedded variant manifest\n")
	fmt.Printf("  --microfat:optimize          Permanently shrink and replace this executable with the optimal variant\n")
	fmt.Printf("  --microfat:optimize-to PATH  Extract the optimal variant to a specific file path (for containers/installs)\n")
	fmt.Printf("  --microfat:help              Show this launcher help message\n\n")
	fmt.Printf("All other arguments and flags are forwarded directly to the application.\n")
}

func printInfo(idx *format.Index, hostInfo microarch.Info, selected *format.VariantEntry, totalSize int64) {
	fmt.Printf("=== Microfat Binary Info ===\n")
	fmt.Printf("App Name:          %s\n", idx.AppName)
	fmt.Printf("Target Platform:   %s/%s\n", idx.TargetOS, idx.TargetArch)
	fmt.Printf("Fat Binary Size:   %d bytes\n", totalSize)
	fmt.Printf("Host Platform:     %s/%s\n", hostInfo.OS, hostInfo.Arch)
	fmt.Printf("Host CPU Level:    %s\n", hostInfo.Level)
	fmt.Printf("Selected Variant:  %s (%d bytes uncompressed)\n", selected.Level, selected.UncompressedSize)
	fmt.Printf("Execution Mode:    Linux memfd_create (anonymous RAM, 0 disk I/O)\n")

	// Print cgroup auto-tuning info
	if limits, err := cgroup.ReadLimits(); err == nil && limits.CgroupVersion != cgroup.VersionUnknown {
		fmt.Printf("\nContainer Auto-Tuning (cgroup v%d):\n", limits.CgroupVersion)
		if limits.MemoryLimitBytes > 0 {
			if memLimit, ok := cgroup.CalculateGOMEMLIMIT(limits.MemoryLimitBytes, cgroup.DefaultMemoryRatio, cgroup.DefaultMinHeadroomBytes); ok {
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
		fmt.Printf(" %s %-6s (offset: %10d | comp: %10d B | uncomp: %10d B | sha256: %.12s...)\n",
			isSel, v.Level, v.Offset, v.CompressedSize, v.UncompressedSize, v.SHA256)
	}
	fmt.Printf("\nFeatures Detected on Host:\n  %s\n", strings.Join(hostInfo.Features, ", "))
}
