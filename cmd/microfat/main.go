// Package main provides the microfat CLI developer and CI toolkit.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ghostnetorg/microfat/internal/format"
	"github.com/ghostnetorg/microfat/internal/pack"
	"github.com/ghostnetorg/pkg/microarch"
	"github.com/ghostnetorg/pkg/version"
	"github.com/spf13/cobra"
)

const (
	percentMultiplier = 100.0
	keyValueParts     = 2
)

func main() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
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
	cmd.AddCommand(newPackCmd())

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

func newPackCmd() *cobra.Command {
	var (
		stubPath    string
		outputPath  string
		appName     string
		targetOS    string
		targetArch  string
		rawVariants []string
	)

	cmd := &cobra.Command{
		Use:   "pack --stub <stub> -v <level>=<path> ... -o <output>",
		Short: "Package multiple Go microarchitecture binaries into a single fat binary",
		RunE: func(cmd *cobra.Command, args []string) error {
			variants := make(map[string]string)
			for _, item := range rawVariants {
				parts := strings.SplitN(item, "=", keyValueParts)
				if len(parts) != keyValueParts || parts[0] == "" || parts[1] == "" {
					return fmt.Errorf("invalid variant specification %q, expected <level>=<path> (e.g. v3=dist/app_v3)", item)
				}
				variants[parts[0]] = parts[1]
			}

			opts := pack.Options{
				StubPath:   stubPath,
				OutputPath: outputPath,
				AppName:    appName,
				TargetOS:   targetOS,
				TargetArch: targetArch,
				Variants:   variants,
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

	cmd.Flags().StringVar(&stubPath, "stub", "", "Path to microfat launcher stub binary (required)")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Destination output path for the fat binary (required)")
	cmd.Flags().StringVar(&appName, "name", "", "Application name")
	cmd.Flags().StringVar(&targetOS, "os", "linux", "Target operating system")
	cmd.Flags().StringVar(&targetArch, "arch", "amd64", "Target architecture")
	cmd.Flags().StringArrayVarP(&rawVariants, "variant", "v", nil,
		"Variant mapping in <level>=<path> format (e.g. -v v1=bin/app_v1 -v v3=bin/app_v3)")

	_ = cmd.MarkFlagRequired("stub")
	_ = cmd.MarkFlagRequired("output")
	_ = cmd.MarkFlagRequired("variant")

	return cmd
}
