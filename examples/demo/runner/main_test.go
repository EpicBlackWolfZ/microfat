package main

import (
	"bytes"
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
	stdArgs := prepareBenchArgs(false, false, false)
	if !reflect.DeepEqual(stdArgs, []string{flagJSON, cmdAll}) {
		t.Errorf("unexpected std args: %v", stdArgs)
	}

	heavyArgs := prepareBenchArgs(false, true, false)
	if !reflect.DeepEqual(heavyArgs, []string{flagJSON, cmdAll, "--heavy"}) {
		t.Errorf("unexpected heavy args: %v", heavyArgs)
	}

	ultraArgs := prepareBenchArgs(true, false, false)
	if !reflect.DeepEqual(ultraArgs, []string{flagJSON, cmdAll, "--ultra"}) {
		t.Errorf("unexpected ultra args: %v", ultraArgs)
	}

	simdArgs := prepareBenchArgs(false, false, true)
	if !reflect.DeepEqual(simdArgs, []string{flagJSON, cmdAll, "--simd"}) {
		t.Errorf("unexpected simd args: %v", simdArgs)
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

	// 2. Test resolveBinary, resolveGoBinary, and findRepoRoot
	_ = resolveBinary("sh")
	_ = resolveBinary("nonexistent-command-xyz")
	_ = resolveGoBinary()
	_ = findRepoRoot()

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

func TestParseDispatchTelemetry(t *testing.T) {
	// Valid telemetry JSON
	validJSON := []byte(`{"event":"dispatch","timestamp_unix_nano":1724790000000000000,"host_arch":"amd64","host_level":"v3",` +
		`"selected_variant":"v3","selected_sha256":"abc123","selected_size_bytes":2097152,"exec_mode":"memfd",` +
		`"cgroup_version":2,"decompression_duration_us":150,"total_launcher_us":450}` + "\n")
	dt, err := parseDispatchTelemetry(validJSON)
	if err != nil {
		t.Fatalf("unexpected error parsing valid telemetry: %v", err)
	}
	if dt.Event != "dispatch" || dt.SelectedVariant != "v3" || dt.TotalLauncherUs != 450 || dt.DecompressionDurationUs != 150 {
		t.Errorf("unexpected parsed telemetry: %+v", dt)
	}

	// Multi-line stderr with logging prefix before JSON
	multiLine := []byte("[DEBUG] Starting stub launcher\n" +
		`{"event":"dispatch","selected_variant":"v4","exec_mode":"cache","total_launcher_us":800}` + "\n")
	dt2, err := parseDispatchTelemetry(multiLine)
	if err != nil {
		t.Fatalf("unexpected error parsing multi-line telemetry: %v", err)
	}
	if dt2.SelectedVariant != "v4" || dt2.TotalLauncherUs != 800 {
		t.Errorf("unexpected multi-line telemetry: %+v", dt2)
	}

	// Empty stderr
	if _, err := parseDispatchTelemetry([]byte("")); err == nil {
		t.Errorf("expected error for empty stderr")
	}

	// Non-JSON stderr
	if _, err := parseDispatchTelemetry([]byte("random stderr text\n")); err == nil {
		t.Errorf("expected error for non-JSON stderr")
	}

	// Different event JSON
	if _, err := parseDispatchTelemetry([]byte(`{"event":"other"}` + "\n")); err == nil {
		t.Errorf("expected error for non-dispatch event JSON")
	}
}

func TestExportStartupCSV(t *testing.T) {
	tempDir := t.TempDir()
	csvPath := filepath.Join(tempDir, "test_startup.csv")

	observations := []StartupObservation{
		{
			Timestamp:          "2026-08-27T22:00:00.000Z",
			Iteration:          1,
			ConfigName:         "1. Native v1",
			ExecMode:           "native",
			SelectedVariant:    "v1",
			TotalWallDuration:  12500 * time.Microsecond,
			LauncherInternalUs: 0,
			DecompressionUs:    0,
			CgroupVersion:      0,
		},
		{
			Timestamp:          "2026-08-27T22:00:01.000Z",
			Iteration:          1,
			ConfigName:         "3. Universal FAT (Cold memfd)",
			ExecMode:           "memfd",
			SelectedVariant:    "v3",
			TotalWallDuration:  13800 * time.Microsecond,
			LauncherInternalUs: 450,
			DecompressionUs:    120,
			CgroupVersion:      2,
		},
	}

	if err := exportStartupCSV(csvPath, observations); err != nil {
		t.Fatalf("exportStartupCSV failed: %v", err)
	}

	content, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("reading exported CSV: %v", err)
	}

	if !bytes.Contains(content, []byte("launcher_internal_us")) {
		t.Errorf("CSV missing header: %s", content)
	}
	if !bytes.Contains(content, []byte("Universal FAT (Cold memfd)")) {
		t.Errorf("CSV missing observation row: %s", content)
	}

	// Invalid path
	if err := exportStartupCSV("/invalid-dir-xyz/sub/bench.csv", observations); err == nil {
		t.Errorf("expected error exporting to invalid directory")
	}
}

func TestPrintStartupSummaryTables(t *testing.T) {
	sampleStat := Stats{
		Mean:   12 * time.Millisecond,
		Median: 12 * time.Millisecond,
		Min:    10 * time.Millisecond,
		Max:    15 * time.Millisecond,
		P95:    14 * time.Millisecond,
		P99:    15 * time.Millisecond,
		StdDev: 1 * time.Millisecond,
	}
	summaries := []StartupSummary{
		{
			Scenario:           StartupScenario{Name: "1. Native v1", ExecMode: "native", Size: 2000000},
			WallStats:          sampleStat,
			LauncherStats:      Stats{},
			DecompressionStats: Stats{},
		},
		{
			Scenario:           StartupScenario{Name: "2. Native v3", ExecMode: "native", Size: 2000000},
			WallStats:          sampleStat,
			LauncherStats:      Stats{},
			DecompressionStats: Stats{},
		},
		{
			Scenario:           StartupScenario{Name: "3. Universal FAT (Cold memfd)", ExecMode: "memfd", Size: 4000000},
			WallStats:          sampleStat,
			LauncherStats:      sampleStat,
			DecompressionStats: sampleStat,
		},
	}

	// Formatting should not panic
	printStartupSummaryTables(summaries)
}

func TestStartupScenariosExecution(t *testing.T) {
	tempDir := t.TempDir()

	mockScript := filepath.Join(tempDir, "mock_startup.sh")
	scriptContent := `#!/bin/sh
if [ "$1" = "--startup-only" ]; then
    echo "READY"
    >&2 echo '{"event":"dispatch","selected_variant":"v3","total_launcher_us":350,"decompression_duration_us":100,"exec_mode":"memfd"}'
    exit 0
fi
echo '{"version":"1.0","total_compute_ms":10.0}'
`
	if err := os.WriteFile(mockScript, []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("writing mock script: %v", err)
	}

	scenarios := []StartupScenario{
		{Name: "Mock Native", Path: mockScript, ExecMode: "native", Size: 100},
		{Name: "Mock Cold Cache", Path: mockScript, ExecMode: "cold-cache", IsCold: true, Size: 100},
		{Name: "Mock Memfd", Path: mockScript, ExecMode: "memfd", Size: 100},
	}

	runStartupWarmups(scenarios, 1, tempDir)

	summaries, observations := measureStartupScenarios(scenarios, 2, tempDir)
	if len(summaries) != 3 {
		t.Fatalf("expected 3 summaries, got %d", len(summaries))
	}
	if len(observations) != 6 {
		t.Fatalf("expected 6 observations, got %d", len(observations))
	}

	if observations[0].TotalWallDuration <= 0 {
		t.Errorf("expected positive wall duration, got %v", observations[0].TotalWallDuration)
	}
	if observations[0].LauncherInternalUs != 350 {
		t.Errorf("expected 350 launcher us, got %d", observations[0].LauncherInternalUs)
	}
}
