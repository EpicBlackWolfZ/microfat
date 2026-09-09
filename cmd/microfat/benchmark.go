package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/runner"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/workloads"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/workloads/baseline"
	"github.com/EpicBlackWolfZ/microfat/internal/version"
	"github.com/spf13/cobra"
)

const (
	defaultBenchmarkWorkload  = "baseline"
	defaultBenchmarkTrials    = 5
	defaultBenchmarkTrialTime = 1 * time.Second
	defaultBenchmarkWarmup    = 500 * time.Millisecond
	timestampFormatFilename   = "20060102T150405Z"
)

var defaultResultsDir = "results/benchmarks"

func newBenchmarkCmd() *cobra.Command {
	var (
		workloadName string
		trials       int
		trialTime    time.Duration
		warmupTime   time.Duration
		outputPath   string
		jsonOutput   bool
	)

	cmd := &cobra.Command{
		Use:   "benchmark [--workload <name>] [--trials <n>] [--trial-time <duration>] [--warmup <duration>] [-o <file>] [--json]",
		Short: "Execute reproducible microfat benchmarks and generate cryptographic evidence",
		Long: `benchmark executes calibrated, reproducible workload trials, gathers detailed host CPU
and process execution telemetry, computes canonical statistical analysis, and generates cryptographically
hashed Evidence artifacts with non-volatile canonical JSON payloads.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var wl workloads.Workload
			switch workloadName {
			case "", defaultBenchmarkWorkload:
				wl = baseline.New()
			default:
				return fmt.Errorf("unknown workload %q (supported: baseline)", workloadName)
			}

			destPath := outputPath
			if destPath == "" && !jsonOutput {
				commit := version.Commit
				if commit == "" || commit == "none" {
					commit = "dev"
				}
				ts := time.Now().UTC().Format(timestampFormatFilename)
				destPath = filepath.Join(defaultResultsDir, fmt.Sprintf("%s-%s.json", ts, commit))
			}

			// Stream Hygiene: when --json is requested, stdout emits only canonical JSON bytes;
			// progress logs route to stderr.
			logWriter := cmd.ErrOrStderr()
			if !jsonOutput {
				logWriter = cmd.OutOrStdout()
			}

			r := runner.New()
			cfg := runner.Config{
				Workload:       wl,
				ScenarioName:   wl.Name(),
				Trials:         trials,
				TrialDuration:  trialTime,
				WarmupDuration: warmupTime,
				OutputPath:     destPath,
				LogWriter:      logWriter,
			}

			evidence, err := r.Run(cmd.Context(), cfg)
			if err != nil {
				return fmt.Errorf("benchmark execution failed: %w", err)
			}

			if jsonOutput {
				// Emit the exact canonical payload bytes that were hashed into the evidence envelope
				if _, err := cmd.OutOrStdout().Write(evidence.PayloadBytes); err != nil {
					return fmt.Errorf("writing json payload to stdout: %w", err)
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout())
				return nil
			}

			// Terminal summary table
			exp, err := evidence.Experiment()
			if err != nil {
				return fmt.Errorf("reading experiment from evidence: %w", err)
			}

			_, _ = fmt.Fprintln(cmd.OutOrStdout())
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "================ Benchmark Results ================")
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Workload:          %s\n", wl.Name())
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Evidence SHA-256:  %s\n", evidence.DigestSHA256)
			if destPath != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Evidence File:     %s\n", destPath)
			}

			for _, sc := range exp.Scenarios {
				var totalOps int64
				for _, obs := range sc.Observations {
					totalOps += obs.Operations
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nScenario: %s\n", sc.Name)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Trials:            %d\n", len(sc.Observations))
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Total Operations:  %d\n", totalOps)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Latency Samples:   %d\n", sc.Analysis.SampleCount)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Throughput:        %.2f ops/sec\n", sc.Analysis.ThroughputOpsPerSec)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Latency Mean:      %.2f ns\n", sc.Analysis.MeanNs)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Latency Min:       %d ns\n", sc.Analysis.MinNs)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Latency Max:       %d ns\n", sc.Analysis.MaxNs)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Latency p50:       %.2f ns\n", sc.Analysis.PercentilesNs["p50"])
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Latency p75:       %.2f ns\n", sc.Analysis.PercentilesNs["p75"])
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Latency p90:       %.2f ns\n", sc.Analysis.PercentilesNs["p90"])
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Latency p95:       %.2f ns\n", sc.Analysis.PercentilesNs["p95"])
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Latency p99:       %.2f ns\n", sc.Analysis.PercentilesNs["p99"])
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Latency p99.9:     %.2f ns\n", sc.Analysis.PercentilesNs["p99.9"])
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "===================================================")

			return nil
		},
	}

	cmd.Flags().StringVar(&workloadName, "workload", defaultBenchmarkWorkload, "Benchmark workload to execute (supported: baseline)")
	cmd.Flags().IntVar(&trials, "trials", defaultBenchmarkTrials, "Number of benchmark trials to execute")
	cmd.Flags().DurationVar(&trialTime, "trial-time", defaultBenchmarkTrialTime, "Target duration per trial (e.g. 1s, 500ms)")
	cmd.Flags().DurationVar(&warmupTime, "warmup", defaultBenchmarkWarmup, "Warmup duration before trial measurements (e.g. 500ms, 0s)")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Destination file path to save evidence envelope JSON")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit pure canonical JSON experiment payload to stdout")
	addExperimentCommands(cmd)

	return cmd
}
