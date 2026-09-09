package main

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/ci"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/compare"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/report"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/runner"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
	"github.com/spf13/cobra"
)

var executeExperiment = runner.RunExperiment

func addExperimentCommands(parent *cobra.Command) {
	parent.AddCommand(newExperimentRunCmd(), newExperimentReadCmd("report"), newExperimentReadCmd("compare"), newExperimentVerifyCmd())
	parent.AddCommand(newExperimentGateCmd())
	parent.AddCommand(&cobra.Command{Use: "observe <configuration>", Hidden: true, Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var cfg system.ObserverConfig
			if err := json.Unmarshal([]byte(args[0]), &cfg); err != nil {
				return err
			}
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			return system.Observe(ctx, cfg, cmd.OutOrStdout())
		}})
	parent.AddCommand(&cobra.Command{Use: "child <configuration>", Hidden: true, Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := system.ParseChild([]byte(args[0]))
			if err != nil {
				return err
			}
			return system.ExecChild(cfg)
		}})
}

func newExperimentRunCmd() *cobra.Command {
	var configPath, output, fortio, repository, baseRepository, goTool string
	cmd := &cobra.Command{Use: "run --config <suite.json> --output-dir <directory>",
		Short: "Run paired server trials with an external Fortio process", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if configPath == "" {
				return errors.New("--config is required")
			}
			cfg, err := runner.ReadExperimentConfig(configPath)
			if err != nil {
				return err
			}
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			result, runErr := executeExperiment(ctx, cfg, runner.RunOptions{Repository: repository,
				BaseRepository: baseRepository, Go: goTool,
				Fortio: fortio, OutputDir: output, LogWriter: cmd.ErrOrStderr()})
			if result != nil {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), result.Bundle)
			}
			return errors.Join(runErr, err)
		}}
	cmd.Flags().StringVar(&configPath, "config", "", "Versioned experiment suite JSON")
	cmd.Flags().StringVar(&output, "output-dir", "results/benchmarks", "Parent directory for immutable evidence bundles")
	cmd.Flags().StringVar(&fortio, "fortio", os.Getenv("MICROFAT_BENCH_FORTIO"), "Path to pinned Fortio 1.75.2")
	cmd.Flags().StringVar(&repository, "repository", ".", "Source repository used for native and fat builds")
	cmd.Flags().StringVar(&baseRepository, "base-repository", "", "Base checkout for counterbalanced packer/stub revision comparison")
	cmd.Flags().StringVar(&goTool, "go", "go", "Go 1.27.1 executable")
	return cmd
}

func newExperimentGateCmd() *cobra.Command {
	var input string
	policy := ci.DefaultPolicy()
	cmd := &cobra.Command{Use: "gate --input <bundle>", Short: "Evaluate coarse base/head regression policy", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			exp, err := report.ReadBundle(input)
			if err != nil {
				return err
			}
			comparisons, err := compare.Revisions(exp)
			if err != nil {
				return err
			}
			decisions, regression, err := ci.Evaluate(comparisons, policy)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprint(cmd.OutOrStdout(), ci.Summary(decisions)); err != nil {
				return err
			}
			if regression {
				return errors.New("coarse benchmark regression detected")
			}
			for _, trial := range exp.Trials {
				if trial.Outcome != schema.OutcomeOK {
					return errors.New("benchmark trial failure")
				}
			}
			if !exp.Complete {
				return errors.New("incomplete benchmark experiment")
			}
			return nil
		}}
	cmd.Flags().StringVar(&input, "input", "", "Verified paired base/head evidence bundle")
	cmd.Flags().BoolVar(&policy.Calibrated, "startup-calibrated", false, "Enable a previously calibrated startup threshold")
	cmd.Flags().Float64Var(&policy.StartupFloorNS, "startup-floor-ns", 0, "Absolute startup regression floor from no-change calibration")
	return cmd
}

func newExperimentReadCmd(operation string) *cobra.Command {
	var input, format, baseline string
	var jsonOutput bool
	cmd := &cobra.Command{Use: operation + " --input <bundle>", Short: "Analyze verified evidence offline", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if input == "" {
				return errors.New("--input is required")
			}
			if jsonOutput {
				format = "json"
			}
			if operation == "report" {
				data, err := report.RenderInput(input, format)
				if err != nil {
					return err
				}
				_, err = cmd.OutOrStdout().Write(append(data, '\n'))
				return err
			}
			exp, err := report.ReadBundle(input)
			if err != nil {
				return err
			}
			if operation == "compare" {
				if baseline == "" {
					baseline = exp.Configurations[0].ID
				}
				exp.Comparisons, err = compare.All(exp, baseline)
				if err != nil {
					return err
				}
			}
			if jsonOutput {
				format = "json"
			}
			var data []byte
			if operation == "compare" && format == "json" {
				data, err = schema.CanonicalV2(exp.Comparisons)
			} else {
				data, err = report.Render(exp, format)
			}
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(append(data, '\n'))
			return err
		}}
	cmd.Flags().StringVar(&input, "input", "", "Evidence bundle directory")
	cmd.Flags().StringVar(&format, "format", "terminal", "terminal, markdown or json")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit JSON only on stdout")
	cmd.Flags().StringVar(&baseline, "baseline", "", "Baseline configuration ID for comparison")
	return cmd
}

func newExperimentVerifyCmd() *cobra.Command {
	return &cobra.Command{Use: "verify <bundle>", Short: "Verify every evidence file and schema reference", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			message, err := report.VerifyInput(args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), message)
			return err
		}}
}
