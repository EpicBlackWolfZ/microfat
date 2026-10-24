// Command p99-investigation runs the frozen #230 historical-source experiment.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/internal/p99investigation"
)

func run(ctx context.Context, args []string, execute p99investigation.Execute) error {
	if len(args) == 0 {
		return errors.New("usage: p99-investigation prepare|measure|summarize [options]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := p99investigation.Options{}
	flags.StringVar(&options.Scenario, "scenario", "", "memory32 or mixed4")
	flags.IntVar(&options.Cohort, "cohort", 0, "independent hosted cohort 0 or 1")
	flags.StringVar(&options.Controls, "controls", "", "host resource controls JSON")
	flags.StringVar(&options.Output, "output", ".work/p99-investigation", "new cohort output directory")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || options.Output == "" {
		return errors.New("unexpected arguments or empty output directory")
	}
	switch args[0] {
	case "prepare":
		return p99investigation.Prepare(ctx, options, execute)
	case "measure":
		const measurementBudget = 165 * time.Minute
		ctx, cancel := context.WithTimeout(ctx, measurementBudget)
		defer cancel()
		return p99investigation.Measure(ctx, options.Output, execute)
	case "summarize":
		return p99investigation.Summarize(options.Output)
	default:
		return errors.New("unknown investigation operation")
	}
}

func execute(ctx context.Context, spec process.Spec) ([]byte, error) {
	return executeOutput(ctx, spec, os.Stderr)
}

func executeOutput(ctx context.Context, spec process.Spec, stderr io.Writer) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	child, err := process.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	const progressInterval = time.Second
	ticker := time.NewTicker(progressInterval)
	defer ticker.Stop()
	written := 0
	flush := func() error {
		data := child.Stderr()
		_, err := stderr.Write(data[written:])
		written = len(data)
		return err
	}
	for {
		select {
		case <-child.Done():
			return child.Stdout(), errors.Join(child.Wait(), flush())
		case <-ticker.C:
			if err := flush(); err != nil {
				cancel()
				return child.Stdout(), errors.Join(err, child.Wait())
			}
		}
	}
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], execute)
	cancel()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
