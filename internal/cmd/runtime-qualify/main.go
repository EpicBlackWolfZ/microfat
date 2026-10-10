// Command runtime-qualify records native source or signed-candidate runtime evidence.
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

	"github.com/EpicBlackWolfZ/microfat/internal/releaseaudit"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
	"github.com/EpicBlackWolfZ/microfat/internal/runtimequalify"
)

func run(ctx context.Context, args []string, out io.Writer, execute releaseaudit.Execute) error {
	if len(args) == 2 && args[0] == "required-tag" {
		if err := releaseaudit.ValidateTag(args[1]); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, releasecheck.UsesInstallerHelper(args[1]))
		return err
	}
	flags := flag.NewFlagSet("runtime-qualify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var options runtimequalify.Options
	flags.StringVar(&options.Input, "input", "source", "source or candidate")
	flags.StringVar(&options.Tests, "tests", "auto", "auto or required")
	flags.StringVar(&options.Backend, "backend", "userns", "userns or explicit sudo")
	flags.StringVar(&options.Output, "output", ".work/runtime-qualification", "evidence parent directory")
	flags.StringVar(&options.Dist, "dist", "", "authenticated candidate input directory")
	flags.StringVar(&options.Tag, "tag", "", "exact candidate tag")
	flags.StringVar(&options.Source, "source", "", "full candidate source SHA")
	flags.StringVar(&options.Go, "go", "go", "Go 1.27.2 executable")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("runtime-qualify accepts no positional arguments")
	}
	return runtimequalify.Run(ctx, options, out, os.Environ(), execute)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout, releaseaudit.ExecuteProcess)
	cancel()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
