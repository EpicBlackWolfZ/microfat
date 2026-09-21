// Command benchmark-evidence replays and archives an exact hosted measurement cohort.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/internal/benchmarkevidence"
)

func run(args []string, env benchmarkevidence.Environment, execute benchmarkevidence.Execute, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: benchmark-evidence ROOT --calibration|--archive|--shard|--source-cohort RUN ATTEMPT")
	}
	const cohortArgs = 4
	if len(args) == cohortArgs && args[1] == "--source-cohort" {
		cohort := [cohortArgs]string(args)
		source, err := benchmarkevidence.SourceCohort(cohort[0], cohort[2], cohort[3])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, source)
		return err
	}
	flags := flag.NewFlagSet("benchmark-evidence", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	calibration := flags.Bool("calibration", false, "verify calibration evidence")
	archive := flags.Bool("archive", false, "verify all 12 shards and create an immutable archive")
	shard := flags.Bool("shard", false, "verify one measurement shard")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	mode := ""
	for _, option := range []struct {
		enabled bool
		mode    string
	}{{*calibration, benchmarkevidence.Calibration},
		{*archive, benchmarkevidence.Archive}, {*shard, benchmarkevidence.Shard}} {
		if !option.enabled {
			continue
		}
		if mode != "" {
			return errors.New("evidence modes are mutually exclusive")
		}
		mode = option.mode
	}
	if mode == "" || flags.NArg() != 0 {
		return errors.New("select exactly one evidence mode without extra arguments")
	}
	return benchmarkevidence.Run(benchmarkevidence.Options{Root: args[0], Mode: mode, WorkDir: ".work"}, env, execute, out)
}

func executeBenchmark(ctx context.Context, args ...string) ([]byte, error) {
	binary, err := filepath.Abs("bin/microfat")
	if err != nil {
		return nil, err
	}
	stdoutLimit := 0
	if len(args) > 0 && args[0] == "report" {
		stdoutLimit = schema.MaxEvidenceBytes + 1
	}
	out, stderr, err := process.Run(ctx, process.Spec{Path: binary, Args: append([]string{"benchmark"}, args...),
		Env: os.Environ(), StdoutLimit: stdoutLimit})
	if err != nil {
		return nil, fmt.Errorf("benchmark command failed: %w: %s", err, stderr)
	}
	return out, nil
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(os.Args[1:], os.Getenv, func(args ...string) ([]byte, error) { return executeBenchmark(ctx, args...) }, os.Stdout)
	cancel()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
