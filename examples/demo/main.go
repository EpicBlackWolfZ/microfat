// Package main implements the microfat multi-workload demonstration and performance benchmark application.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"runtime"
	"sync"
	"time"

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

const (
	matrixDim        = 180
	jsonBatchSize    = 15000
	concurrentTasks  = 200
	defaultAppVer    = "1.0.0"
	seedValue        = 42
	scaleMultiplier  = 100.0
	cryptoBlockSize  = 65536
	cryptoIterations = 200
	byteModulo       = 256
	msPerMicro       = 1000.0
	jsonOpsFactor    = 2
	workerInnerIters = 50000
)

func main() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Microfat Demonstration Workload and Performance Benchmark",
		Long: `A high-performance demonstration application testing SIMD vector math,
bulk memory/JSON processing, and concurrent worker scaling across Go microarchitecture levels.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAllWorkloads(jsonOutput)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output metrics in JSON format")

	cmd.AddCommand(newMathCmd())
	cmd.AddCommand(newJsonCmd())
	cmd.AddCommand(newConcurrentCmd())
	cmd.AddCommand(newAllCmd())

	return cmd
}

func newAllCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "all",
		Short: "Run all 3 benchmark workloads in sequence",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAllWorkloads(jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output metrics in JSON format")
	return cmd
}

func newMathCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "math",
		Short: "Execute SIMD Vector Math & Cryptographic Pipeline (Phase A)",
		RunE: func(cmd *cobra.Command, args []string) error {
			m := runSIMDMathWorkload()
			return outputMetrics([]WorkloadMetrics{m}, jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output metrics in JSON format")
	return cmd
}

func newJsonCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "json-mem",
		Short: "Execute Bulk Memory & JSON Transformation Workload (Phase B)",
		RunE: func(cmd *cobra.Command, args []string) error {
			m := runJSONMemoryWorkload()
			return outputMetrics([]WorkloadMetrics{m}, jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output metrics in JSON format")
	return cmd
}

func newConcurrentCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "concurrent",
		Short: "Execute High-Concurrency Worker Scaling Workload (Phase C)",
		RunE: func(cmd *cobra.Command, args []string) error {
			m := runConcurrentWorkload()
			return outputMetrics([]WorkloadMetrics{m}, jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output metrics in JSON format")
	return cmd
}

func runAllWorkloads(jsonOut bool) error {
	m1 := runSIMDMathWorkload()
	m2 := runJSONMemoryWorkload()
	m3 := runConcurrentWorkload()

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
		fmt.Printf("  • %-38s -> %7.2f ms (%10.0f ops/sec) [%s]\n",
			m.WorkloadName, m.ComputeMs, m.OpsPerSecond, m.Detail)
	}

	fmt.Println("------------------------------------------------------------------")
	fmt.Printf("  Total Pure In-Process Compute Time:   %7.2f ms\n", totalMs)
	fmt.Println("==================================================================")
	return nil
}

// -----------------------------------------------------------------------------
// Workload 1: SIMD Vector Math & Cryptographic Pipeline
// -----------------------------------------------------------------------------
func runSIMDMathWorkload() WorkloadMetrics {
	start := time.Now()

	// 1. Floating-Point Matrix Multiply-Accumulate (FMA & AVX vectorization)
	a := make([][]float64, matrixDim)
	b := make([][]float64, matrixDim)
	c := make([][]float64, matrixDim)

	// #nosec G404 -- deterministic pseudo-random seed for reproducible benchmark runs
	r := rand.New(rand.NewSource(seedValue))
	for i := 0; i < matrixDim; i++ {
		a[i] = make([]float64, matrixDim)
		b[i] = make([]float64, matrixDim)
		c[i] = make([]float64, matrixDim)
		for j := 0; j < matrixDim; j++ {
			a[i][j] = r.Float64() * scaleMultiplier
			b[i][j] = r.Float64() * scaleMultiplier
		}
	}

	// Matrix multiplication with fused multiply-add operations
	var totalOps int64
	for i := 0; i < matrixDim; i++ {
		for k := 0; k < matrixDim; k++ {
			aik := a[i][k]
			for j := 0; j < matrixDim; j++ {
				c[i][j] += math.FMA(aik, b[k][j], math.Sin(float64(i+j)))
				totalOps++
			}
		}
	}

	// 2. Cryptographic Block Hashing
	hasher := sha256.New()
	block := make([]byte, cryptoBlockSize)
	for i := range block {
		block[i] = byte(i % byteModulo)
	}

	for i := 0; i < cryptoIterations; i++ {
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
		Detail:          fmt.Sprintf("Matrix %dx%d FMA + SHA256", matrixDim, matrixDim),
	}
}

// -----------------------------------------------------------------------------
// Workload 2: Bulk Memory & JSON Serialization
// -----------------------------------------------------------------------------
type Record struct {
	ID        int       `json:"id"`
	UUID      string    `json:"uuid"`
	Timestamp time.Time `json:"timestamp"`
	Payload   string    `json:"payload"`
	Scores    []float64 `json:"scores"`
}

func runJSONMemoryWorkload() WorkloadMetrics {
	start := time.Now()

	records := make([]Record, jsonBatchSize)
	now := time.Now()
	for i := 0; i < jsonBatchSize; i++ {
		records[i] = Record{
			ID:        i,
			UUID:      fmt.Sprintf("record-%08d-token", i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Payload:   "High-throughput microfat in-memory serialization test payload block",
			Scores:    []float64{float64(i) * 1.1, float64(i) * 2.2, float64(i) * 3.3},
		}
	}

	// Encode to JSON
	data, err := json.Marshal(records)
	if err != nil {
		panic(err)
	}

	// Decode back
	var decoded []Record
	if err := json.Unmarshal(data, &decoded); err != nil {
		panic(err)
	}

	elapsed := time.Since(start)
	computeMs := float64(elapsed.Microseconds()) / msPerMicro
	totalOps := int64(jsonBatchSize * jsonOpsFactor)
	opsPerSec := float64(totalOps) / elapsed.Seconds()

	return WorkloadMetrics{
		WorkloadName:    "Phase B: Memory & JSON Processing",
		ComputeDuration: elapsed.String(),
		ComputeMs:       computeMs,
		Operations:      totalOps,
		OpsPerSecond:    opsPerSec,
		Detail:          fmt.Sprintf("%d records JSON marshal/unmarshal", jsonBatchSize),
	}
}

// -----------------------------------------------------------------------------
// Workload 3: High-Concurrency Worker Scaling
// -----------------------------------------------------------------------------
func runConcurrentWorkload() WorkloadMetrics {
	start := time.Now()

	var wg sync.WaitGroup
	wg.Add(concurrentTasks)

	var totalOps int64

	for t := 0; t < concurrentTasks; t++ {
		go func(taskID int) {
			defer wg.Done()
			var acc float64
			for i := 1; i <= workerInnerIters; i++ {
				acc += math.Sqrt(float64(i*taskID + 1))
			}
			_ = acc
		}(t)
		totalOps += workerInnerIters
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
		Detail:          fmt.Sprintf("%d concurrent workers x 50k ops", concurrentTasks),
	}
}

func fallbackStr(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}
