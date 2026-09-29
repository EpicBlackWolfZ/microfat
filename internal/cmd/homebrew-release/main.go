// Command homebrew-release authenticates official releases and prepares Linux casks.
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

	"github.com/EpicBlackWolfZ/microfat/internal/homebrew"
)

type service struct {
	discover func(context.Context) (string, error)
	prepare  func(context.Context, string, string) (homebrew.Evidence, error)
	check    func(context.Context, string) error
}

func run(ctx context.Context, args []string, out io.Writer, svc service) error {
	if len(args) == 0 {
		return errors.New("usage: homebrew-release discover|prepare|check|update-needed")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var tag, output, cask, candidate string
	switch args[0] {
	case "discover":
	case "prepare":
		flags.StringVar(&tag, "tag", "", "exact published stable tag")
		flags.StringVar(&output, "output", "", "new output directory")
	case "check":
		flags.StringVar(&cask, "cask", "", "committed microfat cask")
	case "update-needed":
		flags.StringVar(&cask, "cask", "", "current cask (may be absent)")
		flags.StringVar(&candidate, "candidate", "", "authenticated candidate")
	default:
		return errors.New("unknown homebrew-release command")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	switch args[0] {
	case "discover":
		value, err := svc.discover(ctx)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, value)
		return err
	case "prepare":
		if tag == "" || output == "" {
			return errors.New("--tag and --output are required")
		}
		value, err := svc.prepare(ctx, tag, output)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(value)
	case "check":
		if cask == "" {
			return errors.New("--cask is required")
		}
		return svc.check(ctx, cask)
	default:
		if cask == "" || candidate == "" {
			return errors.New("--cask and --candidate are required")
		}
		return updateNeeded(cask, candidate, out)
	}
}

func updateNeeded(cask, candidate string, out io.Writer) error {
	current, err := os.ReadFile(cask) // #nosec G304 -- operator-selected cask; read-only comparison.
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	next, err := os.ReadFile(candidate) // #nosec G304 -- operator-selected candidate; read-only comparison.
	if err != nil {
		return err
	}
	needed, err := homebrew.UpdateNeeded(current, next)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, needed)
	return err
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout, service{homebrew.Discover, homebrew.Prepare, homebrew.Check})
	cancel()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
