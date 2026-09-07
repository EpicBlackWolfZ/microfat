// Package runner orchestrates the benchmark execution lifecycle: host environment detection,
// workload setup, cache warming, timed trial execution, statistical analysis, and evidence serialization.
package runner

import (
	"errors"
	"io"
	"os"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/workloads"
)

const (
	defaultTrialCount     = 5
	defaultTrialDuration  = 1 * time.Second
	defaultWarmupDuration = 500 * time.Millisecond
	defaultScenarioName   = "default"
)

// Sentinel runner configuration errors.
var (
	ErrNilWorkload        = errors.New("workload cannot be nil")
	ErrInvalidTrialCount  = errors.New("trials count must be greater than zero")
	ErrInvalidDuration    = errors.New("trial duration must be positive")
	ErrInvalidWarmup      = errors.New("warmup duration cannot be negative")
)

// Config configures the parameters for a benchmark run.
type Config struct {
	Workload           workloads.Workload `json:"-"`
	ScenarioName       string             `json:"scenario_name"`
	ScenarioParameters map[string]string  `json:"scenario_parameters,omitempty"`
	Trials             int                `json:"trials"`
	TrialDuration      time.Duration      `json:"trial_duration"`
	WarmupDuration     time.Duration      `json:"warmup_duration"`
	BatchSize          int                `json:"batch_size,omitempty"`
	Title              string             `json:"title,omitempty"`
	Description        string             `json:"description,omitempty"`
	ID                 string             `json:"id,omitempty"`
	CreatedAt          string             `json:"created_at,omitempty"`
	LogWriter          io.Writer          `json:"-"`
	OutputPath         string             `json:"output_path,omitempty"`
}

// Validate verifies that the Config options conform to execution invariants.
func (c *Config) Validate() error {
	if c.Workload == nil {
		return ErrNilWorkload
	}
	if c.Trials <= 0 {
		return ErrInvalidTrialCount
	}
	if c.TrialDuration <= 0 {
		return ErrInvalidDuration
	}
	if c.WarmupDuration < 0 {
		return ErrInvalidWarmup
	}
	return nil
}

// Defaults populates missing configuration parameters with authoritative defaults.
func (c *Config) Defaults() {
	if c.ScenarioName == "" {
		if c.Workload != nil {
			c.ScenarioName = c.Workload.Name()
		} else {
			c.ScenarioName = defaultScenarioName
		}
	}
	if c.Trials <= 0 {
		c.Trials = defaultTrialCount
	}
	if c.TrialDuration <= 0 {
		c.TrialDuration = defaultTrialDuration
	}
	if c.WarmupDuration <= 0 {
		c.WarmupDuration = defaultWarmupDuration
	}
	if c.LogWriter == nil {
		c.LogWriter = os.Stderr
	}
}
