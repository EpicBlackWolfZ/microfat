# Microfat Demonstration & Benchmark Suite

This directory contains a complete, self-contained demonstration project showing the real-world performance impact of **CPU microarchitecture specialization (`v1` vs `v3` vs `v4`)**, **binary lifecycle modes (Universal Fat vs Trimmed Fat vs Native ELF)**, and **runtime container resource tuning (`GOMEMLIMIT` & `GOMAXPROCS`)**.

---

## 1. What the Demo Tests

The application executes three distinct compute and memory workloads across three configurable scale levels:

```mermaid
flowchart LR
    subgraph Workload ["3 Benchmark Workload Phases"]
        A["Phase A: SIMD Math & Crypto<br>• Flat 1D Matrix FMA (AVX2/FMA vectorization)<br>• BMI1/BMI2 Bit Twiddling (math/bits)<br>• SHA-256 Multi-Block Stream"]
        B["Phase B: Memory, JSON & Zstd<br>• 15k - 120k Record Serialization<br>• Zstandard Compression Stream<br>• GC Pacing & Memory Bandwidth"]
        C["Phase C: Concurrent Workers<br>• 200 - 1,000 Goroutine Workers<br>• Millions of Arithmetic Loops<br>• CFS Quota & GOMAXPROCS Scaling"]
    end
```

### The 3 Workload Phases:
1. **Phase A: SIMD Vector Math, BMI2 & Cryptography (`demo math`)**:
   - Floating-point matrix multiply-accumulate operations leveraging hardware **Fused Multiply-Add (`FMA`)** and **`AVX2`** vector units.
   - Hardware bit-manipulation via `math/bits` (emitting native BMI1/BMI2 `tzcnt`, `lzcnt`, `popcnt`, and `rorx` instructions on `v3`/`v4`).
   - Cryptographic streaming via SHA-256 exercising Go's AVX2 multi-buffer assembly routines.
2. **Phase B: Bulk Memory, JSON & Zstd Compression (`demo json-mem`)**:
   - Serialization and deserialization of structured records with timestamps, tokens, and numerical score vectors.
   - High-throughput Zstandard compression/decompression exercising hardware bitstream decoding and memory caching.
   - Stresses heap allocation, garbage collector pacing (`GOMEMLIMIT`), and memory throughput.
3. **Phase C: High-Concurrency Worker Scaling (`demo concurrent`)**:
   - Multi-goroutine parallel worker pool executing arithmetic tasks across all available CPU threads, validating `GOMAXPROCS` container CFS scheduler auto-tuning.

---

## 2. Workload Intensity Modes

| Mode | Flag | Compute Duration | Description |
| :--- | :--- | :--- | :--- |
| **Standard** | *(default)* | `~110 ms` | Quick smoke test and baseline verification (50 iterations). |
| **Heavy** | `--heavy` | `~500 ms` | 5x scaled compute workload amortizing startup overhead (20 iterations). |
| **Ultra** | `--ultra` | `~5 – 15 s` | Full sustained CPU saturation demonstrating multi-second hardware instruction speedups. |

---

## 3. Quick Start

### Build & Run the Universal Fat Binary
```bash
# Build v1, v3, v4 variants and package the universal fat binary:
make fat

# Run standard workload (~110ms):
make run

# Run heavy workload (~500ms):
make run-heavy

# Run ultra-heavy sustained compute workload (5-15s):
make run-ultra
```

### Try the Trimmed Fat Binary Mode
```bash
# Trim the binary in-place to your host architecture (-45% disk size):
make trim

# Inspect the single-variant fat binary:
bin/demo-trimmed --microfat:info

# Run workloads in anonymous RAM with container auto-tuning:
bin/demo-trimmed all --ultra
```

### Materialize Raw Native ELF
```bash
# Permanently extract raw uncompressed native v3 ELF:
make optimize

# Run the native binary directly with zero launch overhead:
bin/demo-optimized all --ultra
```

---

## 4. Running the Benchmark Suites

```bash
# 1. Standard benchmark (50 iterations):
make bench

# 2. Heavy sustained compute benchmark (20 iterations):
make bench-heavy

# 3. Ultra sustained compute benchmark (5-15s per run, reporting seconds):
make bench-ultra
```

### Benchmark Metrics Measured:
1. **Startup Overhead (`--help`)**: Isolates kernel execve, launcher stub decompression, and runtime initialization.
2. **Pure In-Process Compute Time (`ops/sec`, `ms`, and `seconds`)**: Measures pure steady-state hardware throughput inside the application after startup is complete.
3. **Total Wall-Clock Latency**: Startup + computation combined.
4. **Binary Footprint on Disk**: Exact file size across all packaging formats.

---

## 5. Understanding the Results: Startup vs Steady-State Compute

When evaluating fat binaries vs native binaries, it is important to distinguish between **One-Time Startup Overhead** and **Steady-State Compute Speed**:

| Characteristic | Native ELF (`v3`) | Universal FAT | Trimmed FAT (`--microfat:trim`) |
| :--- | :--- | :--- | :--- |
| **Startup Overhead** | `~1.5 ms` | `~7.5 ms` (+6ms decompression) | `~7.5 ms` (+6ms decompression) |
| **Steady-State Compute** | **100% Native Hardware Speed** | **100% Native Hardware Speed** | **100% Native Hardware Speed** |
| **Container Auto-Tuning** | Manual configuration | ✅ Automatic `GOMEMLIMIT` & `GOMAXPROCS` | ✅ Automatic `GOMEMLIMIT` & `GOMAXPROCS` |
| **Hardware Portability** | Runs on `v3` only | Runs on **any** x86-64 CPU (`v1`–`v4`) | Runs on `v3` only |

> [!NOTE]
> The **6ms decompression overhead** occurs **only once at process launch** when streaming the payload into anonymous RAM (`memfd_create`). Once running, CPU vector instructions execute at full native hardware speed. For persistent microservices and servers, this 6ms is completely negligible. For ultra-fast sub-2ms CLI utilities, use `--microfat:optimize` to eliminate the startup overhead entirely.
