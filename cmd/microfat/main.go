// Package main provides the microfat CLI developer and CI toolkit.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/builder"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/EpicBlackWolfZ/microfat/internal/version"
	"github.com/spf13/cobra"
)

const (
	percentMultiplier = 100.0
	keyValueParts     = 2
)

var exitFunc = os.Exit

func main() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		exitFunc(1)
	}
}

func newRootCmd() *cobra.Command {
	var showVersion bool

	cmd := &cobra.Command{
		Use:   "microfat",
		Short: "Microfat - Dynamic CPU Microarchitecture Optimization and Packaging Tool",
		Long: `Microfat combines multiple microarchitecture-specific Go ELF binaries into a single,
self-dispatching fat executable with zero persistent process overhead and cryptographically verified integrity.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				fmt.Println(version.Info())
				return nil
			}
			return cmd.Help()
		},
	}

	cmd.Flags().BoolVarP(&showVersion, "version", "v", false, "Print version and build info")

	cmd.AddCommand(newDetectCmd())
	cmd.AddCommand(newInspectCmd())
	cmd.AddCommand(newVerifyCmd())
	cmd.AddCommand(newTrimCmd())
	cmd.AddCommand(newPackCmd())
	cmd.AddCommand(newPgoPackCmd())
	cmd.AddCommand(newPrewarmCmd())

	return cmd
}

func newDetectCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "detect",
		Short: "Detect host CPU microarchitecture level and features",
		RunE: func(cmd *cobra.Command, args []string) error {
			info := microarch.Detect()
			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(info)
			}

			fmt.Printf("OS:       %s\n", info.OS)
			fmt.Printf("Arch:     %s\n", info.Arch)
			fmt.Printf("Level:    %s\n", info.Level)
			fmt.Printf("Features: %s\n", strings.Join(info.Features, ", "))
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results in JSON format")
	return cmd
}

func newInspectCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "inspect <binary>",
		Short: "Inspect embedded variants and metadata inside a microfat binary",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := filepath.Clean(args[0])
			// #nosec G304 -- user-supplied binary path to inspect
			f, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("opening %s: %w", path, err)
			}
			defer func() { _ = f.Close() }()

			stat, err := f.Stat()
			if err != nil {
				return fmt.Errorf("stat %s: %w", path, err)
			}

			if !format.IsFatBinary(f, stat.Size()) {
				return fmt.Errorf("'%s' is not a valid microfat fat binary (missing magic trailer)", path)
			}

			idx, err := format.ReadTrailerAndIndex(f, stat.Size())
			if err != nil {
				return fmt.Errorf("reading binary manifest: %w", err)
			}

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(idx)
			}

			fmt.Printf("Binary Path:       %s\n", path)
			fmt.Printf("App Name:          %s\n", idx.AppName)
			fmt.Printf("Target Platform:   %s/%s\n", idx.TargetOS, idx.TargetArch)
			fmt.Printf("Total Size:        %d bytes\n", stat.Size())
			fmt.Printf("Created At:        %s\n\n", time.Unix(idx.CreatedUnix, 0).Format(time.RFC3339))

			fmt.Printf("Embedded Variants (%d total):\n", len(idx.Variants))
			for _, v := range idx.Variants {
				ratio := float64(v.CompressedSize) / float64(v.UncompressedSize) * percentMultiplier
				fmt.Printf("  • %-6s offset: %10d | comp: %10d B | raw: %10d B (%.1f%%) | sha256: %.12s...\n",
					v.Level, v.Offset, v.CompressedSize, v.UncompressedSize, ratio, v.SHA256)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results in JSON format")
	return cmd
}

func newVerifyCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "verify <binary>",
		Short: "Validate trailer, index SHA-256 hash, and payload integrity of all embedded variants",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := filepath.Clean(args[0])
			// #nosec G304 -- user-supplied binary path to verify
			f, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("opening %s: %w", path, err)
			}
			defer func() { _ = f.Close() }()

			stat, err := f.Stat()
			if err != nil {
				return fmt.Errorf("stat %s: %w", path, err)
			}

			idx, results, err := pack.VerifyBinary(f, stat.Size())
			if err != nil {
				return fmt.Errorf("verification failed: %w", err)
			}

			if jsonOutput {
				out := struct {
					AppName string                    `json:"app_name"`
					Valid   bool                      `json:"valid"`
					Results []pack.VerificationResult `json:"results"`
				}{
					AppName: idx.AppName,
					Valid:   true,
					Results: results,
				}
				for _, r := range results {
					if !r.Valid {
						out.Valid = false
						break
					}
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			fmt.Printf("Verifying '%s' (%s - %s/%s)...\n\n", path, idx.AppName, idx.TargetOS, idx.TargetArch)
			allValid := true
			for _, r := range results {
				if r.Valid {
					fmt.Printf("  [PASS] Variant %-6s (size: %d B, sha256: %.16s...)\n", r.Level, r.UncompressedSize, r.ActualSHA256)
				} else {
					allValid = false
					fmt.Printf("  [FAIL] Variant %-6s: %v\n", r.Level, r.Error)
				}
			}

			fmt.Println()
			if allValid {
				fmt.Println("Result: All embedded variants verified successfully with matching SHA-256 checksums.")
				return nil
			}
			return fmt.Errorf("one or more variants failed checksum/integrity verification")
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results in JSON format")
	return cmd
}

func newTrimCmd() *cobra.Command {
	var (
		outputPath       string
		targetLevel      string
		maxLevel         string
		disabledVariants string
		policyName       string
	)

	cmd := &cobra.Command{
		Use:   "trim <binary> [-o <output>] [--level <level>]",
		Short: "Trim away unneeded variant payloads, keeping the launcher stub and selected variant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			srcPath := filepath.Clean(args[0])
			// #nosec G304 -- user-supplied binary path to trim
			f, err := os.Open(srcPath)
			if err != nil {
				return fmt.Errorf("opening %s: %w", srcPath, err)
			}
			defer func() { _ = f.Close() }()

			stat, err := f.Stat()
			if err != nil {
				return fmt.Errorf("stat %s: %w", srcPath, err)
			}

			if !format.IsFatBinary(f, stat.Size()) {
				return fmt.Errorf("'%s' is not a valid microfat fat binary (missing magic trailer)", srcPath)
			}

			idx, err := format.ReadTrailerAndIndex(f, stat.Size())
			if err != nil {
				return fmt.Errorf("reading binary manifest: %w", err)
			}

			policy := microarch.ReadPolicyFromEnv()
			if targetLevel != "" {
				policy.ForceLevel = targetLevel
			}
			if maxLevel != "" {
				policy.MaxLevel = maxLevel
			}
			if disabledVariants != "" {
				for _, v := range strings.Split(disabledVariants, ",") {
					v = strings.TrimSpace(v)
					if v != "" {
						policy.DisabledVariants = append(policy.DisabledVariants, v)
					}
				}
			}
			if policyName != "" {
				policy.Name = strings.ToLower(strings.TrimSpace(policyName))
			}

			hostInfo := microarch.Detect()
			policyRes, selErr := microarch.SelectVariantWithPolicy(idx.TargetArch, hostInfo.Level, idx.VariantLevels(), policy)
			if selErr != nil {
				return fmt.Errorf("selecting optimal variant with policy: %w", selErr)
			}
			targetLevel = policyRes.SelectedVariant

			destPath := srcPath
			if outputPath != "" {
				destPath = filepath.Clean(outputPath)
			}

			destDir := filepath.Dir(destPath)
			tmpFile, err := os.CreateTemp(destDir, ".microfat-trim-*.tmp")
			if err != nil {
				return fmt.Errorf("creating temp file in %s: %w", destDir, err)
			}
			tmpPath := tmpFile.Name()
			defer func() {
				_ = tmpFile.Close()
				_ = os.Remove(tmpPath)
			}()

			newIdx, err := pack.TrimBinary(f, stat.Size(), targetLevel, tmpFile)
			if err != nil {
				return fmt.Errorf("trimming binary: %w", err)
			}

			if err := tmpFile.Sync(); err != nil {
				return fmt.Errorf("syncing file: %w", err)
			}
			if err := tmpFile.Chmod(stat.Mode()); err != nil {
				return fmt.Errorf("chmodding file: %w", err)
			}
			if err := tmpFile.Close(); err != nil {
				return fmt.Errorf("closing temp file: %w", err)
			}

			if err := os.Rename(tmpPath, destPath); err != nil {
				return fmt.Errorf("writing trimmed binary to %s: %w", destPath, err)
			}

			newStat, _ := os.Stat(destPath)
			var newSize int64
			if newStat != nil {
				newSize = newStat.Size()
			}

			fmt.Printf("Successfully trimmed '%s' to variant '%s':\n", destPath, targetLevel)
			fmt.Printf("  • Size: %d bytes -> %d bytes\n", stat.Size(), newSize)
			fmt.Printf("  • Variants: %d embedded (%s)\n", len(newIdx.Variants), targetLevel)
			if policyRes.PolicyApplied != "" {
				fmt.Printf("  • Policy: %s (%s)\n", policyRes.PolicyApplied, policyRes.OverrideReason)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Destination output path (defaults to in-place replacement)")
	cmd.Flags().StringVar(&targetLevel, "level", "", "Target microarchitecture level to retain (defaults to auto-detected host level)")
	cmd.Flags().StringVar(&maxLevel, "max-level", "", "Maximum microarchitecture level ceiling to retain")
	cmd.Flags().StringVar(&disabledVariants, "disable-variants", "", "Comma-separated list of variant levels to exclude")
	cmd.Flags().StringVar(&policyName, "policy", "", "Policy preset name (e.g. safe_avx512, no_downclock)")

	return cmd
}

func newPackCmd() *cobra.Command {
	var (
		manifestPath      string
		stubPath          string
		outputPath        string
		appName           string
		targetOS          string
		targetArch        string
		rawVariants       []string
		skipELFValidation bool
		concurrency       int
		keepIntermediates bool
		goBinary          string
	)

	cmd := &cobra.Command{
		Use:   "pack [--manifest <file> | --stub <stub> -v <level>=<path> ... -o <output>]",
		Short: "Package multiple Go microarchitecture binaries into a single fat binary",
		RunE: func(cmd *cobra.Command, args []string) error {
			if manifestPath != "" {
				m, err := builder.LoadManifest(manifestPath)
				if err != nil {
					return err
				}
				opts := builder.BuildOptions{
					ManifestPath:      manifestPath,
					OutputPath:        outputPath,
					StubPath:          stubPath,
					Concurrency:       concurrency,
					KeepIntermediates: keepIntermediates,
					GoBinary:          goBinary,
					SkipELFValidation: skipELFValidation,
					Stdout:            os.Stdout,
					Stderr:            os.Stderr,
				}
				fmt.Printf("Compiling and packaging PGO matrix for '%s' (%s/%s)...\n", m.AppName, m.TargetOS, m.TargetArch)
				res, err := builder.BuildAndPack(cmd.Context(), m, opts)
				if err != nil {
					return fmt.Errorf("packaging error: %w", err)
				}
				fmt.Printf("Successfully packaged %d variants into '%s':\n", len(res.Index.Variants), res.OutputPath)
				for _, v := range res.Index.Variants {
					fmt.Printf("  • %-6s -> uncompressed: %d B | compressed: %d B\n", v.Level, v.UncompressedSize, v.CompressedSize)
				}
				return nil
			}

			if stubPath == "" {
				return errors.New("required flag(s) \"stub\" not set (or specify --manifest)")
			}
			if outputPath == "" {
				return errors.New("required flag(s) \"output\" not set (or specify --manifest)")
			}
			if len(rawVariants) == 0 {
				return errors.New("required flag(s) \"variant\" not set (or specify --manifest)")
			}

			variants := make(map[string]string)
			for _, item := range rawVariants {
				parts := strings.SplitN(item, "=", keyValueParts)
				if len(parts) != keyValueParts || parts[0] == "" || parts[1] == "" {
					return fmt.Errorf("invalid variant specification %q, expected <level>=<path> (e.g. v3=dist/app_v3)", item)
				}
				level := parts[0]
				if _, exists := variants[level]; exists {
					return fmt.Errorf("duplicate variant level %q specified", level)
				}
				variants[level] = parts[1]
			}

			opts := pack.Options{
				StubPath:          stubPath,
				OutputPath:        outputPath,
				AppName:           appName,
				TargetOS:          targetOS,
				TargetArch:        targetArch,
				Variants:          variants,
				SkipELFValidation: skipELFValidation,
			}

			fmt.Printf("Packaging fat binary '%s'...\n", outputPath)
			idx, err := pack.Pack(opts)
			if err != nil {
				return fmt.Errorf("packaging error: %w", err)
			}

			fmt.Printf("Successfully packaged %d variants into '%s':\n", len(idx.Variants), outputPath)
			for _, v := range idx.Variants {
				fmt.Printf("  • %-6s -> uncompressed: %d B | compressed: %d B\n", v.Level, v.UncompressedSize, v.CompressedSize)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&manifestPath, "manifest", "m", "", "Path to YAML or JSON build manifest file")
	cmd.Flags().StringVar(&stubPath, "stub", "", "Path to microfat launcher stub binary")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Destination output path for the fat binary")
	cmd.Flags().StringVar(&appName, "name", "", "Application name")
	cmd.Flags().StringVar(&targetOS, "os", "linux", "Target operating system")
	cmd.Flags().StringVar(&targetArch, "arch", "amd64", "Target architecture (amd64 or arm64)")
	cmd.Flags().StringArrayVarP(&rawVariants, "variant", "v", nil,
		"Variant mapping in <level>=<path> format (e.g. -v v1=bin/v1 -v v3=bin/v3 for amd64, or -v v8.0=bin/v80 -v v8.2=bin/v82 for arm64)")
	cmd.Flags().BoolVar(&skipELFValidation, "skip-elf-validation", false, "Skip ELF header architecture validation")
	cmd.Flags().IntVarP(&concurrency, "concurrency", "j", 0, "Number of concurrent compiler workers (when using --manifest)")
	cmd.Flags().BoolVar(&keepIntermediates, "keep-intermediates", false, "Keep intermediate compiled variant ELF binaries")
	cmd.Flags().StringVar(&goBinary, "go-binary", "", "Path to Go toolchain binary (defaults to $GO or 'go')")

	return cmd
}

func newPgoPackCmd() *cobra.Command {
	var (
		manifestPath      string
		outputPath        string
		stubPath          string
		concurrency       int
		keepIntermediates bool
		goBinary          string
		skipELFValidation bool
	)

	cmd := &cobra.Command{
		Use:   "pgo-pack --manifest <manifest-file> [-o <output>]",
		Short: "Compile and package microarchitecture variants with Profile-Guided Optimization (PGO)",
		Long: `pgo-pack automates concurrent Go compilation across multiple microarchitecture levels
(e.g., v1, v3, v4 on AMD64 or v8.0, v8.2 on ARM64) with Profile-Guided Optimization (-pgo) profiles,
then packages them into a self-dispatching microfat binary.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if manifestPath == "" && len(args) > 0 {
				manifestPath = args[0]
			}
			if manifestPath == "" {
				return errors.New("manifest file path is required (use --manifest <file> or specify as first argument)")
			}

			m, err := builder.LoadManifest(manifestPath)
			if err != nil {
				return err
			}

			opts := builder.BuildOptions{
				ManifestPath:      manifestPath,
				OutputPath:        outputPath,
				StubPath:          stubPath,
				Concurrency:       concurrency,
				KeepIntermediates: keepIntermediates,
				GoBinary:          goBinary,
				SkipELFValidation: skipELFValidation,
				Stdout:            os.Stdout,
				Stderr:            os.Stderr,
			}

			fmt.Printf("Compiling and packaging PGO matrix for '%s' (%s/%s)...\n", m.AppName, m.TargetOS, m.TargetArch)
			res, err := builder.BuildAndPack(cmd.Context(), m, opts)
			if err != nil {
				return fmt.Errorf("PGO matrix packaging failed: %w", err)
			}

			fmt.Printf("\nSuccessfully built and packaged %d variants into '%s':\n", len(res.Index.Variants), res.OutputPath)
			for _, v := range res.Index.Variants {
				pgoFlag := res.VariantPGOApplied[v.Level]
				fmt.Printf("  • %-6s (pgo: %-20s) -> uncompressed: %d B | compressed: %d B\n",
					v.Level, pgoFlag, v.UncompressedSize, v.CompressedSize)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&manifestPath, "manifest", "m", "", "Path to YAML or JSON build manifest file")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Destination output path for the fat binary (overrides manifest)")
	cmd.Flags().StringVar(&stubPath, "stub", "", "Path to microfat launcher stub binary (overrides manifest)")
	cmd.Flags().IntVarP(&concurrency, "concurrency", "j", 0, "Number of concurrent compiler workers (defaults to NumCPU)")
	cmd.Flags().BoolVar(&keepIntermediates, "keep-intermediates", false, "Keep intermediate compiled variant ELF binaries")
	cmd.Flags().StringVar(&goBinary, "go-binary", "", "Path to Go toolchain binary (defaults to $GO or 'go')")
	cmd.Flags().BoolVar(&skipELFValidation, "skip-elf-validation", false, "Skip ELF header architecture validation")

	return cmd
}

func newPrewarmCmd() *cobra.Command {
	var (
		targetLevel string
		allVariants bool
		cacheDir    string
		jsonOutput  bool
		verifyOnly  bool
	)

	cmd := &cobra.Command{
		Use:   "prewarm <binary>",
		Short: "Pre-extract optimal or all variant payloads into the microfat cache, or verify cache integrity",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := filepath.Clean(args[0])
			// #nosec G304 -- user-supplied binary path to prewarm
			f, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("opening %s: %w", path, err)
			}
			defer func() { _ = f.Close() }()

			stat, err := f.Stat()
			if err != nil {
				return fmt.Errorf("stat %s: %w", path, err)
			}

			if !format.IsFatBinary(f, stat.Size()) {
				return fmt.Errorf("'%s' is not a valid microfat fat binary (missing magic trailer)", path)
			}

			idx, err := format.ReadTrailerAndIndex(f, stat.Size())
			if err != nil {
				return fmt.Errorf("reading binary manifest: %w", err)
			}

			resolvedDir, err := format.ResolveCacheDir(cacheDir)
			if err != nil {
				return fmt.Errorf("resolving cache directory: %w", err)
			}

			var targetLevels []string
			switch {
			case allVariants:
				targetLevels = idx.VariantLevels()
			case targetLevel != "":
				if _, found := idx.FindVariant(targetLevel); !found {
					return fmt.Errorf("variant level %q not found in binary manifest", targetLevel)
				}
				targetLevels = []string{targetLevel}
			default:
				hostInfo := microarch.Detect()
				policy := microarch.ReadPolicyFromEnv()
				policyRes, selErr := microarch.SelectVariantWithPolicy(idx.TargetArch, hostInfo.Level, idx.VariantLevels(), policy)
				if selErr != nil {
					return fmt.Errorf("selecting optimal variant: %w", selErr)
				}
				targetLevels = []string{policyRes.SelectedVariant}
			}

			if verifyOnly {
				idx, results, err := pack.VerifyCacheBinary(f, stat.Size(), targetLevels, resolvedDir)
				if err != nil {
					return fmt.Errorf("verifying cache: %w", err)
				}

				allValid := true
				for _, r := range results {
					if !r.Valid {
						allValid = false
						break
					}
				}

				if jsonOutput {
					telem := format.PrewarmTelemetry{
						Event:             format.EventPrewarm,
						TimestampUnixNano: time.Now().UnixNano(),
						AppName:           idx.AppName,
						CacheDir:          resolvedDir,
						Results:           results,
					}
					enc := json.NewEncoder(os.Stdout)
					enc.SetIndent("", "  ")
					if err := enc.Encode(telem); err != nil {
						return fmt.Errorf("encoding json: %w", err)
					}
					if !allValid {
						return errors.New("cache verification failed for one or more variants")
					}
					return nil
				}

				fmt.Printf("Verifying cache for '%s' (%s - %s/%s)...\n", path, idx.AppName, idx.TargetOS, idx.TargetArch)
				fmt.Printf("Cache Directory: %s\n\n", resolvedDir)
				for _, r := range results {
					if r.Valid {
						fmt.Printf("  [PASS] %-6s (sha256: %.12s..., size: %d B) -> %s\n",
							r.Level, r.SHA256, r.UncompressedSize, r.CachedPath)
					} else {
						fmt.Printf("  [FAIL] %-6s (sha256: %.12s..., size: %d B) -> status: %s (%s)\n",
							r.Level, r.SHA256, r.UncompressedSize, r.Status, r.Error)
					}
				}
				if !allValid {
					return errors.New("cache verification failed for one or more variants")
				}
				fmt.Println("\nResult: All specified cache entries are valid and verified.")
				return nil
			}

			idx, results, err := pack.PrewarmBinary(f, stat.Size(), targetLevels, resolvedDir)
			if err != nil {
				return fmt.Errorf("prewarming binary: %w", err)
			}

			if jsonOutput {
				telem := format.PrewarmTelemetry{
					Event:             format.EventPrewarm,
					TimestampUnixNano: time.Now().UnixNano(),
					AppName:           idx.AppName,
					CacheDir:          resolvedDir,
					Results:           results,
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(telem)
			}

			fmt.Printf("Prewarming cache for '%s' (%s - %s/%s)...\n", path, idx.AppName, idx.TargetOS, idx.TargetArch)
			fmt.Printf("Cache Directory: %s\n\n", resolvedDir)
			for _, r := range results {
				status := "extracted"
				if r.AlreadyCached {
					status = "already cached"
				}
				fmt.Printf("  • %-6s (sha256: %.12s..., size: %d B) -> %s (%s, %d µs)\n",
					r.Level, r.SHA256, r.UncompressedSize, r.CachedPath, status, r.DecompressionUs)
			}
			fmt.Println("\nResult: Cache prewarming complete.")
			return nil
		},
	}

	cmd.Flags().StringVarP(&targetLevel, "level", "l", "", "Specific variant level to prewarm (defaults to host-optimal variant)")
	cmd.Flags().BoolVar(&allVariants, "all", false, "Prewarm all embedded variants into the cache")
	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "Custom destination cache directory")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results in JSON format")
	cmd.Flags().BoolVar(&verifyOnly, "verify", false, "Verify cache health and checksums for target variants without extracting")

	return cmd
}

