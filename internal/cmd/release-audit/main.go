// Command release-audit authenticates and tests published artifacts on native Linux runners.
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
)

func run(ctx context.Context, args []string, out io.Writer, execute releaseaudit.Execute) error {
	if len(args) == 0 {
		return errors.New("usage: release-audit validate-tag|metadata|run [options]")
	}
	switch args[0] {
	case "validate-tag":
		const tagArgumentCount = 2
		if len(args) != tagArgumentCount {
			return errors.New("validate-tag requires one tag")
		}
		return releaseaudit.ValidateTag(args[1])
	case "metadata":
		return metadata(args[1:], out)
	case "run":
		flags := flag.NewFlagSet("release-audit", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		var options releaseaudit.Options
		flags.StringVar(&options.Dist, "dist", "", "downloaded release directory")
		flags.StringVar(&options.Output, "output", "", "new evidence output directory")
		flags.StringVar(&options.Version, "version", "", "release version without v prefix")
		flags.StringVar(&options.Source, "source", "", "full release source SHA")
		flags.StringVar(&options.Arch, "arch", "", "native Linux architecture")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || options.Dist == "" || options.Output == "" {
			return errors.New("--dist and --output required; no positional arguments")
		}
		return releaseaudit.Run(ctx, options, out, os.Environ(), execute)
	default:
		return errors.New("unknown release-audit command")
	}
}

func metadata(args []string, out io.Writer) error {
	const argumentCount = 3
	if len(args) != argumentCount {
		return errors.New("metadata requires tag, release.json and source.txt")
	}
	if err := releaseaudit.ValidateTag(args[0]); err != nil {
		return err
	}
	data, err := os.ReadFile(args[1]) // #nosec G304,G703 -- explicitly requested release metadata file.
	if err != nil {
		return err
	}
	source, err := os.ReadFile(args[2]) // #nosec G304,G703 -- explicitly requested tag source identity file.
	if err != nil {
		return err
	}
	value, err := releaseaudit.Metadata(args[0], data, string(source))
	if err != nil {
		return err
	}
	_, err = io.WriteString(out, value)
	return err
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
