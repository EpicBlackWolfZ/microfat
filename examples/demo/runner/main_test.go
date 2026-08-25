package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCalculateStats(t *testing.T) {
	// Empty slice
	empty := calculateStats(nil)
	if empty.Mean != 0 {
		t.Errorf("expected 0 mean for empty slice")
	}

	// Single element
	single := calculateStats([]time.Duration{10 * time.Millisecond})
	if single.Mean != 10*time.Millisecond || single.Min != 10*time.Millisecond || single.Max != 10*time.Millisecond {
		t.Errorf("unexpected single stats: %+v", single)
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

const (
	flagJSON = "--json"
	cmdAll   = "all"
)

func TestPrepareBenchArgs(t *testing.T) {
	stdArgs := prepareBenchArgs(false, false)
	if !reflect.DeepEqual(stdArgs, []string{flagJSON, cmdAll}) {
		t.Errorf("unexpected std args: %v", stdArgs)
	}

	heavyArgs := prepareBenchArgs(false, true)
	if !reflect.DeepEqual(heavyArgs, []string{flagJSON, cmdAll, "--heavy"}) {
		t.Errorf("unexpected heavy args: %v", heavyArgs)
	}

	ultraArgs := prepareBenchArgs(true, false)
	if !reflect.DeepEqual(ultraArgs, []string{flagJSON, cmdAll, "--ultra"}) {
		t.Errorf("unexpected ultra args: %v", ultraArgs)
	}
}

func TestPrintSummaryTables(t *testing.T) {
	configs := []Config{
		{Name: "1. Standalone v1 (Baseline)", Path: "/path/v1", Size: 2 * 1024 * 1024},
		{Name: "2. Standalone v3", Path: "/path/v3", Size: 2 * 1024 * 1024},
		{Name: "3. Microfat Universal FAT", Path: "/path/fat", Size: 4 * 1024 * 1024},
		{Name: "4. Trimmed FAT", Path: "/path/trimmed", Size: 2 * 1024 * 1024},
		{Name: "5. Optimized v3", Path: "/path/opt", Size: 2 * 1024 * 1024},
	}
	sampleStat := Stats{
		Mean:   10 * time.Millisecond,
		Median: 10 * time.Millisecond,
		Min:    9 * time.Millisecond,
		Max:    11 * time.Millisecond,
		P95:    11 * time.Millisecond,
		StdDev: 1 * time.Millisecond,
	}
	stats := []Stats{sampleStat, sampleStat, sampleStat, sampleStat, sampleStat}

	// Should format tables without error in both normal and ultra mode
	printSummaryTables(configs, stats, stats, stats, false)
	printSummaryTables(configs, stats, stats, stats, true)
}

func TestRunnerHelpers(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Test getFileSize
	testFile := filepath.Join(tempDir, "test.txt")
	_ = os.WriteFile(testFile, []byte("1234567890"), 0o644)
	if getFileSize(testFile) != 10 {
		t.Errorf("expected file size 10, got %d", getFileSize(testFile))
	}

	// 2. Test resolveBinary
	_ = resolveBinary("sh")
	_ = resolveBinary("nonexistent-command-xyz")

	// 3. Test mustRun
	mustRun(tempDir, "sh", "-c", "echo ok", "ENV:TEST_VAR=1")

	// 4. Test runWarmups, measureStartup, measureCompute with mock executable script
	mockScript := filepath.Join(tempDir, "mock_bench.sh")
	scriptContent := `#!/bin/sh
if [ "$1" = "--help" ]; then
    echo "help message"
    exit 0
fi
echo '{"version":"1.0","total_compute_ms":12.5}'
`
	if err := os.WriteFile(mockScript, []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("writing mock script: %v", err)
	}

	mockConfigs := []Config{
		{Name: "Mock 1", Path: mockScript, Size: 100},
		{Name: "Mock 2", Path: mockScript, Size: 100},
	}

	runWarmups(mockConfigs, 1, []string{"--json", "all"})
	startup := measureStartup(mockConfigs, 2)
	if len(startup) != 2 || startup[0].Mean <= 0 {
		t.Errorf("unexpected startup stats: %+v", startup)
	}

	pure, wall := measureCompute(mockConfigs, 2, []string{"--json", "all"}, false)
	if len(pure) != 2 || pure[0].Mean <= 0 || len(wall) != 2 {
		t.Errorf("unexpected compute stats: pure=%+v wall=%+v", pure, wall)
	}

	// Measure compute with isUltra = true
	_, _ = measureCompute(mockConfigs, 1, []string{"--json", "all"}, true)
}
