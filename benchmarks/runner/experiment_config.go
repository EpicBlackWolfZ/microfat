package runner

import (
	json "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/load"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/report"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/system"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/workloads/server"
	"github.com/EpicBlackWolfZ/microfat/internal/cgroup"
)

const (
	minimalProfile          = "minimal"
	defaultMatchedProcs     = 2
	tuningMatched           = "matched"
	defaultExperimentBlocks = 5
	defaultDurationMS       = 1000
	defaultWarmupMS         = 200
	defaultSampleMS         = 25
	minimumSampleMS         = 10
	maxRunDuration          = 6 * time.Hour
	defaultQPS              = 1000
	defaultResolution       = 0.00001
	defaultFormat           = 2
)

type ExperimentConfig struct {
	RunnerIdentity   string         `json:"runner_identity"`
	DisableObserver  bool           `json:"disable_observer"`
	ExecDiagnostics  bool           `json:"exec_diagnostics"`
	Version          string         `json:"version"`
	Name             string         `json:"name"`
	Workload         string         `json:"workload"`
	Blocks           int            `json:"blocks"`
	DurationMS       int            `json:"duration_ms"`
	WarmupMS         int            `json:"warmup_ms"`
	SampleMS         int            `json:"sample_ms"`
	Seed             uint64         `json:"seed"`
	PayloadBytes     int            `json:"payload_bytes"`
	Iterations       int            `json:"iterations"`
	Concurrency      int            `json:"concurrency"`
	QPS              float64        `json:"qps"`
	Resolution       float64        `json:"resolution_seconds"`
	Format           int            `json:"format"`
	Profile          string         `json:"profile"`
	Codec            string         `json:"codec"`
	Dictionary       bool           `json:"dictionary"`
	SpecializedLevel string         `json:"specialized_level"`
	CacheStates      []string       `json:"cache_states"`
	Tuning           string         `json:"tuning"`
	GOMAXPROCS       int            `json:"gomaxprocs"`
	GOMEMLIMIT       string         `json:"gomemlimit"`
	GOGC             string         `json:"gogc"`
	Target           system.Options `json:"target"`
	Generator        system.Options `json:"generator"`
}

func DefaultExperimentConfig() ExperimentConfig {
	return ExperimentConfig{Version: "v1", Name: "smoke", Workload: "mixed", Blocks: defaultExperimentBlocks,
		DurationMS: defaultDurationMS, WarmupMS: defaultWarmupMS, SampleMS: defaultSampleMS, Seed: 1,
		PayloadBytes: server.DefaultPayload, Iterations: server.DefaultIterations, Concurrency: server.DefaultConcurrency,
		QPS: defaultQPS, Resolution: defaultResolution, Format: defaultFormat, Profile: "full", Codec: "zstd",
		CacheStates: []string{"cold", "warm"}, Tuning: tuningMatched, GOMAXPROCS: defaultMatchedProcs, GOMEMLIMIT: "1GiB", GOGC: "100",
		Target: system.Options{CPUPeriodUS: system.DefaultPeriod}, Generator: system.Options{CPUPeriodUS: system.DefaultPeriod}}
}

func ReadExperimentConfig(path string) (ExperimentConfig, error) {
	cfg := DefaultExperimentConfig()
	data, err := report.ReadBounded(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg, json.RejectUnknownMembers(true)); err != nil {
		return cfg, err
	}
	return cfg, cfg.Validate()
}

func (c ExperimentConfig) Validate() error {
	if c.Version != "v1" || !schema.SafeReference(c.Name) || strings.Contains(c.Name, "/") {
		return errors.New("invalid suite version or name")
	}
	if c.Workload != "mixed" && c.Workload != "cpu" && c.Workload != "memory" {
		return errors.New("unknown server workload")
	}
	if err := c.validateTiming(); err != nil {
		return err
	}
	if c.Format != 1 && c.Format != defaultFormat || c.Profile != "full" && c.Profile != minimalProfile ||
		c.Codec != "zstd" && c.Codec != "lz4" && c.Codec != "none" || c.Dictionary && c.Codec != "zstd" {
		return errors.New("unsupported format/profile/codec/dictionary combination")
	}
	if err := c.validateControls(); err != nil {
		return err
	}
	work := server.Defaults()
	work.PayloadBytes, work.Iterations, work.Concurrency = c.PayloadBytes, c.Iterations, c.Concurrency
	if err := work.Validate(); err != nil {
		return err
	}
	return (load.Options{Duration: time.Duration(c.DurationMS) * time.Millisecond, QPS: c.QPS,
		Concurrency: c.Concurrency, Resolution: c.Resolution, RawPath: "trials/validation/fortio.json"}).Validate()
}

type RunOptions struct {
	Repository     string
	BaseRepository string
	Go             string
	Fortio         string
	Helper         string
	OutputDir      string
	LogWriter      interface{ Write([]byte) (int, error) }
}

func (o *RunOptions) resolve() error {
	if o.Repository == "" {
		o.Repository = "."
	}
	if o.Go == "" {
		o.Go = "go"
	}
	if o.LogWriter == nil {
		o.LogWriter = os.Stderr
	}
	if o.Fortio == "" {
		return errors.New("Fortio path required; install the pinned tool with make benchmark-tools")
	}
	if o.Helper == "" {
		var err error
		o.Helper, err = os.Executable()
		if err != nil {
			return err
		}
	}
	if o.OutputDir == "" {
		return errors.New("output directory required")
	}
	for _, field := range []*string{&o.Repository, &o.Fortio, &o.Helper, &o.OutputDir} {
		absolute, err := filepath.Abs(*field)
		if err != nil {
			return fmt.Errorf("resolving path: %w", err)
		}
		*field = absolute
	}
	return nil
}

func (c ExperimentConfig) validateTiming() error {
	const maxConfigurations = 10 // Two revisions of up to five execution configurations.
	if c.Blocks <= 0 || c.Blocks > schema.MaxTrials/maxConfigurations || c.DurationMS <= 0 ||
		c.DurationMS > int(load.MaxDuration/time.Millisecond) || c.WarmupMS < 0 ||
		c.WarmupMS > int(load.MaxDuration/time.Millisecond) || c.SampleMS < minimumSampleMS || c.SampleMS > c.DurationMS {
		return errors.New("invalid experiment timing or trial count")
	}
	if time.Duration(c.Blocks)*time.Duration(c.DurationMS+c.WarmupMS)*maxConfigurations*time.Millisecond > maxRunDuration {
		return errors.New("experiment exceeds six hour trial budget; shard the suite")
	}
	return nil
}

func (c ExperimentConfig) validateControls() error {
	if c.Tuning != tuningMatched && c.Tuning != "on-off" {
		return errors.New("tuning must be matched or on-off")
	}
	if c.GOMAXPROCS <= 0 || c.GOMEMLIMIT == "" || c.GOGC == "" {
		return errors.New("matched tuning requires explicit runtime settings")
	}
	if _, err := cgroup.ParseByteSize(c.GOMEMLIMIT); err != nil {
		return fmt.Errorf("GOMEMLIMIT: %w", err)
	}
	if c.GOGC != "off" {
		value, err := strconv.Atoi(c.GOGC)
		if err != nil || value < 0 {
			return errors.New("GOGC must be nonnegative or off")
		}
	}
	seen := make(map[string]bool)
	for _, state := range c.CacheStates {
		if state != "cold" && state != "warm" || seen[state] {
			return errors.New("invalid cache state")
		}
		seen[state] = true
	}
	if len(c.CacheStates) == 0 {
		return errors.New("at least one cache state required")
	}
	if err := c.Target.Validate(); err != nil {
		return err
	}
	if err := c.Generator.Validate(); err != nil {
		return err
	}
	for _, cpu := range c.Target.Affinity {
		for _, generatorCPU := range c.Generator.Affinity {
			if cpu == generatorCPU {
				return errors.New("target and generator CPU masks overlap")
			}
		}
	}
	return nil
}
