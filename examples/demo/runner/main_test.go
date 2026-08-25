package main

import (
	"testing"
	"time"
)

func TestCalculateStats(t *testing.T) {
	// Empty slice
	empty := calculateStats(nil)
	if empty.Mean != 0 {
		t.Errorf("expected 0 mean for empty slice")
	}

	// Normal slice
	durations := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
		50 * time.Millisecond,
	}

	stats := calculateStats(durations)
	if stats.Mean != 30*time.Millisecond {
		t.Errorf("expected mean 30ms, got %v", stats.Mean)
	}
	if stats.Median != 30*time.Millisecond {
		t.Errorf("expected median 30ms, got %v", stats.Median)
	}
	if stats.Min != 10*time.Millisecond || stats.Max != 50*time.Millisecond {
		t.Errorf("unexpected min/max: %v / %v", stats.Min, stats.Max)
	}
}
