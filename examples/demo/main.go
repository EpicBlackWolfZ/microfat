// Package main implements the microfat multi-workload demonstration and performance benchmark application.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"math"
	"math/bits"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
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
	dimStandard     = 180
	dimHeavy        = 300
	dimUltra        = 1100
	cryptoStd       = 200
	cryptoHeavy     = 1000
	cryptoUltra     = 20000
	bitStd          = 200000
	bitHeavy        = 2000000
	bitUltra        = 30000000
	batchStd        = 15000
	batchHeavy      = 50000
	batchUltra      = 250000
	tasksStd        = 200
	tasksHeavy      = 500
	tasksUltra      = 1000
	innerStd        = 50000
	innerHeavy      = 150000
	innerUltra      = 600000
	maxPooledBatch  = 250000
	vecOffset0      = 0
	vecOffset1      = 1
	vecOffset2      = 2
	vecOffset3      = 3
	vecOffset4      = 4
	vecOffset5      = 5
	vecOffset6      = 6
	vecOffset7      = 7
	vecUnroll8      = 8
	vecUnroll4      = 4
)

var (
	flagJSON        bool
	flagHeavy       bool
	flagUltra       bool
	flagStartupOnly bool
	flagSIMD        bool
	flagCPUProfile  string
)

var (
	bufferPool = sync.Pool{
		New: func() any {
			return new(bytes.Buffer)
		},
	}
	recordSlicePool = sync.Pool{
		New: func() any {
			s := make([]Record, 0, batchHeavy)
			return &s
		},
	}
	zstdWriterPool = sync.Pool{
		New: func() any {
			w, err := zstd.NewWriter(nil)
			if err != nil {
				panic(err)
			}
			return w
		},
	}
	zstdReaderPool = sync.Pool{
		New: func() any {
			r, err := zstd.NewReader(nil)
			if err != nil {
				panic(err)
			}
			return r
		},
	}
)

// StartupStatus records startup initialization state when --startup-only and --json are enabled.
type StartupStatus struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func getWorkloadLevel() WorkloadLevel {
	if flagUltra {
		return LevelUltra
	}
	if flagHeavy {
		return LevelHeavy
	}
	return LevelStandard
}

func levelToString(level WorkloadLevel) string {
	switch level {
	case LevelUltra:
		return "ultra"
	case LevelHeavy:
		return "heavy"
	default:
		return "standard"
	}
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
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if flagCPUProfile != "" && !flagStartupOnly {
				cleanProfile := filepath.Clean(flagCPUProfile)
				f, err := os.Create(cleanProfile)
				if err != nil {
					return fmt.Errorf("creating CPU profile file: %w", err)
				}
				if err := pprof.StartCPUProfile(f); err != nil {
					_ = f.Close()
					return fmt.Errorf("starting CPU profile: %w", err)
				}
			}
			if flagStartupOnly {
				if flagJSON {
					if err := json.MarshalWrite(cmd.OutOrStdout(), StartupStatus{
						Status:  "ready",
						Version: defaultAppVer,
					}, jsontext.WithIndent("  ")); err != nil {
						return err
					}
					if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
						return err
					}
				} else {
					if _, err := fmt.Fprintln(cmd.OutOrStdout(), "READY"); err != nil {
						return err
					}
				}
				cmd.RunE = func(c *cobra.Command, a []string) error {
					return nil
				}
			}
			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			if flagCPUProfile != "" && !flagStartupOnly {
				pprof.StopCPUProfile()
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAllWorkloads(flagJSON, getWorkloadLevel(), flagSIMD)
		},
	}

	cmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "Output metrics in JSON format")
	cmd.PersistentFlags().BoolVar(&flagHeavy, "heavy", false, "Run heavy compute workload (~500ms)")
	cmd.PersistentFlags().BoolVar(&flagUltra, "ultra", false, "Run ultra-heavy sustained compute workload (10s of seconds)")
	cmd.PersistentFlags().BoolVar(&flagStartupOnly, "startup-only", false, "Run runtime initialization and exit immediately")
	cmd.PersistentFlags().BoolVar(&flagSIMD, "simd", false, "Enable experimental wide-vector unrolled SIMD math kernels")
	cmd.PersistentFlags().StringVar(&flagCPUProfile, "cpu-profile", "", "Write pprof CPU profile to specified file")

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
			return runAllWorkloads(flagJSON, getWorkloadLevel(), flagSIMD)
		},
	}
}

func newMathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "math",
		Short: "Execute SIMD Vector Math & Cryptographic Pipeline (Phase A)",
		RunE: func(cmd *cobra.Command, args []string) error {
			m := runSIMDMathWorkload(getWorkloadLevel(), flagSIMD)
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

func runAllWorkloads(jsonOut bool, level WorkloadLevel, simd bool) error {
	m1 := runSIMDMathWorkload(level, simd)
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
		if err := json.MarshalWrite(os.Stdout, report, jsontext.WithIndent("  ")); err != nil {
			return err
		}
		fmt.Println()
		return nil
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
func runSIMDMathWorkload(level WorkloadLevel, simd bool) WorkloadMetrics {
	start := time.Now()

	dim := dimStandard
	cryptoIters := cryptoStd
	bitIters := bitStd

	switch level {
	case LevelUltra:
		dim = dimUltra
		cryptoIters = cryptoUltra
		bitIters = bitUltra
	case LevelHeavy:
		dim = dimHeavy
		cryptoIters = cryptoHeavy
		bitIters = bitHeavy
	case LevelStandard:
		// default values
	}

	// 1. Contiguous 1D Flat Slices for Vector Matrix Multiplication (AVX2/AVX-512/NEON)
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
	if simd {
		// 8-way unrolled vector fused multiply-accumulate across contiguous rows
		for i := 0; i < dim; i++ {
			iOff := i * dim
			for k := 0; k < dim; k++ {
				aik := a[iOff+k]
				kOff := k * dim
				j := 0
				for ; j <= dim-vecUnroll8; j += vecUnroll8 {
					c0 := c[iOff+j+vecOffset0]
					c1 := c[iOff+j+vecOffset1]
					c2 := c[iOff+j+vecOffset2]
					c3 := c[iOff+j+vecOffset3]
					c4 := c[iOff+j+vecOffset4]
					c5 := c[iOff+j+vecOffset5]
					c6 := c[iOff+j+vecOffset6]
					c7 := c[iOff+j+vecOffset7]

					b0 := b[kOff+j+vecOffset0]
					b1 := b[kOff+j+vecOffset1]
					b2 := b[kOff+j+vecOffset2]
					b3 := b[kOff+j+vecOffset3]
					b4 := b[kOff+j+vecOffset4]
					b5 := b[kOff+j+vecOffset5]
					b6 := b[kOff+j+vecOffset6]
					b7 := b[kOff+j+vecOffset7]

					s0 := math.Sin(float64(i + j + vecOffset0))
					s1 := math.Sin(float64(i + j + vecOffset1))
					s2 := math.Sin(float64(i + j + vecOffset2))
					s3 := math.Sin(float64(i + j + vecOffset3))
					s4 := math.Sin(float64(i + j + vecOffset4))
					s5 := math.Sin(float64(i + j + vecOffset5))
					s6 := math.Sin(float64(i + j + vecOffset6))
					s7 := math.Sin(float64(i + j + vecOffset7))

					c[iOff+j+vecOffset0] = math.FMA(aik, b0, c0+s0)
					c[iOff+j+vecOffset1] = math.FMA(aik, b1, c1+s1)
					c[iOff+j+vecOffset2] = math.FMA(aik, b2, c2+s2)
					c[iOff+j+vecOffset3] = math.FMA(aik, b3, c3+s3)
					c[iOff+j+vecOffset4] = math.FMA(aik, b4, c4+s4)
					c[iOff+j+vecOffset5] = math.FMA(aik, b5, c5+s5)
					c[iOff+j+vecOffset6] = math.FMA(aik, b6, c6+s6)
					c[iOff+j+vecOffset7] = math.FMA(aik, b7, c7+s7)

					totalOps += vecUnroll8
				}
				for ; j < dim; j++ {
					c[iOff+j] += math.FMA(aik, b[kOff+j], math.Sin(float64(i+j)))
					totalOps++
				}
			}
		}
	} else {
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
	}

	// 2. Hardware Bit Manipulation (BMI1 / BMI2 via math/bits)
	if simd {
		var b0 uint64 = bitInitialMask
		var b1 uint64 = bitInitialMask ^ 0x5555555555555555
		var b2 uint64 = bitInitialMask ^ 0x3333333333333333
		var b3 uint64 = bitInitialMask ^ 0x0F0F0F0F0F0F0F0F
		for i := 0; i < bitIters; i += vecUnroll4 {
			b0 = bits.RotateLeft64(b0, bitShiftAmount) ^ uint64(i+vecOffset0)
			b1 = bits.RotateLeft64(b1, bitShiftAmount) ^ uint64(i+vecOffset1)
			b2 = bits.RotateLeft64(b2, bitShiftAmount) ^ uint64(i+vecOffset2)
			b3 = bits.RotateLeft64(b3, bitShiftAmount) ^ uint64(i+vecOffset3)

			totalOps += int64(bits.OnesCount64(b0) + bits.LeadingZeros64(b0) + bits.TrailingZeros64(b0))
			totalOps += int64(bits.OnesCount64(b1) + bits.LeadingZeros64(b1) + bits.TrailingZeros64(b1))
			totalOps += int64(bits.OnesCount64(b2) + bits.LeadingZeros64(b2) + bits.TrailingZeros64(b2))
			totalOps += int64(bits.OnesCount64(b3) + bits.LeadingZeros64(b3) + bits.TrailingZeros64(b3))
		}
		_ = b0 + b1 + b2 + b3
	} else {
		var bitAcc uint64 = bitInitialMask
		for i := 0; i < bitIters; i++ {
			bitAcc = bits.RotateLeft64(bitAcc, bitShiftAmount) ^ uint64(i)
			totalOps += int64(bits.OnesCount64(bitAcc))
			totalOps += int64(bits.LeadingZeros64(bitAcc))
			totalOps += int64(bits.TrailingZeros64(bitAcc))
		}
		_ = bitAcc
	}

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

	detail := fmt.Sprintf("Flat %dx%d FMA + BMI2 + SHA256", dim, dim)
	if simd {
		detail = fmt.Sprintf("Flat %dx%d FMA + BMI2 + SHA256 (SIMD 8-Way Unrolled)", dim, dim)
	}

	return WorkloadMetrics{
		WorkloadName:    "Phase A: SIMD Math & Cryptography",
		ComputeDuration: elapsed.String(),
		ComputeMs:       computeMs,
		Operations:      totalOps,
		OpsPerSecond:    opsPerSec,
		Detail:          detail,
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

	batchSize := batchStd
	switch level {
	case LevelUltra:
		batchSize = batchUltra
	case LevelHeavy:
		batchSize = batchHeavy
	case LevelStandard:
		// default
	}

	recordPtr := recordSlicePool.Get().(*[]Record)
	records := (*recordPtr)[:0]
	if cap(records) < batchSize {
		records = make([]Record, 0, batchSize)
	}
	defer func() {
		if cap(records) <= maxPooledBatch {
			res := records[:0]
			recordSlicePool.Put(&res)
		}
	}()

	now := time.Now()
	for i := 0; i < batchSize; i++ {
		records = append(records, Record{
			ID:        i,
			UUID:      fmt.Sprintf("record-%08d-token", i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Payload:   "High-throughput microfat in-memory serialization and zstd compression test payload block",
			Scores:    []float64{float64(i) * 1.1, float64(i) * 2.2, float64(i) * 3.3},
		})
	}

	// 1. Encode JSON (encoding/json/v2)
	jsonData, err := json.Marshal(records)
	if err != nil {
		panic(err)
	}

	// 2. Compress with Zstandard using buffer & writer pool
	zstdBuf := bufferPool.Get().(*bytes.Buffer)
	zstdBuf.Reset()
	defer func() {
		zstdBuf.Reset()
		bufferPool.Put(zstdBuf)
	}()

	enc := zstdWriterPool.Get().(*zstd.Encoder)
	enc.Reset(zstdBuf)
	if _, err := enc.Write(jsonData); err != nil {
		panic(err)
	}
	if err := enc.Close(); err != nil {
		panic(err)
	}
	zstdWriterPool.Put(enc)

	// 3. Decompress Zstandard using buffer & reader pool
	dec := zstdReaderPool.Get().(*zstd.Decoder)
	if err := dec.Reset(bytes.NewReader(zstdBuf.Bytes())); err != nil {
		panic(err)
	}

	decompressedBuf := bufferPool.Get().(*bytes.Buffer)
	decompressedBuf.Reset()
	defer func() {
		decompressedBuf.Reset()
		bufferPool.Put(decompressedBuf)
	}()

	if _, err := decompressedBuf.ReadFrom(dec); err != nil {
		panic(err)
	}
	zstdReaderPool.Put(dec)

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

	tasks := tasksStd
	innerIters := innerStd

	switch level {
	case LevelUltra:
		tasks = tasksUltra
		innerIters = innerUltra
	case LevelHeavy:
		tasks = tasksHeavy
		innerIters = innerHeavy
	case LevelStandard:
		// default
	}

	var wg sync.WaitGroup
	wg.Add(tasks)

	var totalOps int64

	labels := pprof.Labels(
		"workload", "concurrent_scaling",
		"level", levelToString(level),
		"gomaxprocs", strconv.Itoa(runtime.GOMAXPROCS(0)),
		"tasks", strconv.Itoa(tasks),
	)

	pprof.Do(context.Background(), labels, func(ctx context.Context) {
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
	})

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
