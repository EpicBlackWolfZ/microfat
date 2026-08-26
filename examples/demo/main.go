// Package main implements the microfat multi-workload demonstration and performance benchmark application.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
	"math/rand"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/spf13/cobra"
)

// WorkloadMetrics stores timing and throughput data for individual workload runs.
type WorkloadMetrics struct {
	WorkloadName    string  `json:"workload_name"`
	ComputeDuration string  `json:"compute_duration"`
	ComputeMs       float64 `json:"compute_ms"`
	Operations      int64   `json:"operations"`
	OpsPerSecond    float64 `json:"ops_per_second"`
	Detail          string  `json:"detail,omitempty"`
}

// BenchmarkReport captures full inside-process execution statistics.
type BenchmarkReport struct {
	Version      string            `json:"version"`
	GoVersion    string            `json:"go_version"`
	GOMAXPROCS   int               `json:"gomaxprocs"`
	NumCPU       int               `json:"num_cpu"`
	GOMEMLIMIT   string            `json:"gomemlimit,omitempty"`
	Metrics      []WorkloadMetrics `json:"metrics"`
	TotalCompute float64           `json:"total_compute_ms"`
}

type WorkloadLevel int

const (
	LevelStandard WorkloadLevel = iota
	LevelHeavy
	LevelUltra
)

const (
	defaultAppVer   = "1.0.0"
	seedValue       = 42
	scaleMultiplier = 100.0
	cryptoBlockSize = 65536
	byteModulo      = 256
	msPerMicro      = 1000.0
	jsonTotalFactor = 4
	opsDivisor      = 1000
	bitShiftAmount  = 7
	bitInitialMask  = 0xAAAAAAAAAAAAAAAA
)

var (
	flagJSON  bool
	flagHeavy bool
	flagUltra bool
)

func getWorkloadLevel() WorkloadLevel {
	if flagUltra {
		return LevelUltra
	}
	if flagHeavy {
		return LevelHeavy
	}
	return LevelStandard
}

func main() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Microfat Demonstration Workload and Performance Benchmark",
		Long: `A high-performance demonstration application testing SIMD vector math,
bulk memory/JSON processing, and concurrent worker scaling across Go microarchitecture levels.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAllWorkloads(flagJSON, getWorkloadLevel())
		},
	}

	cmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "Output metrics in JSON format")
	cmd.PersistentFlags().BoolVar(&flagHeavy, "heavy", false, "Run heavy compute workload (~500ms)")
	cmd.PersistentFlags().BoolVar(&flagUltra, "ultra", false, "Run ultra-heavy sustained compute workload (10s of seconds)")

	cmd.AddCommand(newMathCmd())
	cmd.AddCommand(newJsonCmd())
	cmd.AddCommand(newConcurrentCmd())
	cmd.AddCommand(newAllCmd())

	return cmd
}

func newAllCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "all",
		Short: "Run all 3 benchmark workloads in sequence",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAllWorkloads(flagJSON, getWorkloadLevel())
		},
	}
}

func newMathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "math",
		Short: "Execute SIMD Vector Math & Cryptographic Pipeline (Phase A)",
		RunE: func(cmd *cobra.Command, args []string) error {
			m := runSIMDMathWorkload(getWorkloadLevel())
			return outputMetrics([]WorkloadMetrics{m}, flagJSON)
		},
	}
}

func newJsonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "json-mem",
		Short: "Execute Bulk Memory & JSON Transformation Workload (Phase B)",
		RunE: func(cmd *cobra.Command, args []string) error {
			m := runJSONMemoryWorkload(getWorkloadLevel())
			return outputMetrics([]WorkloadMetrics{m}, flagJSON)
		},
	}
}

func newConcurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "concurrent",
		Short: "Execute High-Concurrency Worker Scaling Workload (Phase C)",
		RunE: func(cmd *cobra.Command, args []string) error {
			m := runConcurrentWorkload(getWorkloadLevel())
			return outputMetrics([]WorkloadMetrics{m}, flagJSON)
		},
	}
}

func runAllWorkloads(jsonOut bool, level WorkloadLevel) error {
	m1 := runSIMDMathWorkload(level)
	m2 := runJSONMemoryWorkload(level)
	m3 := runConcurrentWorkload(level)

	return outputMetrics([]WorkloadMetrics{m1, m2, m3}, jsonOut)
}

func outputMetrics(metrics []WorkloadMetrics, jsonOut bool) error {
	var totalMs float64
	for _, m := range metrics {
		totalMs += m.ComputeMs
	}

	report := BenchmarkReport{
		Version:      defaultAppVer,
		GoVersion:    runtime.Version(),
		GOMAXPROCS:   runtime.GOMAXPROCS(0),
		NumCPU:       runtime.NumCPU(),
		GOMEMLIMIT:   os.Getenv("GOMEMLIMIT"),
		Metrics:      metrics,
		TotalCompute: totalMs,
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	fmt.Println("==================================================================")
	fmt.Println("             Microfat Demo Benchmark Results                      ")
	fmt.Println("==================================================================")
	fmt.Printf("Runtime Config: GOMAXPROCS=%d | NumCPU=%d | GOMEMLIMIT=%s\n\n",
		report.GOMAXPROCS, report.NumCPU, fallbackStr(report.GOMEMLIMIT, "(unset)"))

	for _, m := range metrics {
		fmt.Printf("  • %-38s -> %9.2f ms (%10.0f ops/sec) [%s]\n",
			m.WorkloadName, m.ComputeMs, m.OpsPerSecond, m.Detail)
	}

	fmt.Println("------------------------------------------------------------------")
	fmt.Printf("  Total Pure In-Process Compute Time:   %9.2f ms (%.2f s)\n", totalMs, totalMs/msPerMicro)
	fmt.Println("==================================================================")
	return nil
}

// -----------------------------------------------------------------------------
// Workload 1: SIMD Vector Math & Cryptographic Pipeline
// -----------------------------------------------------------------------------
func runSIMDMathWorkload(level WorkloadLevel) WorkloadMetrics {
	start := time.Now()

	dim := 180
	cryptoIters := 200
	bitIters := 200000

	switch level {
	case LevelUltra:
		dim = 1100
		cryptoIters = 20000
		bitIters = 30000000
	case LevelHeavy:
		dim = 300
		cryptoIters = 1000
		bitIters = 2000000
	case LevelStandard:
		// default values
	}

	// 1. Contiguous 1D Flat Slices for Vector Matrix Multiplication (AVX2/FMA)
	totalElements := dim * dim
	a := make([]float64, totalElements)
	b := make([]float64, totalElements)
	c := make([]float64, totalElements)

	// #nosec G404 -- deterministic pseudo-random seed for reproducible benchmark runs
	r := rand.New(rand.NewSource(seedValue))
	for i := 0; i < totalElements; i++ {
		a[i] = r.Float64() * scaleMultiplier
		b[i] = r.Float64() * scaleMultiplier
	}

	var totalOps int64
	for i := 0; i < dim; i++ {
		iOff := i * dim
		for k := 0; k < dim; k++ {
			aik := a[iOff+k]
			kOff := k * dim
			for j := 0; j < dim; j++ {
				c[iOff+j] += math.FMA(aik, b[kOff+j], math.Sin(float64(i+j)))
				totalOps++
			}
		}
	}

	// 2. Hardware Bit Manipulation (BMI1 / BMI2 via math/bits)
	var bitAcc uint64 = bitInitialMask
	for i := 0; i < bitIters; i++ {
		bitAcc = bits.RotateLeft64(bitAcc, bitShiftAmount) ^ uint64(i)
		totalOps += int64(bits.OnesCount64(bitAcc))
		totalOps += int64(bits.LeadingZeros64(bitAcc))
		totalOps += int64(bits.TrailingZeros64(bitAcc))
	}
	_ = bitAcc

	// 3. Cryptographic Multi-Block Hashing (SHA-256)
	hasher := sha256.New()
	block := make([]byte, cryptoBlockSize)
	for i := range block {
		block[i] = byte(i % byteModulo)
	}

	for i := 0; i < cryptoIters; i++ {
		hasher.Write(block)
		_ = hasher.Sum(nil)
		hasher.Reset()
		totalOps += int64(len(block))
	}

	elapsed := time.Since(start)
	computeMs := float64(elapsed.Microseconds()) / msPerMicro
	opsPerSec := float64(totalOps) / elapsed.Seconds()

	return WorkloadMetrics{
		WorkloadName:    "Phase A: SIMD Math & Cryptography",
		ComputeDuration: elapsed.String(),
		ComputeMs:       computeMs,
		Operations:      totalOps,
		OpsPerSecond:    opsPerSec,
		Detail:          fmt.Sprintf("Flat %dx%d FMA + BMI2 + SHA256", dim, dim),
	}
}

// -----------------------------------------------------------------------------
// Workload 2: Bulk Memory & JSON / Zstd Processing
// -----------------------------------------------------------------------------
type Record struct {
	ID        int       `json:"id"`
	UUID      string    `json:"uuid"`
	Timestamp time.Time `json:"timestamp"`
	Payload   string    `json:"payload"`
	Scores    []float64 `json:"scores"`
}

func runJSONMemoryWorkload(level WorkloadLevel) WorkloadMetrics {
	start := time.Now()

	batchSize := 15000
	switch level {
	case LevelUltra:
		batchSize = 250000
	case LevelHeavy:
		batchSize = 50000
	case LevelStandard:
		// default
	}

	records := make([]Record, batchSize)
	now := time.Now()
	for i := 0; i < batchSize; i++ {
		records[i] = Record{
			ID:        i,
			UUID:      fmt.Sprintf("record-%08d-token", i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Payload:   "High-throughput microfat in-memory serialization and zstd compression test payload block",
			Scores:    []float64{float64(i) * 1.1, float64(i) * 2.2, float64(i) * 3.3},
		}
	}

	// 1. Encode JSON
	jsonData, err := json.Marshal(records)
	if err != nil {
		panic(err)
	}

	// 2. Compress with Zstandard (exercising hardware bit-decoding & memory stream)
	var zstdBuf bytes.Buffer
	enc, err := zstd.NewWriter(&zstdBuf)
	if err != nil {
		panic(err)
	}
	if _, err := enc.Write(jsonData); err != nil {
		panic(err)
	}
	if err := enc.Close(); err != nil {
		panic(err)
	}

	// 3. Decompress Zstandard
	dec, err := zstd.NewReader(&zstdBuf)
	if err != nil {
		panic(err)
	}
	var decompressedBuf bytes.Buffer
	if _, err := decompressedBuf.ReadFrom(dec); err != nil {
		panic(err)
	}
	dec.Close()

	// 4. Decode JSON back
	var decoded []Record
	if err := json.Unmarshal(decompressedBuf.Bytes(), &decoded); err != nil {
		panic(err)
	}

	elapsed := time.Since(start)
	computeMs := float64(elapsed.Microseconds()) / msPerMicro
	totalOps := int64(batchSize * jsonTotalFactor)
	opsPerSec := float64(totalOps) / elapsed.Seconds()

	return WorkloadMetrics{
		WorkloadName:    "Phase B: Memory, JSON & Zstd",
		ComputeDuration: elapsed.String(),
		ComputeMs:       computeMs,
		Operations:      totalOps,
		OpsPerSecond:    opsPerSec,
		Detail:          fmt.Sprintf("%d records JSON + Zstd encode/decode", batchSize),
	}
}

// -----------------------------------------------------------------------------
// Workload 3: High-Concurrency Worker Scaling
// -----------------------------------------------------------------------------
func runConcurrentWorkload(level WorkloadLevel) WorkloadMetrics {
	start := time.Now()

	tasks := 200
	innerIters := 50000

	switch level {
	case LevelUltra:
		tasks = 1000
		innerIters = 600000
	case LevelHeavy:
		tasks = 500
		innerIters = 150000
	case LevelStandard:
		// default
	}

	var wg sync.WaitGroup
	wg.Add(tasks)

	var totalOps int64

	for t := 0; t < tasks; t++ {
		go func(taskID int) {
			defer wg.Done()
			var acc float64
			for i := 1; i <= innerIters; i++ {
				acc += math.Sqrt(float64(i*taskID + 1))
			}
			_ = acc
		}(t)
		totalOps += int64(innerIters)
	}

	wg.Wait()

	elapsed := time.Since(start)
	computeMs := float64(elapsed.Microseconds()) / msPerMicro
	opsPerSec := float64(totalOps) / elapsed.Seconds()

	return WorkloadMetrics{
		WorkloadName:    "Phase C: Concurrent Worker Pool",
		ComputeDuration: elapsed.String(),
		ComputeMs:       computeMs,
		Operations:      totalOps,
		OpsPerSecond:    opsPerSec,
		Detail:          fmt.Sprintf("%d concurrent workers x %dk ops", tasks, innerIters/opsDivisor),
	}
}

func fallbackStr(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}
