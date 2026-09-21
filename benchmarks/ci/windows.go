package ci

import (
	"fmt"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
)

// Duration precision may differ across the load generator and observer clocks.
// Sampling permits one missed tick plus precision, and two edge ticks for a span.
const windowPrecision = 50 * time.Millisecond

func qualifyObservedWindows(trial schema.ProcessTrial, cfg hostedSchedule) []string {
	var reasons []string
	add := func(reason string) { reasons = append(reasons, trial.ID+": "+reason) }
	if trial.SampleIntervalMS <= 0 || trial.SampleIntervalMS > releaseSampleMS || trial.SampleIntervalMS != cfg.SampleMS {
		add("effective sample interval does not match the qualified schedule")
		return reasons
	}
	if len(trial.Samples) != trial.SampleCount || len(trial.Samples) < 2 {
		add("observed telemetry samples unavailable or incomplete")
		return reasons
	}
	interval := time.Duration(trial.SampleIntervalMS) * time.Millisecond
	if !completeSampleTimeline(trial, interval) {
		add("telemetry timestamps contain gaps or contradict the trial duration")
	}
	var totalSeconds float64
	for _, phase := range []struct {
		name string
		ms   int
		load *schema.HTTPResult
	}{{"warmup", cfg.WarmupMS, trial.Warmup}, {"steady_state", cfg.DurationMS, trial.Load}} {
		if phase.load == nil {
			add(phase.name + " load observations unavailable; duration cannot be established")
			continue
		}
		observed := phase.load.DurationSeconds
		totalSeconds += observed
		if observed+windowPrecision.Seconds() < float64(phase.ms)/float64(time.Second/time.Millisecond) ||
			phase.load.Requests == 0 || phase.load.Successful != phase.load.Requests {
			add(phase.name + " load ended early or contains request failures")
		}
		if !completePhaseSamples(trial.Samples, phase.name, observed, interval) {
			add(fmt.Sprintf("%s telemetry does not cover the observed load window", phase.name))
		}
	}
	if float64(trial.DurationNS)/float64(time.Second)+windowPrecision.Seconds() < totalSeconds {
		add("total trial duration is shorter than its observed warmup and measurement")
	}
	return reasons
}

func completeSampleTimeline(trial schema.ProcessTrial, interval time.Duration) bool {
	previous := int64(-1)
	steady := false
	for _, sample := range trial.Samples {
		if sample.ElapsedNS <= previous || sample.ElapsedNS > trial.DurationNS ||
			previous >= 0 && sample.ElapsedNS-previous > int64(2*interval+windowPrecision) {
			return false
		}
		if sample.Phase == "steady_state" {
			steady = true
		} else if steady && sample.Phase == "warmup" {
			return false
		}
		previous = sample.ElapsedNS
	}
	return true
}

func completePhaseSamples(samples []schema.ResourceSample, phase string, seconds float64, interval time.Duration) bool {
	first, last := int64(-1), int64(-1)
	count := 0
	for _, sample := range samples {
		if sample.Phase != phase {
			continue
		}
		if first < 0 {
			first = sample.ElapsedNS
		}
		last = sample.ElapsedNS
		count++
	}
	return count >= 2 && float64(last-first)/float64(time.Second)+(2*interval+windowPrecision).Seconds() >= seconds
}
