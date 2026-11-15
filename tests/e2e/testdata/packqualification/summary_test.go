//go:build linux

package main

import (
	"testing"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrozenPlan(t *testing.T) {
	t.Parallel()
	trials := plan()
	require.Len(t, trials, 1152)
	first := trials[0]
	seen := make(map[cell]int)
	for _, p := range trials {
		seen[p.Cell]++
		if p.Block%2 == 0 {
			assert.Equal(t, [2]string{serialArm, defaultArm}, p.Arms)
		} else {
			assert.Equal(t, [2]string{defaultArm, serialArm}, p.Arms)
		}
	}
	assert.Len(t, seen, 48)
	for _, count := range seen {
		assert.Equal(t, 24, count)
	}
	assert.NotEqual(t, first.Cell, trials[len(seen)].Cell)
	result := summarize(nil, trials)
	assert.Equal(t, 768, result.PlannedComparisons)
	assert.Equal(t, 3, result.IntervalRank)
	assert.Equal(t, decisionInconclusive, result.Decision)
	assert.Equal(t, blocks, result.Cells[0].MissingPairs)
}

func TestDecisions(t *testing.T) {
	t.Parallel()
	cellUnderTest := cell{"stripped", "balanced", 4, 512}
	tests := []struct {
		name, want, evidence string
		change               func(serial, candidate *attempt)
	}{
		{"parallel win", decisionPass, "packing-time reduction", nil},
		{"serial fallback", decisionPass, "", func(_, a *attempt) { a.Worker.Workers = 1 }},
		{"varying workers", decisionInconclusive, "admission varied", func(_, a *attempt) {
			if a.Block == 0 {
				a.Worker.Workers = 1
			}
		}},
		{"candidate-only OOM", decisionRegression, "failure or OOM", func(_, a *attempt) { setOOM(a) }},
		{"baseline and candidate OOM", decisionInconclusive, "", func(s, a *attempt) { setOOM(s); setOOM(a) }},
		{"packing failure", decisionRegression, "failure or OOM", func(_, a *attempt) { a.ExitCode = new(1) }},
		{"control failure", decisionInconclusive, "", func(_, a *attempt) { a.ControlError = "readback" }},
		{"comparison unavailable", decisionInconclusive, "", func(s, _ *attempt) { s.Worker.PackNS = 0 }},
		{"peak over hard cap", decisionRegression, "memory.peak", func(_, a *attempt) {
			a.Cgroup["memory.peak"] = measurement(float64((a.Cell.MemoryMiB + 1) * mebibyte))
		}},
		{"unequal output", decisionRegression, "artifacts differ", func(_, a *attempt) { a.ArtifactSHA256 = "different" }},
		{"slow default", decisionRegression, "5% regression", func(_, a *attempt) { a.Worker.PackNS = 120 }},
		{"no regression established", decisionInconclusive, "upper bound", func(_, a *attempt) {
			if a.Block < blocks/2 {
				a.Worker.PackNS = 120
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var schedule []pair
			var observations []attempt
			for block := range blocks {
				schedule = append(schedule, pair{cellUnderTest, block, [2]string{serialArm, defaultArm}})
				serial := sample(cellUnderTest, block, serialArm, 100, 1)
				candidate := sample(cellUnderTest, block, defaultArm, 80, 3)
				if tc.change != nil {
					tc.change(&serial, &candidate)
				}
				observations = append(observations, serial, candidate)
			}
			report := summarize(observations, schedule)
			require.Len(t, report.Cells, 1)
			cellReport := report.Cells[0]
			assert.Equal(t, tc.want, report.Decision)
			assert.Equal(t, tc.want, cellReport.Decision)
			if tc.evidence != "" {
				require.NotEmpty(t, cellReport.Reasons)
				assert.Contains(t, cellReport.Reasons[0], tc.evidence)
			}
		})
	}
}

func sample(c cell, block int, arm string, packNS int64, workers int) attempt {
	return attempt{
		Cell: c, Block: block, Arm: arm, ExitCode: new(0),
		ArtifactSHA256: "same", ArtifactBytes: 1000, Worker: workerResult{PackNS: packNS, Workers: workers},
		Cgroup: map[string]schema.Measurement{
			"memory.peak": measurement(100 * mebibyte), "memory.events/oom": measurement(0),
			"memory.events/oom_kill": measurement(0),
		},
	}
}

func setOOM(a *attempt) {
	a.ExitCode = new(-1)
	a.Cgroup["memory.events/oom"] = measurement(1)
	a.Cgroup["memory.events/oom_kill"] = measurement(1)
}

func measurement(value float64) schema.Measurement {
	return schema.Measurement{Value: &value}
}
