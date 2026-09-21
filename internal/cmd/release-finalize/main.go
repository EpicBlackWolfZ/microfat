// Command release-finalize publishes only a complete draft with verified hosted evidence.
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

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/internal/releaseworkflow"
)

func run(args []string, env releaseworkflow.Environment, gh releaseworkflow.Command, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: release-finalize ROOT [--recovery-run RUN --recovery-attempt ATTEMPT] | --recovery-source RUN ATTEMPT")
	}
	const recoveryArgs = 3
	if args[0] == "--recovery-source" {
		if len(args) != recoveryArgs {
			return errors.New("recovery source requires an exact run and attempt")
		}
		tag, source, err := releaseworkflow.RecoverySource(env("GH_REPO"), args[1], args[2], env, gh)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s %s\n", tag, source)
		return err
	}
	flags := flag.NewFlagSet("release-finalize", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := releaseworkflow.FinalizeOptions{Root: args[0]}
	flags.StringVar(&options.RecoveryRun, "recovery-run", "", "exact measurement run")
	flags.StringVar(&options.RecoveryAttempt, "recovery-attempt", "1", "exact measurement attempt")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected publication arguments")
	}
	return releaseworkflow.Finalize(options, env, gh)
}

func executeGitHub(ctx context.Context, args ...string) ([]byte, error) {
	out, stderr, err := process.Run(ctx, process.Spec{Path: "gh", Args: args, Env: os.Environ()})
	if err != nil {
		return nil, fmt.Errorf("GitHub operation failed: %w: %s", err, stderr)
	}
	return out, nil
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(os.Args[1:], os.Getenv, func(args ...string) ([]byte, error) { return executeGitHub(ctx, args...) }, os.Stdout)
	cancel()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
