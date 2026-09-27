package main

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"

	"github.com/EpicBlackWolfZ/microfat/internal/update"
	"github.com/EpicBlackWolfZ/microfat/internal/version"
	"github.com/spf13/cobra"
)

const updateUsageCode = 2

type updateCommandError struct {
	error
	code int
}

func (e updateCommandError) Unwrap() error { return e.error }

func newUpdateCmd() *cobra.Command {
	return newUpdateCommand(func(ctx context.Context, opts update.Options) (update.Result, error) {
		return update.NewService(version.Version).Run(ctx, opts)
	})
}

func newUpdateCommand(run func(context.Context, update.Options) (update.Result, error)) *cobra.Command {
	var opts update.Options
	var jsonOutput bool
	cmd := &cobra.Command{
		Use: "update", Short: "Check for or install a verified stable release",
		SilenceUsage: true, SilenceErrors: true,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return updateCommandError{errors.New("update accepts no positional arguments"), updateUsageCode}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := opts.Validate(); err != nil {
				return updateCommandError{err, updateUsageCode}
			}
			result, operationErr := run(cmd.Context(), opts)
			var outputErr error
			if jsonOutput {
				outputErr = json.MarshalWrite(cmd.OutOrStdout(), result)
				if outputErr == nil {
					_, outputErr = fmt.Fprintln(cmd.OutOrStdout())
				}
			} else {
				outputErr = writeUpdateResult(cmd, result)
			}
			if outputErr != nil && result.Activated {
				outputErr = fmt.Errorf("v%s is active, but writing update confirmation failed: %w", *result.CurrentVersion, outputErr)
			}
			if err := errors.Join(operationErr, outputErr); err != nil {
				return updateCommandError{err, 1}
			}
			return nil
		},
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return updateCommandError{err, updateUsageCode} })
	flags := cmd.Flags()
	flags.BoolVar(&opts.Check, "check", false, "Check official release metadata without authenticating or installing artifacts")
	flags.BoolVar(&jsonOutput, "json", false, "Write a structured result as JSON")
	flags.StringVar(&opts.Version, "version", "", "Select an exact published stable release (default: latest stable)")
	flags.BoolVar(&opts.AllowDowngrade, "allow-downgrade", false, "Permit an explicitly selected older release")
	flags.BoolVar(&opts.System, "system", false, "Permit updating a root-owned installation while running as root")
	flags.StringVar(&opts.Staging, "staging-dir", "", "Existing safe executable temporary directory parent")
	flags.StringVar(&opts.Cosign, "cosign", "", "Absolute independently authenticated verifier override")
	flags.StringVar(&opts.CosignSHA256, "cosign-sha256", "", "Independent SHA-256 pin for the verifier override")
	return cmd
}

func writeUpdateResult(cmd *cobra.Command, result update.Result) error {
	value := func(version *string) string {
		if version == nil {
			return "unknown"
		}
		return "v" + *version
	}
	management := "unknown"
	if result.Management != nil {
		management = *result.Management
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Current: %s; running: %s; target: %s.\nStatus: %s; management: %s.\n",
		value(result.CurrentVersion), value(result.RunningVersion), value(result.TargetVersion), result.Status, management)
	if err != nil {
		return err
	}
	if result.Verification == "metadata_only" {
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Release metadata only; artifact authentication occurs during an update."); err != nil {
			return err
		}
	}
	if result.Guidance != "" {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), result.Guidance)
	}
	return err
}
