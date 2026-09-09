// Package load adapts a pinned external load generator without inventing request samples.
package load

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
)

const (
	Version              = "1.75.2"
	ModuleSum            = "h1:TzBLdqgF9XOyBG4zt/uK8V2QoRbYRPvXM/Ju196nsGA="
	SourceCommit         = "630bfb3db6cf663b4e01524f9e9be945e44f8a20"
	MaxDuration          = 120 * time.Second
	MaxConcurrency       = 128
	MaxQPS               = 1000000
	defaultResolution    = 0.00001
	loadGrace            = 5 * time.Second
	nanosecondsPerSecond = 1e9
	saturationRatio      = 0.95
	minimumTailSamples   = 10000
)

type Options struct {
	Duration    time.Duration
	QPS         float64
	Concurrency int
	Resolution  float64
	RawPath     string
}

func (o Options) Validate() error {
	if o.Duration <= 0 || o.Duration > MaxDuration || o.Concurrency <= 0 || o.Concurrency > MaxConcurrency ||
		!schema.Finite(o.QPS) || o.QPS < 0 || o.QPS > MaxQPS || !schema.Finite(o.Resolution) ||
		o.Resolution <= 0 || o.Resolution > 1 || !schema.SafeReference(o.RawPath) {
		return errors.New("invalid load options")
	}
	return nil
}

type Result struct {
	HTTP      schema.HTTPResult
	Raw       []byte
	Stderr    []byte
	Process   schema.ProcessIdentity
	Resources map[string]schema.Measurement
}

type LoadGenerator interface {
	Configure(Options) error
	Run(context.Context, string) (*Result, error)
	Stop(context.Context) error
}

type Fortio struct {
	Path string
	Env  []string
	// Wrap applies the same child bootstrap controls as the benchmark target.
	Wrap      func(process.Spec) process.Spec
	mu        sync.Mutex
	options   Options
	child     *process.Child
	running   bool
	runCancel context.CancelFunc
}

func (f *Fortio) Configure(options Options) error {
	if options.Resolution == 0 {
		options.Resolution = defaultResolution
	}
	if err := options.Validate(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running {
		return errors.New("load generator already running")
	}
	f.options = options
	return nil
}

func (f *Fortio) CheckVersion(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, loadGrace)
	defer cancel()
	out, _, err := process.Run(ctx, process.Spec{Path: f.Path, Args: []string{"version"}, Env: f.Env})
	if err != nil {
		return fmt.Errorf("Fortio version: %w", err)
	}
	if strings.TrimSpace(string(out)) != Version {
		return fmt.Errorf("expected Fortio %s, got %q", Version, out)
	}
	return nil
}

func (f *Fortio) Run(ctx context.Context, target string) (*Result, error) {
	ctx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	f.mu.Lock()
	if f.running {
		f.mu.Unlock()
		return nil, errors.New("load generator already running")
	}
	opts := f.options
	f.running = true
	f.runCancel = runCancel
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.running = false; f.child = nil; f.runCancel = nil; f.mu.Unlock() }()
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || u.Scheme != "http" || u.User != nil {
		return nil, errors.New("expected an HTTP target without credentials")
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Duration+loadGrace)
	defer cancel()
	args := []string{"load", "-qps", strconv.FormatFloat(opts.QPS, 'g', -1, 64), "-c", strconv.Itoa(opts.Concurrency),
		"-t", opts.Duration.String(), "-r", strconv.FormatFloat(opts.Resolution, 'g', -1, 64),
		"-uniform", "-nocatchup", "-allow-initial-errors", "-p", "50,75,90,95,99,99.9", "-json", "-", target}
	spec := process.Spec{Path: f.Path, Args: args, Env: f.Env}
	if f.Wrap != nil {
		spec = f.Wrap(spec)
	}
	child, err := process.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.child = child
	f.mu.Unlock()
	err = child.Wait()
	result := &Result{Raw: child.Stdout(), Stderr: child.Stderr(),
		Process: schema.ProcessIdentity{PID: child.PID(), Executable: f.Path, Arguments: args}, Resources: system.Usage(child.State())}
	if err != nil {
		return result, fmt.Errorf("Fortio execution: %w", err)
	}
	result.HTTP, err = Parse(result.Raw, opts)
	return result, err
}

func (f *Fortio) Stop(ctx context.Context) error {
	f.mu.Lock()
	child := f.child
	cancel := f.runCancel
	f.mu.Unlock()
	if child == nil {
		if cancel != nil {
			cancel()
		}
		return nil
	}
	return child.Stop(ctx)
}

type fortioJSON struct {
	Version           string
	ActualQPS         float64
	ActualDuration    int64
	RetCodes          map[string]int64
	DurationHistogram struct {
		Count int64
		Data  []struct {
			Start, End float64
			Count      int64
		}
		Percentiles []struct{ Percentile, Value float64 }
	}
}

func Parse(data []byte, opts Options) (schema.HTTPResult, error) {
	var raw fortioJSON
	var result schema.HTTPResult
	if err := opts.Validate(); err != nil {
		return result, err
	}
	if len(data) > process.MaxOutput {
		return result, process.ErrOutputLimit
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return result, fmt.Errorf("Fortio JSON: %w", err)
	}
	if raw.Version != Version {
		return result, fmt.Errorf("unexpected Fortio JSON version %q", raw.Version)
	}
	result = schema.HTTPResult{Requests: raw.DurationHistogram.Count, Successful: raw.RetCodes["200"],
		StatusCodes: raw.RetCodes, DurationSeconds: float64(raw.ActualDuration) / nanosecondsPerSecond,
		TargetQPS: opts.QPS, ActualQPS: raw.ActualQPS, ResolutionSeconds: opts.Resolution, RawPath: opts.RawPath,
		HistogramType: "fortio variable-width histogram", CoordinatedOmission: "uncorrected service-time histogram",
		PercentilesSeconds: make(map[string]float64)}
	for _, bucket := range raw.DurationHistogram.Data {
		result.Buckets = append(result.Buckets, schema.HistogramBucket{Start: bucket.Start, End: bucket.End, Count: bucket.Count})
	}
	for _, p := range raw.DurationHistogram.Percentiles {
		key := "p" + strconv.FormatFloat(p.Percentile, 'g', -1, 64)
		if _, exists := result.PercentilesSeconds[key]; exists {
			return result, errors.New("duplicate percentile")
		}
		result.PercentilesSeconds[key] = p.Value
	}
	for _, key := range []string{"p50", "p75", "p90", "p95", "p99", "p99.9"} {
		if _, ok := result.PercentilesSeconds[key]; !ok {
			return result, fmt.Errorf("missing percentile %s", key)
		}
	}
	if result.ActualQPS < result.TargetQPS*saturationRatio {
		result.Warnings = append(result.Warnings, "target QPS not achieved")
	}
	if result.Requests < minimumTailSamples {
		result.Warnings = append(result.Warnings, "few observations for p99.9")
	}
	if result.Successful != result.Requests {
		result.Warnings = append(result.Warnings, "HTTP or transport failures")
	}
	return result, schema.ValidateHTTP(&result)
}
