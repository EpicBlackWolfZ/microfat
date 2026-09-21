// Command benchmark-matrix runs the declared compatibility or sustained benchmark cases.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/internal/benchmarkmatrix"
)

func run(ctx context.Context, args []string, out io.Writer, execute benchmarkmatrix.Execute) error {
	if len(args) == 0 {
		return errors.New("usage: benchmark-matrix compatibility|nightly|release [options]")
	}
	flags := flag.NewFlagSet("benchmark-matrix", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := benchmarkmatrix.Options{Tier: args[0]}
	flags.StringVar(&options.Workload, "workload", "mixed", "mixed, cpu or memory")
	flags.StringVar(&options.Intensity, "intensity", "", "standard or heavy shard")
	flags.StringVar(&options.Output, "output", ".work/benchmark-matrix", "evidence output directory")
	controlsPath := flags.String("controls", "", "explicit target/generator settings")
	list := flags.Bool("list", false, "print matrix without execution")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected benchmark matrix arguments")
	}
	cases, err := benchmarkmatrix.Cases(options)
	if err != nil {
		return err
	}
	var controls map[string]any
	if *controlsPath != "" {
		data, err := os.ReadFile(*controlsPath) // #nosec G304 -- explicitly supplied controls file.
		if err != nil {
			return err
		}
		controls, err = benchmarkmatrix.ParseControls(data)
		if err != nil {
			return err
		}
	}
	if *list {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(cases)
	}
	return benchmarkmatrix.Run(ctx, options, controls, execute)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	execute := func(ctx context.Context, spec process.Spec) (int, error) {
		return benchmarkmatrix.RunProcess(ctx, spec, os.Stdout, os.Stderr)
	}
	err := run(ctx, os.Args[1:], os.Stdout, execute)
	cancel()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
