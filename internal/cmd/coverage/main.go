// Command coverage merges default/minimal profiles and enforces exact statement coverage.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/EpicBlackWolfZ/microfat/internal/coveragegate"
)

func main() {
	if err := run(os.Args[1:], os.Getenv("COVERAGE_THRESHOLD"), os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, threshold string, out io.Writer) error {
	const minimumArgs = 3
	const percent = 100
	const outputPerm = 0o600
	if len(args) < minimumArgs {
		return errors.New("usage: coverage OUTPUT DEFAULT_PROFILE MINIMAL_PROFILE [PROFILE...]")
	}
	if threshold == "" {
		threshold = "95"
	}
	profile, err := coveragegate.Merge(args[1:])
	if err != nil {
		return err
	}
	passed, err := coveragegate.MeetsThreshold(profile.Covered, profile.Total, threshold)
	if err != nil {
		return err
	}
	// #nosec G703 -- an operator-selected output path; this CLI does not confine it to a root.
	if err := os.WriteFile(args[0], []byte(profile.Text), outputPerm); err != nil {
		return err
	}
	display := float64(0)
	if profile.Total != 0 {
		display = percent * float64(profile.Covered) / float64(profile.Total)
	}
	if _, err := fmt.Fprintf(out, "Exact coverage: %d/%d statements (%.6f%%)\n", profile.Covered, profile.Total, display); err != nil {
		return err
	}
	if !passed {
		return fmt.Errorf("coverage below %s%% (without rounding)", threshold)
	}
	return nil
}
