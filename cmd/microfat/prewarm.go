package main

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/spf13/cobra"
)

type prewarmCommandOptions struct {
	targetLevel     string
	allVariants     bool
	cacheDir        string
	jsonOutput      bool
	verifyOnly      bool
	planOnly        bool
	maxPayloadBytes string
}

func newPrewarmCmd() *cobra.Command {
	var opts prewarmCommandOptions
	cmd := &cobra.Command{
		Use:   "prewarm <binary>",
		Short: "Pre-extract variant payloads, plan selected cache work, or verify cache integrity",
		Args:  cobra.ExactArgs(1),
		RunE:  opts.run,
	}
	cmd.Flags().StringVarP(&opts.targetLevel, "level", "l", "", "Specific variant level (defaults to host-optimal variant)")
	cmd.Flags().BoolVar(&opts.allVariants, "all", false, "Select all embedded variants")
	cmd.Flags().StringVar(&opts.cacheDir, "cache-dir", "", "Custom destination cache directory")
	cmd.Flags().BoolVar(&opts.jsonOutput, "json", false, "Output results in JSON format")
	cmd.Flags().BoolVar(&opts.verifyOnly, "verify", false, "Verify selected cache entries without extracting")
	cmd.Flags().BoolVar(&opts.planOnly, "plan", false, "Plan selected work using read-only cache checks; never extract or execute")
	cmd.Flags().StringVar(&opts.maxPayloadBytes, "max-payload-bytes", "", "Cap unique selected raw payload bytes (unsigned decimal)")
	cmd.MarkFlagsMutuallyExclusive("all", "level")
	cmd.MarkFlagsMutuallyExclusive("plan", "verify")
	return cmd
}

func parsePrewarmLimit(raw string, present bool) (pack.PrewarmOptions, error) {
	if !present {
		return pack.PrewarmOptions{}, nil
	}
	const invalidLimit = "invalid --max-payload-bytes %q: expected nonnegative ASCII decimal bytes fitting uint64"
	if raw == "" {
		return pack.PrewarmOptions{}, fmt.Errorf(invalidLimit, raw)
	}
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			return pack.PrewarmOptions{}, fmt.Errorf(invalidLimit, raw)
		}
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return pack.PrewarmOptions{}, fmt.Errorf(invalidLimit, raw)
	}
	return pack.PrewarmOptions{MaxPayloadBytes: &value}, nil
}

func (opts *prewarmCommandOptions) run(cmd *cobra.Command, args []string) error {
	admission, err := parsePrewarmLimit(opts.maxPayloadBytes, cmd.Flags().Changed("max-payload-bytes"))
	if err != nil {
		return err
	}
	path := filepath.Clean(args[0])
	// #nosec G304 -- user-supplied binary path to prewarm, opened without following special files.
	f, err := inputfile.Open(path)
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
	levels, err := opts.selectLevels(idx)
	if err != nil {
		return err
	}
	// Admission uses only validated index declarations and precedes even cache discovery.
	if err := pack.ValidatePrewarmAdmission(idx, levels, admission); err != nil {
		return err
	}
	if opts.planOnly {
		idx, plan, err := pack.PlanPrewarmBinary(f, stat.Size(), levels, opts.cacheDir, admission)
		if err != nil {
			return fmt.Errorf("planning prewarm: %w", err)
		}
		return printPrewarmPlan(cmd.OutOrStdout(), idx, plan, opts.jsonOutput)
	}
	if opts.verifyOnly {
		resolvedDir, err := format.ResolveExistingCacheDir(opts.cacheDir)
		if err != nil {
			return fmt.Errorf("resolving cache directory: %w", err)
		}
		idx, results, err := pack.VerifyCacheBinary(f, stat.Size(), levels, resolvedDir)
		if err != nil {
			return fmt.Errorf("verifying cache: %w", err)
		}
		return printPrewarmVerification(cmd.OutOrStdout(), path, idx, results, resolvedDir, opts.jsonOutput)
	}
	idx, results, runErr := pack.PrewarmBinaryWithOptions(f, stat.Size(), levels, opts.cacheDir, admission)
	if idx != nil && (runErr == nil || len(results) > 0) {
		resolvedDir := opts.cacheDir
		if len(results) > 0 {
			resolvedDir = filepath.Dir(results[0].CachedPath)
		}
		if opts.jsonOutput {
			err = printPrewarmJSON(cmd.OutOrStdout(), idx, results, resolvedDir)
		} else {
			printPrewarmResults(cmd.OutOrStdout(), path, idx, results, resolvedDir, runErr == nil)
		}
		if err != nil {
			return err
		}
	}
	if runErr != nil {
		return fmt.Errorf("prewarming binary: %w", runErr)
	}
	return nil
}

func (opts *prewarmCommandOptions) selectLevels(idx *format.Index) ([]string, error) {
	switch {
	case opts.allVariants:
		return idx.VariantLevels(), nil
	case opts.targetLevel != "":
		if _, found := idx.FindVariant(opts.targetLevel); !found {
			return nil, fmt.Errorf("variant level %q not found in binary manifest", opts.targetLevel)
		}
		return []string{opts.targetLevel}, nil
	default:
		selected, err := microarch.SelectVariantForHost(
			idx.TargetArch, microarch.Detect(), idx.VariantLevels(), microarch.ReadPolicyFromEnv())
		if err != nil {
			return nil, fmt.Errorf("selecting optimal variant: %w", err)
		}
		return []string{selected.SelectedVariant}, nil
	}
}

func printPrewarmJSON(out io.Writer, idx *format.Index, results []format.PrewarmResult, cacheDir string) error {
	telem := format.PrewarmTelemetry{
		Event:             format.EventPrewarm,
		TimestampUnixNano: time.Now().UnixNano(),
		AppName:           idx.AppName,
		CacheDir:          cacheDir,
		Results:           results,
	}
	if err := json.MarshalWrite(out, telem, jsontext.WithIndent("  ")); err != nil {
		return fmt.Errorf("encoding json: %w", err)
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return fmt.Errorf("writing prewarm json: %w", err)
	}
	return nil
}

func printPrewarmVerification(
	out io.Writer, path string, idx *format.Index, results []format.PrewarmResult, cacheDir string, asJSON bool,
) error {
	allValid := true
	for _, result := range results {
		allValid = allValid && result.Valid
	}
	if asJSON {
		if err := printPrewarmJSON(out, idx, results, cacheDir); err != nil {
			return err
		}
	} else {
		_, _ = fmt.Fprintf(out, "Verifying cache for '%s' (%s - %s/%s)...\n", path, idx.AppName, idx.TargetOS, idx.TargetArch)
		_, _ = fmt.Fprintf(out, "Cache Directory: %s\n\n", cacheDir)
		for _, r := range results {
			if r.Valid {
				_, _ = fmt.Fprintf(out, "  [PASS] %-6s (sha256: %.12s..., size: %d B) -> %s\n",
					r.Level, r.SHA256, r.UncompressedSize, r.CachedPath)
			} else {
				_, _ = fmt.Fprintf(out, "  [FAIL] %-6s (sha256: %.12s..., size: %d B) -> status: %s (%s)\n",
					r.Level, r.SHA256, r.UncompressedSize, r.Status, r.Error)
			}
		}
	}
	if !allValid {
		return errors.New("cache verification failed for one or more variants")
	}
	if !asJSON {
		_, _ = fmt.Fprintln(out, "\nResult: All specified cache entries are valid and verified.")
	}
	return nil
}

func printPrewarmResults(
	out io.Writer, path string, idx *format.Index, results []format.PrewarmResult, cacheDir string, complete bool,
) {
	_, _ = fmt.Fprintf(out, "Prewarming cache for '%s' (%s - %s/%s)...\n", path, idx.AppName, idx.TargetOS, idx.TargetArch)
	_, _ = fmt.Fprintf(out, "Cache Directory: %s\n\n", cacheDir)
	for _, r := range results {
		status := "extracted"
		if r.AlreadyCached {
			status = "already cached"
		}
		_, _ = fmt.Fprintf(out, "  • %-6s (sha256: %.12s..., size: %d B) -> %s (%s, %d µs)\n",
			r.Level, r.SHA256, r.UncompressedSize, r.CachedPath, status, r.DecompressionUs)
	}
	if complete {
		_, _ = fmt.Fprintln(out, "\nResult: Cache prewarming complete.")
	} else {
		_, _ = fmt.Fprintf(out, "\nResult: Prewarming stopped after %d completed variants.\n", len(results))
	}
}

func printPrewarmPlan(out io.Writer, idx *format.Index, plan *pack.PrewarmPlan, asJSON bool) error {
	if asJSON {
		report := struct {
			Event      string `json:"event"`
			AppName    string `json:"app_name"`
			TargetOS   string `json:"target_os"`
			TargetArch string `json:"target_arch"`
			*pack.PrewarmPlan
		}{"prewarm_plan", idx.AppName, idx.TargetOS, idx.TargetArch, plan}
		if err := json.MarshalWrite(out, report, jsontext.WithIndent("  ")); err != nil {
			return fmt.Errorf("encoding prewarm plan json: %w", err)
		}
		if _, err := fmt.Fprintln(out); err != nil {
			return fmt.Errorf("writing prewarm plan: %w", err)
		}
	} else {
		if err := printPrewarmPlanText(out, idx, plan); err != nil {
			return err
		}
	}
	for _, entry := range plan.Entries {
		if !entry.CanPrewarm {
			return errors.New("prewarm plan has unsafe, unavailable, or undecodable entries; inspect entry statuses")
		}
	}
	return nil
}

func printPrewarmPlanText(out io.Writer, idx *format.Index, plan *pack.PrewarmPlan) error {
	var text strings.Builder
	_, _ = fmt.Fprintf(&text, "Prewarm plan for %s (%s/%s)\n", idx.AppName, idx.TargetOS, idx.TargetArch)
	_, _ = fmt.Fprintf(&text, "Unique selected payload bytes: %d\nVerified existing cache bytes: %d\n"+
		"Declared bytes needing materialization: %d\nBlocked or unavailable payload bytes: %d\n",
		plan.UniquePayloadBytes, plan.VerifiedCacheBytes, plan.NeedsMaterializationBytes, plan.BlockedPayloadBytes)
	for _, entry := range plan.Entries {
		_, _ = fmt.Fprintf(&text, "  %s sha256:%s size:%d B cache:%s decoder_available:%t can_prewarm:%t -> %s\n",
			entry.Level, entry.SHA256, entry.UncompressedSize, entry.CacheStatus,
			entry.DecoderAvailable, entry.CanPrewarm, entry.CachePath)
		if entry.CacheError != "" || entry.DecoderError != "" {
			_, _ = fmt.Fprintf(&text, "    cache: %s; decoder: %s\n", entry.CacheError, entry.DecoderError)
		}
		if entry.ReuseAfterLevel != "" {
			_, _ = fmt.Fprintf(&text, "    planned reuse after successful level %s\n", entry.ReuseAfterLevel)
		}
	}
	text.WriteString("Read-only snapshot: cache checks read/hash files; index declarations do not verify embedded payloads.\n")
	if _, err := io.WriteString(out, text.String()); err != nil {
		return fmt.Errorf("writing prewarm plan: %w", err)
	}
	return nil
}
