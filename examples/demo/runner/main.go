// Package main implements the statistical multi-workload benchmark runner.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

const (
	bytesInMegabyte   = 1024.0 * 1024.0
	msPerMicro        = 1000.0
	secPerMilli       = 1000.0
	percentMultiplier = 100.0
	p95Multiplier     = 0.95
	stdWarmup         = 5
	ultraWarmup       = 1
	stdIterations     = 50
	heavyIterations   = 20
	ultraIterations   = 3
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
	Min    time.Duration
	Max    time.Duration
}

type DemoReport struct {
	Version      string  `json:"version"`
	TotalCompute float64 `json:"total_compute_ms"`
}

func main() {
	ultraPtr := flag.Bool("ultra", false, "Run ULTRA sustained heavy compute benchmark (10s of seconds)")
	heavyPtr := flag.Bool("heavy", false, "Run heavy compute-intensive benchmark (~500ms workload)")
	itersPtr := flag.Int("n", 0, "Number of benchmark iterations")
	flag.Parse()

	isUltra := *ultraPtr
	isHeavy := *heavyPtr && !isUltra

	iterations := stdIterations
	warmups := stdWarmup

	if isUltra {
		iterations = ultraIterations
		warmups = ultraWarmup
	} else if isHeavy {
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

	modeStr := "Standard Workload (~110ms)"
	if isUltra {
		modeStr = "ULTRA Sustained Heavy Workload (10s of seconds per run)"
	} else if isHeavy {
		modeStr = "HEAVY Sustained Compute (~500ms per run)"
	}

	fmt.Println("==========================================================================================")
	fmt.Printf("      Microfat Performance Benchmark Suite [%s - %d runs]   \n", modeStr, iterations)
	fmt.Println("==========================================================================================")

	configs := buildAndPackageConfigs(srcDir, benchDir, microfatStub, microfatCli)
	benchArgs := prepareBenchArgs(isUltra, isHeavy)

	runWarmups(configs, warmups, benchArgs)

	startupStats := measureStartup(configs, iterations)
	pureComputeStats, totalWallStats := measureCompute(configs, iterations, benchArgs, isUltra)

	printSummaryTables(configs, startupStats, pureComputeStats, totalWallStats, isUltra)
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

func prepareBenchArgs(isUltra, isHeavy bool) []string {
	benchArgs := []string{"--json", "all"}
	if isUltra {
		benchArgs = append(benchArgs, "--ultra")
	} else if isHeavy {
		benchArgs = append(benchArgs, "--heavy")
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
		if len(arg) > 4 && arg[:4] == "ENV:" {
			env = append(env, arg[4:])
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

	median := durations[len(durations)/2]
	p95Index := int(float64(len(durations)) * p95Multiplier)
	if p95Index >= len(durations) {
		p95Index = len(durations) - 1
	}

	return Stats{
		Mean:   time.Duration(meanNs),
		StdDev: time.Duration(stdDevNs),
		Median: median,
		P95:    durations[p95Index],
		Min:    durations[0],
		Max:    durations[len(durations)-1],
	}
}
