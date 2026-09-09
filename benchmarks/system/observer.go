package system

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

type ObserverConfig struct {
	PID         int      `json:"pid"`
	PhaseFile   string   `json:"phase_file"`
	IntervalMS  int      `json:"interval_ms"`
	CgroupPaths []string `json:"cgroup_paths"`
}

// Observe runs in its own process so sampler allocations never enter target resource accounting.
func Observe(ctx context.Context, cfg ObserverConfig, writer io.Writer) error {
	const minimumInterval = 1
	if cfg.PID <= 0 || cfg.IntervalMS < minimumInterval || cfg.IntervalMS > 1000 || !filepath.IsAbs(cfg.PhaseFile) {
		return errors.New("invalid observer configuration")
	}
	ticker := time.NewTicker(time.Duration(cfg.IntervalMS) * time.Millisecond)
	defer ticker.Stop()
	start := time.Now()
	sandbox := &Sandbox{Paths: cfg.CgroupPaths}
	for count := 0; count < schema.MaxMeasurements; count++ {
		phaseBytes, err := os.ReadFile(cfg.PhaseFile) // #nosec G304 -- explicit benchmark-owned phase file.
		if err != nil {
			return err
		}
		phase := strings.TrimSpace(string(phaseBytes))
		if phase != "startup" && phase != "warmup" && phase != "steady_state" {
			return errors.New("invalid measurement phase")
		}
		metrics := ReadProcess("/proc", cfg.PID, phase)
		maps.Copy(metrics, sandbox.Read(phase))
		sample := schema.ResourceSample{ElapsedNS: time.Since(start).Nanoseconds(), Phase: phase, Metrics: metrics}
		if err := json.MarshalWrite(writer, sample, json.Deterministic(true)); err != nil {
			return err
		}
		if _, err := writer.Write([]byte{'\n'}); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return errors.New("observer sample limit reached")
}
