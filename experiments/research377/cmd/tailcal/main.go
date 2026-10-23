package main

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/EpicBlackWolfZ/microfat/experiments/research377"
)

const (
	exitFailure = 1
	defaultSeed = 377
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, input io.Reader, output, diagnostics io.Writer) int {
	flags := flag.NewFlagSet("tailcal", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	mode := flags.String("mode", "calibrate", "calibrate or analyze (strict JSON on standard input)")
	samples := flags.Int("samples", research377.DefaultSamples, "fixed samples per arm in calibration")
	replications := flags.Int("replications", research377.DefaultReplications, "seeded calibration replications")
	seed := flags.Uint64("seed", defaultSeed, "reproducible simulation seed")
	if err := flags.Parse(args); err != nil {
		return exitFailure
	}
	if flags.NArg() != 0 {
		return fail(diagnostics, errors.New("unexpected positional arguments"))
	}
	result, err := execute(*mode, input, research377.CalibrationConfig{Seed: *seed, Samples: *samples, Replications: *replications})
	if err != nil {
		return fail(diagnostics, err)
	}
	if err := json.MarshalWrite(output, result, jsontext.WithIndent("  ")); err != nil {
		return fail(diagnostics, err)
	}
	return 0
}

func execute(mode string, input io.Reader, config research377.CalibrationConfig) (any, error) {
	switch mode {
	case "calibrate":
		return research377.Calibrate(config)
	case "analyze":
		data, err := decodeInput(input, research377.MaximumInput)
		if err != nil {
			return nil, err
		}
		return research377.Analyze(data)
	default:
		return nil, errors.New("mode must be calibrate or analyze")
	}
}

func decodeInput(input io.Reader, limit int64) (research377.Input, error) {
	encoded, err := io.ReadAll(io.LimitReader(input, limit+1))
	if err != nil {
		return research377.Input{}, err
	}
	if int64(len(encoded)) > limit {
		return research377.Input{}, errors.New("JSON input exceeds byte cap")
	}
	var data research377.Input
	if err := json.Unmarshal(encoded, &data, json.RejectUnknownMembers(true)); err != nil {
		return research377.Input{}, err
	}
	return data, nil
}

func fail(diagnostics io.Writer, err error) int {
	// A diagnostics write failure cannot change the unsuccessful exit status.
	_, _ = fmt.Fprintln(diagnostics, err)
	return exitFailure
}
