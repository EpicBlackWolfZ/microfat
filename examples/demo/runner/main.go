package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
	json "encoding/json/v2"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

const (
	bytesInMegabyte    = 1024.0 * 1024.0
	msPerMicro         = 1000.0
	secPerMilli        = 1000.0
	percentMultiplier  = 100.0
	p95Multiplier      = 0.95
	p99Multiplier      = 0.99
	medianMultiplier   = 0.50
	stdWarmup          = 5
	ultraWarmup        = 1
	stdIterations      = 50
	startupIterations  = 50
	heavyIterations    = 20
	ultraIterations    = 3
	dirPermission      = 0o750
	envPrefixLen       = 4
	timestampLayout    = "2006-01-02T15:04:05.000Z07:00"
	csvTimestampLayout = "20060102-150405"
	execModeNative     = "native"
	execModeMemfd      = "memfd"
	execModeColdCache  = "cold-cache"
	execModeWarmCache  = "warm-cache"
	envExecModeMemfd   = "MICROFAT_EXEC_MODE=memfd"
)

type Config struct {
	Name string
	Path string
	Size int64
}

type Stats struct {
	Mean   time.Duration
	StdDev time.Duration
	Median time.Duration
	P95    time.Duration
	P99    time.Duration
	Min    time.Duration
	Max    time.Duration
}

type DemoReport struct {
	Version      string  `json:"version"`
	TotalCompute float64 `json:"total_compute_ms"`
}

type StartupScenario struct {
	Name      string
	Path      string
	ExecMode  string
	IsCold    bool
	Env       []string
	Size      int64
	WarmCache string
}

type StartupObservation struct {
	Timestamp          string        `json:"timestamp"`
	Iteration          int           `json:"iteration"`
	ConfigName         string        `json:"config_name"`
	ExecMode           string        `json:"exec_mode"`
	SelectedVariant    string        `json:"selected_variant"`
	TotalWallDuration  time.Duration `json:"total_wall_duration"`
	LauncherInternalUs int64         `json:"launcher_internal_us"`
	DecompressionUs    int64         `json:"decompression_us"`
	CgroupVersion      int           `json:"cgroup_version"`
}

type StartupSummary struct {
	Scenario           StartupScenario
	WallStats          Stats
	LauncherStats      Stats
	DecompressionStats Stats
}

func main() {
	startupPtr := flag.Bool("startup", false, "Run microsecond startup latency and stub overhead benchmark suite")
	csvPtr := flag.String("csv", "", "Path to export CSV benchmark results (defaults to timestamped file in -startup mode)")
	ultraPtr := flag.Bool("ultra", false, "Run ULTRA sustained heavy compute benchmark (10s of seconds)")
	heavyPtr := flag.Bool("heavy", false, "Run heavy compute-intensive benchmark (~500ms workload)")
	simdPtr := flag.Bool("simd", false, "Run benchmarks with experimental wide-vector unrolled SIMD mode enabled")
	itersPtr := flag.Int("n", 0, "Number of benchmark iterations")
	flag.Parse()

	isStartup := *startupPtr
	isUltra := *ultraPtr
	isHeavy := *heavyPtr && !isUltra
	isSIMD := *simdPtr

	iterations := stdIterations
	warmups := stdWarmup

	switch {
	case isStartup:
		iterations = startupIterations
	case isUltra:
		iterations = ultraIterations
		warmups = ultraWarmup
	case isHeavy:
		iterations = heavyIterations
	}

	if *itersPtr > 0 {
		iterations = *itersPtr
	}

	benchDir, err := os.MkdirTemp("", "microfat-bench-*")
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(benchDir) }()

	srcDir := filepath.Clean("..")
	microfatStub := resolveBinary("microfat-stub")
	microfatCli := resolveBinary("microfat")

	if isStartup {
		runStartupBenchmarkSuite(srcDir, benchDir, microfatStub, microfatCli, *csvPtr, iterations, warmups)
		return
	}

	modeStr := "Standard Workload (~110ms)"
	if isUltra {
		modeStr = "ULTRA Sustained Heavy Workload (10s of seconds per run)"
	} else if isHeavy {
		modeStr = "HEAVY Sustained Compute (~500ms per run)"
	}
	if isSIMD {
		modeStr += " [SIMD 8-Way Unrolled Mode]"
	}

	fmt.Println("==========================================================================================")
	fmt.Printf("      Microfat Performance Benchmark Suite [%s - %d runs]   \n", modeStr, iterations)
	fmt.Println("==========================================================================================")

	configs := buildAndPackageConfigs(srcDir, benchDir, microfatStub, microfatCli)
	benchArgs := prepareBenchArgs(isUltra, isHeavy, isSIMD)

	runWarmups(configs, warmups, benchArgs)

	startupStats := measureStartup(configs, iterations)
	pureComputeStats, totalWallStats := measureCompute(configs, iterations, benchArgs, isUltra)

	printSummaryTables(configs, startupStats, pureComputeStats, totalWallStats, isUltra)
}

func runStartupBenchmarkSuite(srcDir, benchDir, microfatStub, microfatCli, csvPath string, iterations, warmups int) {
	fmt.Println("==========================================================================================")
	fmt.Printf("    Microfat Startup Overhead & Latency Benchmark Suite [%d iterations]   \n", iterations)
	fmt.Println("==========================================================================================")

	scenarios := buildStartupScenarios(srcDir, benchDir, microfatStub, microfatCli)

	fmt.Println("\n==> Step 2: Running warm-up cycles across execution modes...")
	runStartupWarmups(scenarios, warmups, benchDir)

	fmt.Printf("\n==> Step 3: Measuring Startup Overhead & Microsecond Telemetry [%d iterations]...\n", iterations)
	summaries, observations := measureStartupScenarios(scenarios, iterations, benchDir)

	printStartupSummaryTables(summaries)

	finalCSVPath := csvPath
	if finalCSVPath == "" {
		finalCSVPath = fmt.Sprintf("benchmarks-startup-%s.csv", time.Now().Format(csvTimestampLayout))
	}

	if err := exportStartupCSV(finalCSVPath, observations); err != nil {
		fmt.Printf("Warning: failed to export CSV to %s: %v\n", finalCSVPath, err)
	} else {
		fmt.Printf("\n==> ✔ Exported %d raw telemetry observations to CSV: %s\n\n", len(observations), finalCSVPath)
	}
}

func buildStartupScenarios(srcDir, benchDir, microfatStub, microfatCli string) []StartupScenario {
	fmt.Println("==> Step 1: Compiling and packaging multi-architecture test binaries...")

	v1Native := filepath.Join(benchDir, "01_v1_native")
	v2Native := filepath.Join(benchDir, "02_v2_temp")
	v3Native := filepath.Join(benchDir, "03_v3_native")
	v4Native := filepath.Join(benchDir, "00_v4_temp")

	fatZstd := filepath.Join(benchDir, "04_fat_zstd")
	fatLZ4 := filepath.Join(benchDir, "05_fat_lz4")
	fatNone := filepath.Join(benchDir, "06_fat_none")
	fatTrimmed := filepath.Join(benchDir, "07_fat_trimmed")
	optimizedV3 := filepath.Join(benchDir, "08_optimized_v3")

	goBin := resolveGoBinary()
	mustRun(srcDir, goBin, "build", "-ldflags=-s -w", "-o", v1Native, "main.go", "ENV:GOAMD64=v1")
	mustRun(srcDir, goBin, "build", "-ldflags=-s -w", "-o", v2Native, "main.go", "ENV:GOAMD64=v2")
	mustRun(srcDir, goBin, "build", "-ldflags=-s -w", "-o", v3Native, "main.go", "ENV:GOAMD64=v3")
	mustRun(srcDir, goBin, "build", "-ldflags=-s -w", "-o", v4Native, "main.go", "ENV:GOAMD64=v4")

	// 1. Zstd (Default)
	mustRun(benchDir, microfatCli, "pack",
		"--stub", microfatStub,
		"--name", "demo-app",
		"-v", "v1="+v1Native,
		"-v", "v2="+v2Native,
		"-v", "v3="+v3Native,
		"-v", "v4="+v4Native,
		"-o", fatZstd,
	)

	// 2. LZ4 Codec
	mustRun(benchDir, microfatCli, "pack",
		"--stub", microfatStub,
		"--name", "demo-app",
		"--compression", "lz4",
		"-v", "v1="+v1Native,
		"-v", "v2="+v2Native,
		"-v", "v3="+v3Native,
		"-v", "v4="+v4Native,
		"-o", fatLZ4,
	)

	// 3. None / Uncompressed Codec
	mustRun(benchDir, microfatCli, "pack",
		"--stub", microfatStub,
		"--name", "demo-app",
		"--compression", "none",
		"-v", "v1="+v1Native,
		"-v", "v2="+v2Native,
		"-v", "v3="+v3Native,
		"-v", "v4="+v4Native,
		"-o", fatNone,
	)

	// 4. Zstd with Shared Inter-Variant Dictionary
	fatDict := filepath.Join(benchDir, "07_fat_dict")
	mustRun(benchDir, microfatCli, "pack",
		"--stub", microfatStub,
		"--name", "demo-app",
		"--dict",
		"-v", "v1="+v1Native,
		"-v", "v2="+v2Native,
		"-v", "v3="+v3Native,
		"-v", "v4="+v4Native,
		"-o", fatDict,
	)

	// 5. Format v1 JSON Manifest Format
	fatV1 := filepath.Join(benchDir, "08_fat_v1")
	mustRun(benchDir, microfatCli, "pack",
		"--stub", microfatStub,
		"--name", "demo-app",
		"--format-version", "1",
		"-v", "v1="+v1Native,
		"-v", "v2="+v2Native,
		"-v", "v3="+v3Native,
		"-v", "v4="+v4Native,
		"-o", fatV1,
	)

	// 6. Minimal Stub Profile (if available)
	microfatStubMinimal := resolveBinary("microfat-stub-minimal")
	fatMinimal := filepath.Join(benchDir, "09_fat_minimal")
	hasMinimal := false
	if _, err := os.Stat(microfatStubMinimal); err == nil {
		mustRun(benchDir, microfatCli, "pack",
			"--stub", microfatStubMinimal,
			"--name", "demo-app",
			"-v", "v1="+v1Native,
			"-v", "v2="+v2Native,
			"-v", "v3="+v3Native,
			"-v", "v4="+v4Native,
			"-o", fatMinimal,
		)
		hasMinimal = true
	}

	// 7. Trimmed Fat
	mustRun(benchDir, microfatCli, "trim", fatZstd, "-o", fatTrimmed)

	// 8. Optimized raw ELF
	// #nosec G204 -- benchmark runner invokes locally built fat binary for optimize-to test
	cmdOpt := exec.Command(fatZstd, "--microfat:optimize-to="+optimizedV3)
	if err := cmdOpt.Run(); err != nil {
		panic(err)
	}

	// 9. Pre-warm dedicated cache directory for warm-cache scenario
	warmCacheDir := filepath.Join(benchDir, "warm_cache")
	if err := os.MkdirAll(warmCacheDir, dirPermission); err != nil {
		panic(err)
	}
	// #nosec G204 -- prewarm run on local test fat binary
	cmdPrewarm := exec.Command(fatZstd, "--microfat:prewarm")
	cmdPrewarm.Env = append(os.Environ(), "XDG_CACHE_HOME="+warmCacheDir)
	if err := cmdPrewarm.Run(); err != nil {
		panic(err)
	}

	scenarios := []StartupScenario{
		{Name: "1. Native v1 (Baseline SSE2)", Path: v1Native, ExecMode: execModeNative, Size: getFileSize(v1Native)},
		{Name: "2. Native v3 (AVX2/FMA)", Path: v3Native, ExecMode: execModeNative, Size: getFileSize(v3Native)},
		{
			Name:     "3. Universal FAT Format v2 (Cold memfd)",
			Path:     fatZstd,
			ExecMode: execModeMemfd,
			Env:      []string{envExecModeMemfd},
			Size:     getFileSize(fatZstd),
		},
		{
			Name:     "4. Universal FAT Format v1 JSON (Cold memfd)",
			Path:     fatV1,
			ExecMode: execModeMemfd,
			Env:      []string{envExecModeMemfd},
			Size:     getFileSize(fatV1),
		},
		{
			Name:     "5. Universal FAT Zstd-Dict (Cold memfd)",
			Path:     fatDict,
			ExecMode: execModeMemfd,
			Env:      []string{envExecModeMemfd},
			Size:     getFileSize(fatDict),
		},
		{
			Name:     "6. Universal FAT (Cold cache)",
			Path:     fatZstd,
			ExecMode: execModeColdCache,
			IsCold:   true,
			Env:      []string{"MICROFAT_EXEC_MODE=cache"},
			Size:     getFileSize(fatZstd),
		},
		{
			Name:      "7. Universal FAT (Warm cache)",
			Path:      fatZstd,
			ExecMode:  execModeWarmCache,
			WarmCache: warmCacheDir,
			Env:       []string{"MICROFAT_EXEC_MODE=cache", "XDG_CACHE_HOME=" + warmCacheDir},
			Size:      getFileSize(fatZstd),
		},
		{
			Name:     "8. Universal FAT LZ4 (Cold memfd)",
			Path:     fatLZ4,
			ExecMode: execModeMemfd,
			Env:      []string{envExecModeMemfd},
			Size:     getFileSize(fatLZ4),
		},
		{
			Name:     "9. Universal FAT None (Cold memfd)",
			Path:     fatNone,
			ExecMode: execModeMemfd,
			Env:      []string{envExecModeMemfd},
			Size:     getFileSize(fatNone),
		},
	}

	if hasMinimal {
		scenarios = append(scenarios, StartupScenario{
			Name:     "10. Universal FAT Minimal Stub (Cold memfd)",
			Path:     fatMinimal,
			ExecMode: execModeMemfd,
			Env:      []string{envExecModeMemfd},
			Size:     getFileSize(fatMinimal),
		})
	}

	scenarios = append(scenarios,
		StartupScenario{
			Name:     "11. Trimmed FAT (Cold memfd)",
			Path:     fatTrimmed,
			ExecMode: execModeMemfd,
			Env:      []string{envExecModeMemfd},
			Size:     getFileSize(fatTrimmed),
		},
		StartupScenario{Name: "12. Optimized v3 (from FAT)", Path: optimizedV3, ExecMode: execModeNative, Size: getFileSize(optimizedV3)},
	)

	for _, s := range scenarios {
		fmt.Printf("  • %-36s -> %6.2f MB (%d bytes)\n", s.Name, float64(s.Size)/bytesInMegabyte, s.Size)
	}

	return scenarios
}

func runStartupWarmups(scenarios []StartupScenario, warmups int, benchDir string) {
	for _, s := range scenarios {
		for i := 0; i < warmups; i++ {
			_ = runStartupIteration(s, benchDir, 0)
		}
	}
}

func runStartupIteration(s StartupScenario, benchDir string, iter int) StartupObservation {
	var cleanupDir string
	extraEnv := make([]string, len(s.Env))
	copy(extraEnv, s.Env)

	if s.IsCold {
		tDir, err := os.MkdirTemp(benchDir, "cold-cache-iter-*")
		if err == nil {
			cleanupDir = tDir
			extraEnv = append(extraEnv, "XDG_CACHE_HOME="+tDir)
		}
	}
	if cleanupDir != "" {
		defer func() { _ = os.RemoveAll(cleanupDir) }()
	}

	cmdEnv := append(os.Environ(), "MICROFAT_LOG=json")
	cmdEnv = append(cmdEnv, extraEnv...)

	// #nosec G204 -- startup benchmark execution of test binaries
	cmd := exec.Command(s.Path, "--startup-only")
	cmd.Env = cmdEnv

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	t0 := time.Now()
	err := cmd.Run()
	wallDuration := time.Since(t0)

	obs := StartupObservation{
		Timestamp:         time.Now().UTC().Format(timestampLayout),
		Iteration:         iter,
		ConfigName:        s.Name,
		ExecMode:          s.ExecMode,
		TotalWallDuration: wallDuration,
	}

	if err != nil {
		return obs
	}

	telemetry, parseErr := parseDispatchTelemetry(stderrBuf.Bytes())
	if parseErr == nil {
		obs.LauncherInternalUs = telemetry.TotalLauncherUs
		obs.DecompressionUs = telemetry.DecompressionDurationUs
		obs.SelectedVariant = telemetry.SelectedVariant
		obs.CgroupVersion = telemetry.CgroupVersion
		if telemetry.ExecMode != "" && s.ExecMode != execModeColdCache && s.ExecMode != execModeWarmCache {
			obs.ExecMode = telemetry.ExecMode
		}
	}

	return obs
}

func measureStartupScenarios(scenarios []StartupScenario, iterations int, benchDir string) ([]StartupSummary, []StartupObservation) {
	summaries := make([]StartupSummary, len(scenarios))
	allObservations := make([]StartupObservation, 0, len(scenarios)*iterations)

	for i, s := range scenarios {
		fmt.Printf("    Benchmarking %-36s...", s.Name)
		wallDurs := make([]time.Duration, iterations)
		launcherDurs := make([]time.Duration, iterations)
		decompressDurs := make([]time.Duration, iterations)

		for j := 0; j < iterations; j++ {
			obs := runStartupIteration(s, benchDir, j+1)
			allObservations = append(allObservations, obs)
			wallDurs[j] = obs.TotalWallDuration
			launcherDurs[j] = time.Duration(obs.LauncherInternalUs) * time.Microsecond
			decompressDurs[j] = time.Duration(obs.DecompressionUs) * time.Microsecond
		}

		summaries[i] = StartupSummary{
			Scenario:           s,
			WallStats:          calculateStats(wallDurs),
			LauncherStats:      calculateStats(launcherDurs),
			DecompressionStats: calculateStats(decompressDurs),
		}

		meanWallMs := float64(summaries[i].WallStats.Mean.Microseconds()) / msPerMicro
		meanStubUs := float64(summaries[i].LauncherStats.Mean.Microseconds())
		if s.ExecMode == execModeNative {
			fmt.Printf(" done (wall: %.2f ms)\n", meanWallMs)
		} else {
			fmt.Printf(" done (wall: %.2f ms | stub: %.0f µs)\n", meanWallMs, meanStubUs)
		}
	}

	return summaries, allObservations
}

func parseDispatchTelemetry(stderr []byte) (format.DispatchTelemetry, error) {
	scanner := bufio.NewScanner(bytes.NewReader(stderr))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		idx := bytes.IndexByte(line, '{')
		if idx == -1 {
			continue
		}
		jsonBytes := line[idx:]
		var dt format.DispatchTelemetry
		if err := json.Unmarshal(jsonBytes, &dt); err == nil && dt.Event == format.EventDispatch {
			return dt, nil
		}
	}
	return format.DispatchTelemetry{}, fmt.Errorf("no dispatch telemetry JSON found in stderr")
}

func exportStartupCSV(csvPath string, observations []StartupObservation) error {
	f, err := os.Create(filepath.Clean(csvPath))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	w := csv.NewWriter(f)
	defer w.Flush()

	header := []string{
		"timestamp",
		"iteration",
		"config_name",
		"exec_mode",
		"selected_variant",
		"total_wall_us",
		"total_wall_ms",
		"launcher_internal_us",
		"decompression_us",
		"cgroup_version",
	}
	if err := w.Write(header); err != nil {
		return err
	}

	for _, obs := range observations {
		wallUs := obs.TotalWallDuration.Microseconds()
		wallMs := float64(wallUs) / msPerMicro
		row := []string{
			obs.Timestamp,
			strconv.Itoa(obs.Iteration),
			obs.ConfigName,
			obs.ExecMode,
			obs.SelectedVariant,
			strconv.FormatInt(wallUs, 10),
			strconv.FormatFloat(wallMs, 'f', 3, 64),
			strconv.FormatInt(obs.LauncherInternalUs, 10),
			strconv.FormatInt(obs.DecompressionUs, 10),
			strconv.Itoa(obs.CgroupVersion),
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}

	return w.Error()
}

func printStartupSummaryTables(summaries []StartupSummary) {
	fmt.Println("\n==========================================================================================")
	fmt.Println("                  STARTUP LATENCY & STUB TELEMETRY RESULTS                                ")
	fmt.Println("==========================================================================================")

	fmt.Println("\n### Table 1: Binary Footprint on Disk")
	fmt.Println("| Binary Configuration | Disk Size (Bytes) | Disk Size (MB) | % of Universal FAT | Space Savings |")
	fmt.Println("| :--- | :--- | :--- | :--- | :--- |")
	fatSize := float64(summaries[2].Scenario.Size)
	for _, s := range summaries {
		ratio := (float64(s.Scenario.Size) / fatSize) * percentMultiplier
		diff := percentMultiplier - ratio
		diffStr := fmt.Sprintf("-%.1f%%", diff)
		if s.Scenario.Name == summaries[2].Scenario.Name {
			diffStr = "baseline"
		}
		fmt.Printf("| **%s** | `%d B` | `%.2f MB` | `%.1f%%` | `%s` |\n",
			s.Scenario.Name, s.Scenario.Size, float64(s.Scenario.Size)/bytesInMegabyte, ratio, diffStr)
	}

	fmt.Println("\n### Table 2: Process Cold-Start Latency & Stub Overhead Breakdown (`--startup-only`)")
	fmt.Println("| Configuration / Execution Mode | Mean Wall (ms) | StdDev (ms) | Median p50 (ms) | p95 (ms) | " +
		"p99 (ms) | Stub Overhead (µs) | Decompress (µs) | Overhead vs Native v3 |")
	fmt.Println("| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |")

	nativeMean := float64(summaries[1].WallStats.Mean.Microseconds()) / msPerMicro
	for i, s := range summaries {
		meanMs := float64(s.WallStats.Mean.Microseconds()) / msPerMicro
		stdMs := float64(s.WallStats.StdDev.Microseconds()) / msPerMicro
		medMs := float64(s.WallStats.Median.Microseconds()) / msPerMicro
		p95Ms := float64(s.WallStats.P95.Microseconds()) / msPerMicro
		p99Ms := float64(s.WallStats.P99.Microseconds()) / msPerMicro
		stubUs := float64(s.LauncherStats.Mean.Microseconds())
		decUs := float64(s.DecompressionStats.Mean.Microseconds())

		diff := meanMs - nativeMean
		diffStr := fmt.Sprintf("%+.2f ms", diff)
		if i == 1 {
			diffStr = "baseline (0.00 ms)"
		}

		stubStr := fmt.Sprintf("%.0f µs", stubUs)
		decStr := fmt.Sprintf("%.0f µs", decUs)
		if s.Scenario.ExecMode == execModeNative {
			stubStr = "n/a (direct)"
			decStr = "n/a (direct)"
		}

		fmt.Printf("| **%s** | `%.2f ms` | `±%.2f ms` | `%.2f ms` | `%.2f ms` | `%.2f ms` | `%s` | `%s` | `%s` |\n",
			s.Scenario.Name, meanMs, stdMs, medMs, p95Ms, p99Ms, stubStr, decStr, diffStr)
	}
	fmt.Println("==========================================================================================")
}

func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func resolveBinary(name string) string {
	if root := findRepoRoot(); root != "" {
		repoBin := filepath.Join(root, "bin", name)
		if st, err := os.Stat(repoBin); err == nil && !st.IsDir() {
			return repoBin
		}
	}
	p, _ := exec.LookPath(name)
	if p == "" {
		p = filepath.Join(os.Getenv("HOME"), ".local/bin", name)
	}
	return p
}

func resolveGoBinary() string {
	if root := findRepoRoot(); root != "" {
		repoGo := filepath.Join(root, ".go", "bin", "go")
		if st, err := os.Stat(repoGo); err == nil && !st.IsDir() {
			return repoGo
		}
	}
	if p, err := exec.LookPath("go"); err == nil {
		return p
	}
	return "go"
}

func buildAndPackageConfigs(srcDir, benchDir, microfatStub, microfatCli string) []Config {
	fmt.Println("==> Step 1: Compiling and packaging 5 test configurations...")

	v1Native := filepath.Join(benchDir, "01_v1_native")
	v2Native := filepath.Join(benchDir, "02_v2_temp")
	v3Native := filepath.Join(benchDir, "03_v3_native")
	v4Native := filepath.Join(benchDir, "00_v4_temp")
	fatUniversal := filepath.Join(benchDir, "04_fat_universal")
	fatTrimmed := filepath.Join(benchDir, "05_fat_trimmed")
	optimizedV3 := filepath.Join(benchDir, "06_optimized_v3")

	goBin := resolveGoBinary()
	mustRun(srcDir, goBin, "build", "-ldflags=-s -w", "-o", v1Native, "main.go", "ENV:GOAMD64=v1")
	mustRun(srcDir, goBin, "build", "-ldflags=-s -w", "-o", v2Native, "main.go", "ENV:GOAMD64=v2")
	mustRun(srcDir, goBin, "build", "-ldflags=-s -w", "-o", v3Native, "main.go", "ENV:GOAMD64=v3")
	mustRun(srcDir, goBin, "build", "-ldflags=-s -w", "-o", v4Native, "main.go", "ENV:GOAMD64=v4")

	mustRun(benchDir, microfatCli, "pack",
		"--stub", microfatStub,
		"--name", "demo-app",
		"-v", "v1="+v1Native,
		"-v", "v2="+v2Native,
		"-v", "v3="+v3Native,
		"-v", "v4="+v4Native,
		"-o", fatUniversal,
	)

	mustRun(benchDir, microfatCli, "trim", fatUniversal, "-o", fatTrimmed)
	// #nosec G204 -- benchmark runner invokes locally built fat binary for optimize-to test
	cmdOpt := exec.Command(fatUniversal, "--microfat:optimize-to="+optimizedV3)
	if err := cmdOpt.Run(); err != nil {
		panic(err)
	}

	configs := []Config{
		{Name: "1. Native v1 (Baseline SSE2)", Path: v1Native, Size: getFileSize(v1Native)},
		{Name: "2. Native v3 (AVX2/FMA)", Path: v3Native, Size: getFileSize(v3Native)},
		{Name: "3. Universal FAT (v1–v4)", Path: fatUniversal, Size: getFileSize(fatUniversal)},
		{Name: "4. Trimmed FAT (v3 + Stub)", Path: fatTrimmed, Size: getFileSize(fatTrimmed)},
		{Name: "5. Optimized v3 (from FAT)", Path: optimizedV3, Size: getFileSize(optimizedV3)},
	}

	for _, c := range configs {
		fmt.Printf("  • %-30s -> %6.2f MB (%d bytes)\n", c.Name, float64(c.Size)/bytesInMegabyte, c.Size)
	}

	return configs
}

func prepareBenchArgs(isUltra, isHeavy, isSIMD bool) []string {
	benchArgs := []string{"--json", "all"}
	if isUltra {
		benchArgs = append(benchArgs, "--ultra")
	} else if isHeavy {
		benchArgs = append(benchArgs, "--heavy")
	}
	if isSIMD {
		benchArgs = append(benchArgs, "--simd")
	}
	return benchArgs
}

func runWarmups(configs []Config, warmups int, benchArgs []string) {
	fmt.Println("\n==> Step 2: Running warm-up cycles...")
	for _, c := range configs {
		for i := 0; i < warmups; i++ {
			// #nosec G204 -- warm-up run of benchmark test binary
			_ = exec.Command(c.Path, "--help").Run()
			// #nosec G204 -- warm-up run of benchmark test binary
			_ = exec.Command(c.Path, benchArgs...).Run()
		}
	}
}

func measureStartup(configs []Config, iterations int) []Stats {
	fmt.Printf("\n==> Step 3: Measuring Startup Overhead (--help) [%d iterations]...\n", iterations)
	startupStats := make([]Stats, len(configs))
	for i, c := range configs {
		fmt.Printf("    Benchmarking %s...", c.Name)
		durations := make([]time.Duration, iterations)
		for j := 0; j < iterations; j++ {
			t0 := time.Now()
			// #nosec G204 -- benchmark execution of test binary
			cmd := exec.Command(c.Path, "--help")
			if err := cmd.Run(); err != nil {
				panic(err)
			}
			durations[j] = time.Since(t0)
		}
		startupStats[i] = calculateStats(durations)
		fmt.Printf(" done (mean: %.2f ms)\n", float64(startupStats[i].Mean.Microseconds())/msPerMicro)
	}
	return startupStats
}

func measureCompute(configs []Config, iterations int, benchArgs []string, isUltra bool) ([]Stats, []Stats) {
	fmt.Printf("\n==> Step 4: Measuring Pure In-Process Compute Time & Total Latency [%d iterations]...\n", iterations)
	pureComputeStats := make([]Stats, len(configs))
	totalWallStats := make([]Stats, len(configs))

	for i, c := range configs {
		fmt.Printf("    Benchmarking %s...", c.Name)
		pureDurs := make([]time.Duration, iterations)
		wallDurs := make([]time.Duration, iterations)

		for j := 0; j < iterations; j++ {
			t0 := time.Now()
			// #nosec G204 -- benchmark execution of test binary
			cmd := exec.Command(c.Path, benchArgs...)
			var outBuf bytes.Buffer
			cmd.Stdout = &outBuf
			if err := cmd.Run(); err != nil {
				panic(err)
			}
			wallDurs[j] = time.Since(t0)

			var report DemoReport
			if err := json.Unmarshal(outBuf.Bytes(), &report); err != nil {
				panic(err)
			}
			pureDurs[j] = time.Duration(report.TotalCompute * float64(time.Millisecond))
		}

		pureComputeStats[i] = calculateStats(pureDurs)
		totalWallStats[i] = calculateStats(wallDurs)

		pureMs := float64(pureComputeStats[i].Mean.Microseconds()) / msPerMicro
		wallMs := float64(totalWallStats[i].Mean.Microseconds()) / msPerMicro

		if isUltra {
			fmt.Printf(" done (pure compute: %.3f s | wall: %.3f s)\n", pureMs/secPerMilli, wallMs/secPerMilli)
		} else {
			fmt.Printf(" done (pure compute: %.2f ms | wall: %.2f ms)\n", pureMs, wallMs)
		}
	}
	return pureComputeStats, totalWallStats
}

func printSummaryTables(configs []Config, startupStats, pureComputeStats, totalWallStats []Stats, isUltra bool) {
	fmt.Println("\n==========================================================================================")
	fmt.Println("                        BENCHMARK RESULTS & COMPARISON                                    ")
	fmt.Println("==========================================================================================")

	printDiskTable(configs)
	printStartupTable(configs, startupStats)
	printComputeTable(configs, pureComputeStats, isUltra)
	printWallTable(configs, totalWallStats, isUltra)
}

func printDiskTable(configs []Config) {
	fmt.Println("\n### Table 1: Binary Footprint on Disk")
	fmt.Println("| Binary Configuration | Disk Size (Bytes) | Disk Size (MB) | % of Universal FAT | Space Savings |")
	fmt.Println("| :--- | :--- | :--- | :--- | :--- |")
	fatSize := float64(configs[2].Size)
	for _, c := range configs {
		ratio := (float64(c.Size) / fatSize) * percentMultiplier
		diff := percentMultiplier - ratio
		diffStr := fmt.Sprintf("-%.1f%%", diff)
		if c.Name == configs[2].Name {
			diffStr = "baseline"
		}
		fmt.Printf("| **%s** | `%d B` | `%.2f MB` | `%.1f%%` | `%s` |\n",
			c.Name, c.Size, float64(c.Size)/bytesInMegabyte, ratio, diffStr)
	}
}

func printStartupTable(configs []Config, startupStats []Stats) {
	fmt.Println("\n### Table 2: Process Startup Overhead (`--help`)")
	fmt.Println("| Binary Configuration | Mean Startup (ms) | StdDev (ms) | Median p50 (ms) | p95 (ms) | Startup Overhead vs Native v3 |")
	fmt.Println("| :--- | :--- | :--- | :--- | :--- | :--- |")
	nativeStartup := float64(startupStats[1].Mean.Microseconds()) / msPerMicro
	for i, c := range configs {
		meanMs := float64(startupStats[i].Mean.Microseconds()) / msPerMicro
		stdMs := float64(startupStats[i].StdDev.Microseconds()) / msPerMicro
		medMs := float64(startupStats[i].Median.Microseconds()) / msPerMicro
		p95Ms := float64(startupStats[i].P95.Microseconds()) / msPerMicro
		diff := meanMs - nativeStartup
		diffStr := fmt.Sprintf("%+.2f ms", diff)
		if i == 1 {
			diffStr = "baseline (0.00 ms)"
		}
		fmt.Printf("| **%s** | `%.2f ms` | `±%.2f ms` | `%.2f ms` | `%.2f ms` | `%s` |\n",
			c.Name, meanMs, stdMs, medMs, p95Ms, diffStr)
	}
}

func printComputeTable(configs []Config, pureComputeStats []Stats, isUltra bool) {
	fmt.Println("\n### Table 3: Pure In-Process Compute Time (Isolated Hardware Speed)")
	if isUltra {
		fmt.Println("| Binary Configuration | Mean Compute (s) | StdDev (s) | Median p50 (s) | Delta vs Native v1 (s) | Compute Speedup |")
		fmt.Println("| :--- | :--- | :--- | :--- | :--- | :--- |")
		v1Sec := float64(pureComputeStats[0].Mean.Microseconds()) / (msPerMicro * secPerMilli)
		for i, c := range configs {
			meanSec := float64(pureComputeStats[i].Mean.Microseconds()) / (msPerMicro * secPerMilli)
			stdSec := float64(pureComputeStats[i].StdDev.Microseconds()) / (msPerMicro * secPerMilli)
			medSec := float64(pureComputeStats[i].Median.Microseconds()) / (msPerMicro * secPerMilli)
			deltaSec := meanSec - v1Sec
			speedup := v1Sec / meanSec
			deltaStr := fmt.Sprintf("%+.3f s", deltaSec)
			if i == 0 {
				deltaStr = "baseline (0.000 s)"
			}
			fmt.Printf("| **%s** | `%.3f s` | `±%.3f s` | `%.3f s` | `%s` | `%.2fx speedup` |\n",
				c.Name, meanSec, stdSec, medSec, deltaStr, speedup)
		}
	} else {
		fmt.Println("| Binary Configuration | Mean Compute (ms) | StdDev (ms) | Median p50 (ms) | p95 (ms) | Compute Speedup vs Native v1 |")
		fmt.Println("| :--- | :--- | :--- | :--- | :--- | :--- |")
		v1Compute := float64(pureComputeStats[0].Mean.Microseconds()) / msPerMicro
		for i, c := range configs {
			meanMs := float64(pureComputeStats[i].Mean.Microseconds()) / msPerMicro
			stdMs := float64(pureComputeStats[i].StdDev.Microseconds()) / msPerMicro
			medMs := float64(pureComputeStats[i].Median.Microseconds()) / msPerMicro
			p95Ms := float64(pureComputeStats[i].P95.Microseconds()) / msPerMicro
			speedup := (v1Compute / meanMs)
			fmt.Printf("| **%s** | `%.2f ms` | `±%.2f ms` | `%.2f ms` | `%.2f ms` | `%.2fx speedup` |\n",
				c.Name, meanMs, stdMs, medMs, p95Ms, speedup)
		}
	}
}

func printWallTable(configs []Config, totalWallStats []Stats, isUltra bool) {
	fmt.Println("\n### Table 4: Total End-to-End Latency (Startup + Workload Combined)")
	if isUltra {
		fmt.Println("| Binary Configuration | Total Mean (s) | StdDev (s) | Median p50 (s) | Delta vs Native v1 (s) | Total Speedup |")
		fmt.Println("| :--- | :--- | :--- | :--- | :--- | :--- |")
		v1TotalSec := float64(totalWallStats[0].Mean.Microseconds()) / (msPerMicro * secPerMilli)
		for i, c := range configs {
			meanSec := float64(totalWallStats[i].Mean.Microseconds()) / (msPerMicro * secPerMilli)
			stdSec := float64(totalWallStats[i].StdDev.Microseconds()) / (msPerMicro * secPerMilli)
			medSec := float64(totalWallStats[i].Median.Microseconds()) / (msPerMicro * secPerMilli)
			deltaSec := meanSec - v1TotalSec
			speedup := v1TotalSec / meanSec
			deltaStr := fmt.Sprintf("%+.3f s", deltaSec)
			if i == 0 {
				deltaStr = "baseline (0.000 s)"
			}
			fmt.Printf("| **%s** | `%.3f s` | `±%.3f s` | `%.3f s` | `%s` | `%.2fx` |\n",
				c.Name, meanSec, stdSec, medSec, deltaStr, speedup)
		}
	} else {
		fmt.Println("| Binary Configuration | Total Mean (ms) | StdDev (ms) | Median p50 (ms) | p95 (ms) | Total Speed vs Native v1 |")
		fmt.Println("| :--- | :--- | :--- | :--- | :--- | :--- |")
		v1Total := float64(totalWallStats[0].Mean.Microseconds()) / msPerMicro
		for i, c := range configs {
			meanMs := float64(totalWallStats[i].Mean.Microseconds()) / msPerMicro
			stdMs := float64(totalWallStats[i].StdDev.Microseconds()) / msPerMicro
			medMs := float64(totalWallStats[i].Median.Microseconds()) / msPerMicro
			p95Ms := float64(totalWallStats[i].P95.Microseconds()) / msPerMicro
			speedup := (v1Total / meanMs)
			fmt.Printf("| **%s** | `%.2f ms` | `±%.2f ms` | `%.2f ms` | `%.2f ms` | `%.2fx` |\n",
				c.Name, meanMs, stdMs, medMs, p95Ms, speedup)
		}
	}
}

func mustRun(dir string, name string, args ...string) {
	var env []string
	cleanArgs := make([]string, 0, len(args))
	for _, arg := range args {
		if len(arg) > envPrefixLen && arg[:envPrefixLen] == "ENV:" {
			env = append(env, arg[envPrefixLen:])
		} else {
			cleanArgs = append(cleanArgs, arg)
		}
	}

	// #nosec G204,G702 -- benchmark builder invokes compilers with sanitized args
	cmd := exec.Command(filepath.Clean(name), cleanArgs...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("command %s %v failed: %v\nOutput: %s", name, cleanArgs, err, out))
	}
}

func getFileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		panic(err)
	}
	return st.Size()
}

func calculateStats(durations []time.Duration) Stats {
	if len(durations) == 0 {
		return Stats{}
	}

	sort.Slice(durations, func(i, j int) bool {
		return durations[i] < durations[j]
	})

	var sum int64
	for _, d := range durations {
		sum += int64(d)
	}
	meanNs := float64(sum) / float64(len(durations))

	var varianceSum float64
	for _, d := range durations {
		diff := float64(d) - meanNs
		varianceSum += diff * diff
	}
	stdDevNs := math.Sqrt(varianceSum / float64(len(durations)))

	median := durations[int(float64(len(durations))*medianMultiplier)]
	p95Index := int(float64(len(durations)) * p95Multiplier)
	if p95Index >= len(durations) {
		p95Index = len(durations) - 1
	}
	p99Index := int(float64(len(durations)) * p99Multiplier)
	if p99Index >= len(durations) {
		p99Index = len(durations) - 1
	}

	return Stats{
		Mean:   time.Duration(meanNs),
		StdDev: time.Duration(stdDevNs),
		Median: median,
		P95:    durations[p95Index],
		P99:    durations[p99Index],
		Min:    durations[0],
		Max:    durations[len(durations)-1],
	}
}
