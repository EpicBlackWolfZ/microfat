// research375 screens codec resource allocations without changing microfat defaults.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	executableMode = 0o700
	modeDecode     = "decode"
	modeEncode     = "encode"
	privateMode    = 0o600
	workerReadyFD  = 3
	researchIssue  = 375
	maxDictBytes   = 1 << 20
	maxParallel    = 4
	maxTrials      = 20
	maxInputs      = 8
	maxInputBytes  = 32 << 20
	maxFrameBytes  = maxInputBytes + maxDictBytes
	maxTotalBytes  = 64 << 20
	maxOutputBytes = 4 << 10
	defaultBudget  = 4
	defaultTrials  = 3
	defaultTimeout = 30 * time.Second
	maxCampaign    = 10 * time.Minute
)

type stringsFlag []string

func (s *stringsFlag) String() string     { return strings.Join(*s, ",") }
func (s *stringsFlag) Set(v string) error { *s = append(*s, v); return nil }

type config struct {
	Inputs      stringsFlag
	PayloadArgs stringsFlag
	Source      string
	Family      string
	Dictionary  string
	Level       string
	Encoders    []int
	Workers     []int
	Decoders    []int
	Bursts      []int
	Budget      int
	Trials      int
	Execute     bool
	ReadyOutput string
	Gap         time.Duration
	Timeout     time.Duration
}

func integers(value string, allowDefault bool) ([]int, error) {
	var result []int
	seen := map[int]bool{}
	for item := range strings.SplitSeq(value, ",") {
		n, err := strconv.Atoi(item)
		if err != nil || n > maxParallel || (n < 1 && !(allowDefault && n == -1)) || seen[n] {
			return nil, fmt.Errorf("invalid distinct concurrency list %q (use 1..4 or -1 for upstream default)", value)
		}
		seen[n] = true
		result = append(result, n)
	}
	return result, nil
}

func parse(args []string) (config, error) {
	c := config{}
	f := flag.NewFlagSet("research375", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.Var(&c.Inputs, "input", "trusted native Go ELF; repeat for encoder bundle")
	f.Var(&c.PayloadArgs, "payload-arg", "argument for explicitly requested application readiness")
	f.StringVar(&c.Source, "source-revision", "", "exact source revision")
	f.StringVar(&c.Family, "family", "", "corpus program family")
	f.StringVar(&c.Dictionary, "dictionary", "", "optional trained Zstd dictionary")
	f.StringVar(&c.Level, "level", "default", "microfat codec level")
	f.IntVar(&c.Budget, "cpu-budget", defaultBudget, "outer workers times effective encoder threads ceiling")
	f.IntVar(&c.Trials, "trials", defaultTrials, "independent burst repetitions; at most 20")
	f.BoolVar(&c.Execute, "execute", false, "execute hash-verified decoded trusted ELF to exact stdout readiness")
	f.StringVar(&c.ReadyOutput, "ready-output", "", "exact expected child stdout, required with execute")
	f.DurationVar(&c.Gap, "arrival-gap", 0, "parent release spacing between burst members")
	f.DurationVar(&c.Timeout, "timeout", defaultTimeout, "per burst/encoder batch timeout; at most 30s")
	enc := f.String("encoders", "-1,1,2,4", "encoder threads; -1 preserves upstream default")
	workers := f.String("workers", "1,2,4", "outer encoder workers")
	dec := f.String("decoders", "-1,1,2,4", "decoder threads; -1 preserves upstream default")
	bursts := f.String("bursts", "1,2,4", "decoder subprocess burst sizes")
	if err := f.Parse(args); err != nil {
		return c, err
	}
	if f.NArg() != 0 || len(c.Inputs) == 0 || len(c.Inputs) > maxInputs || c.Source == "" || c.Family == "" {
		return c, errors.New("require --source-revision, --family and 1..8 --input files; no positional arguments")
	}
	if c.Budget < 1 || c.Budget > maxParallel || c.Trials < 1 || c.Trials > maxTrials ||
		c.Timeout <= 0 || c.Timeout > defaultTimeout || c.Gap < 0 || c.Gap > time.Second ||
		(c.Execute && c.ReadyOutput == "") || len(c.ReadyOutput) > maxOutputBytes {
		return c, errors.New("invalid budget, trial, timeout, arrival gap or expected readiness bound")
	}
	var err error
	for _, item := range []struct {
		dst      *[]int
		text     string
		defaults bool
	}{
		{&c.Encoders, *enc, true}, {&c.Workers, *workers, false},
		{&c.Decoders, *dec, true}, {&c.Bursts, *bursts, false},
	} {
		*item.dst, err = integers(item.text, item.defaults)
		if err != nil {
			return c, err
		}
	}
	return c, nil
}

func run(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
	if len(args) == 2 && args[0] == "_worker" {
		ready := os.NewFile(workerReadyFD, "parent-ready-pipe")
		defer func() { _ = ready.Close() }()
		return worker(ctx, args[1], input, output, ready)
	}
	c, err := parse(args)
	if err != nil {
		return err
	}
	r, err := campaign(ctx, c)
	if err != nil {
		r.Error = err.Error()
		return errors.Join(err, json.NewEncoder(output).Encode(r))
	}
	return json.NewEncoder(output).Encode(r)
}

func main() { os.Exit(entry()) }

func entry() int {
	ctx, cancel := context.WithTimeout(context.Background(), maxCampaign)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func effective(requested int, decoder bool) int {
	if requested != -1 {
		return requested
	}
	if decoder {
		return min(runtime.GOMAXPROCS(0), maxParallel)
	}
	return runtime.GOMAXPROCS(0)
}
