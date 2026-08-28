# Microfat

[![Go Reference](https://pkg.go.dev/badge/github.com/EpicBlackWolfZ/microfat.svg)](https://pkg.go.dev/github.com/EpicBlackWolfZ/microfat)
[![Go Report Card](https://goreportcard.com/badge/github.com/EpicBlackWolfZ/microfat)](https://goreportcard.com/report/github.com/EpicBlackWolfZ/microfat)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

**Microfat** combines multiple CPU microarchitecture-specific ELF binaries (`v1`, `v2`, `v3`, `v4`) into a single, self-dispatching Linux executable with zero persistent process overhead, automatic container resource tuning (`GOMEMLIMIT` & `GOMAXPROCS`), and cryptographic integrity validation.

---

## Key Highlights

- 🚀 **Dynamic Hardware Dispatch**: Automatically detects host CPU instruction sets (`AVX2`, `FMA`, `BMI2`, `AVX-512`) and boots the optimal machine code at startup.
- ⚡ **Zero Persistent Process Overhead**: Dispatches via Linux `memfd_create` and `syscall.Exec` directly from anonymous RAM (no wrapper daemon, PID 1 preserved in containers).
- 🛡️ **Container Auto-Tuning**: Automatically parses Linux cgroup v1 & v2 limits to set safe `GOMEMLIMIT` pacing (preventing OOMKills) and `GOMAXPROCS` (preventing CPU throttling).
- ✂️ **Flexible Lifecycle Modes**:
  - **Universal Fat Binary**: Distribute a single file that runs everywhere (`v1`–`v4`).
  - **Trimmed Fat Binary (`--microfat:trim`)**: Discard unneeded variants on disk (~45%–50% size reduction) while retaining launcher auto-tuning and RAM execution.
  - **Raw Native ELF (`--microfat:optimize`)**: Permanently specialize to raw uncompressed ELF machine code with 0.0ms launch overhead.
- 🔒 **Cryptographic Verification**: 56-byte cryptographic trailer with SHA-256 index hashing and variant checksum validation.

---

## Documentation Guide

Explore the specialized deep-dive documentation in the [`docs/`](docs/) and [`examples/`](examples/) directories:

| Guide | Description |
| :--- | :--- |
| 📊 [**Demo & Benchmark Suite**](examples/demo/README.md) | Multi-workload benchmark application testing SIMD vector math, JSON/Zstd processing, and concurrent workers. |
| 📖 [**Architecture & Binary Format**](docs/architecture.md) | Technical specification of the 56-byte trailer, JSON Index schema, and `memfd_create` lifecycle. |
| ⚙️ [**Container Resource Auto-Tuning**](docs/runtime-tuning.md) | Linux cgroup v1/v2 auto-tuning (`GOMEMLIMIT` / `GOMAXPROCS`), GC mechanics, and workload `GOGC` tuning recipes. |
| 🔄 [**Binary Lifecycle Modes**](docs/lifecycle-modes.md) | Comprehensive comparison and workflows for Universal Fat, Trimmed Fat, and Native ELF modes. |
| 🚀 [**Advanced Optimizations & Compression Matrix**](docs/advanced-optimizations.md) | AVX-512 downclocking mitigation, Compression Decision Matrix, PGO matrices, and C allocators. |

---

## Quick Start

### 1. Installation

```bash
go install github.com/EpicBlackWolfZ/microfat/cmd/microfat@latest
go install github.com/EpicBlackWolfZ/microfat/cmd/microfat-stub@latest
```

### 2. Detect Host CPU Capabilities & Environment Readiness

```bash
# Check CPU microarchitecture level
microfat detect

# Comprehensive host environment verification (CPU, memfd, cache, cgroups)
microfat doctor

# Machine-readable JSON output for CI/CD gating
microfat doctor --json
```

```text
=== Microfat Host Environment Doctor ===

[✔] Host CPU Microarchitecture
    • OS/Arch:        linux/amd64
    • Detected Level: v3
    • Key Features:   cx16, popcnt, sse3, ssse3, sse4.1, sse4.2, avx, avx2, bmi1, bmi2, fma, osxsave
    • AVX-512 Status: not present (no downclock risk)

[✔] In-Memory Execution (memfd_create)
    • Kernel Support: Available (Linux 6.8.0-generic)
    • Seccomp Filter: Permitted

[✔] Disk Cache Execution Fallback
    • Resolved Path:  /home/deployer/.cache/microfat
    • Permissions:    0700 (read/write OK)

[✔] Container Resource Limits (cgroup v2)
    • Memory Limit:   2147483648 B (2.00 GiB)
    • CFS CPU Quota:  4.00 cores
    • Auto GOMEMLIMIT: 1932735283 B (~1.80 GiB)
    • Auto GOMAXPROCS: 4

[✔] Toolchain & Version Metadata
    • Version:        dev
    • Commit:         none

Summary: Environment is fully ready for high-performance Microfat dispatch!
```

### 3. Compile & Package a Fat Binary

#### AMD64 (x86_64) Packaging
```bash
# 1. Compile variants with Go microarchitecture targets
GOAMD64=v1 go build -o bin/app_v1 main.go
GOAMD64=v3 go build -o bin/app_v3 main.go
GOAMD64=v4 go build -o bin/app_v4 main.go

# 2. Package with microfat
microfat pack \
  --stub ~/.local/bin/microfat-stub \
  --name myapp \
  -v v1=bin/app_v1 \
  -v v3=bin/app_v3 \
  -v v4=bin/app_v4 \
  -o bin/myapp
```

#### ARM64 (aarch64) Packaging
```bash
# 1. Compile ARM64 variants (Graviton 2/3/4, Apple Silicon, Ampere)
GOOS=linux GOARCH=arm64 GOARM64=v8.0 go build -o bin/app_arm64_v8.0 main.go
GOOS=linux GOARCH=arm64 GOARM64=v8.2 go build -o bin/app_arm64_v8.2 main.go
GOOS=linux GOARCH=arm64 GOARM64=v9.0 go build -o bin/app_arm64_v9.0 main.go

# 2. Package universal ARM64 fat binary
microfat pack \
  --stub bin/microfat-stub-arm64 \
  --name myapp \
  --arch arm64 \
  -v v8.0=bin/app_arm64_v8.0 \
  -v v8.2=bin/app_arm64_v8.2 \
  -v v9.0=bin/app_arm64_v9.0 \
  -o bin/myapp-arm64
```

#### Declarative PGO Matrix Packaging (`microfat pgo-pack`)
Automate variant compilation and Profile-Guided Optimization packaging in a single step using a YAML/JSON manifest:

```yaml
# pgo.yaml
name: myapp
package: ./cmd/myapp
output: bin/myapp
variants:
  - level: v1
    pgo: "off"
  - level: v3
    pgo: profiles/v3.pgo
  - level: v4
    pgo: profiles/v4.pgo
```

```bash
microfat pgo-pack --manifest pgo.yaml
```

### 4. Try the Demonstration & Benchmark Suite

```bash
# Build and run the AMD64 demo workloads:
make demo

# Build the ARM64 demo fat binary:
make demo-arm64

# Run the standard benchmark suite (~110ms):
make bench

# Run the heavy sustained compute benchmark suite (~500ms):
make bench-heavy

# Run the ultra sustained compute benchmark suite (5-15s per run):
make bench-ultra
```

---

## Standalone Container Tuning (`runtimeinit`)

For Go services running directly in Docker or Kubernetes without the fat binary wrapper, import `runtimeinit` to automatically tune `debug.SetMemoryLimit` and `runtime.GOMAXPROCS`:

```go
package main

import (
	_ "github.com/EpicBlackWolfZ/microfat/runtimeinit"
)

func main() {
	// GOMEMLIMIT (90% cgroup ceiling) and GOMAXPROCS (CFS quota) are auto-configured
}
```

---

## Runtime Meta-Commands

Every fat executable supports reserved meta-commands for diagnostics and disk specialization:

```bash
# View host capabilities, cgroup limits, and embedded variants
./myapp --microfat:info

# Pre-extract host-optimal variant into cache (eliminates cold start latency)
./myapp --microfat:prewarm

# Trim unneeded variants in-place (keeps stub & cgroup auto-tuning, cuts size ~50%)
./myapp --microfat:trim

# Extract trimmed single-variant fat binary to a target path
./myapp --microfat:trim-to /usr/local/bin/myapp

# Permanently replace with raw uncompressed native ELF
./myapp --microfat:optimize

# Extract raw uncompressed native ELF to a target path
./myapp --microfat:optimize-to /usr/local/bin/myapp
```

---

## Environment Variable & Policy Configuration

| Variable | Default | Description |
| :--- | :--- | :--- |
| `MICROFAT_AUTOTUNE` | `1` / `true` | Set to `0` or `false` to disable automatic `GOMEMLIMIT` and `GOMAXPROCS` injection. |
| `MICROFAT_MEM_RATIO` | `0.90` | Fraction of container cgroup memory limit to assign to `GOMEMLIMIT` (e.g. `0.85`). |
| `MICROFAT_FORCE_LEVEL` | *(unset)* | Pin execution strictly to a specific level (`v1`, `v2`, `v3`, `v4`, `v8.0`..`v9.5`). Fails fast on incompatibility. |
| `MICROFAT_MAX_LEVEL` | *(unset)* | Cap selection ceiling level (e.g. `v3` or `v8.2`). |
| `MICROFAT_DISABLE_VARIANTS` | *(unset)* | Comma-separated list of variant levels to exclude from selection (e.g. `v4`). |
| `MICROFAT_POLICY` | *(unset)* | Preset policy name (`safe_avx512`, `no_downclock`). |
| `MICROFAT_AVX512_DOWNCLOCK_PROTECTION` | `0` / `false` | Enable automatic Intel Skylake-X / Cascade Lake Xeon downclocking mitigation. |
| `MICROFAT_EXEC_MODE` | `memfd` | Execution mechanism: `memfd` (in-RAM) or `cache` (from prewarmed cache). |
| `MICROFAT_CACHE_DIR` | *(unset)* | Custom node cache directory (defaults to `$XDG_CACHE_HOME/microfat` or `~/.cache/microfat`). |
| `GOMEMLIMIT` | *(unset)* | If already set by the user or Kubernetes YAML, `microfat` **never** overrides it. |
| `GOMAXPROCS` | *(unset)* | If already set by the user or Kubernetes YAML, `microfat` **never** overrides it. |

---

## Compression Decision Matrix & Go API Integration

`microfat` provides three compression profiles to balance cold-start startup overhead against disk size and network transfer bandwidth:

| Profile | Codec | Typical Payload Size | Cold-Start Overhead | Ratio | Best-Fit Workload |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `latency` | `none` / `lz4` | `< 10 MB` | **< 80 µs – 350 µs** | 0% – 48% | Sub-millisecond serverless functions, low-latency CLI tools |
| `balanced` *(default)* | `zstd` | `10 MB – 50 MB` | **< 1.5 ms** | ~50% – 60% | Kubernetes daemon sets, general cloud microservices |
| `size` | `zstd:best` (+`--dict`) | `> 50 MB` | **< 4.5 ms – 6.0 ms** | **~65% – 78%** | Multi-variant matrices, bandwidth-constrained edge IoT, large monoliths |

For full benchmarks and sizing recipes, see the [Advanced Optimizations Guide](docs/advanced-optimizations.md#4-compression-profiles--decision-matrix).

### Programmatic Packaging in Go

To package binaries programmatically, initialize options with `pack.DefaultOptions()`:

```go
package main

import (
	"log"

	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

func main() {
	opts := pack.DefaultOptions()
	opts.StubPath = "bin/microfat-stub"
	opts.OutputPath = "bin/myapp-fat"
	opts.AppName = "myapp"
	opts.Variants["v1"] = "dist/app_v1"
	opts.Variants["v3"] = "dist/app_v3"
	opts.Variants["v4"] = "dist/app_v4"

	if _, err := pack.Pack(opts); err != nil {
		log.Fatalf("Packaging failed: %v", err)
	}
}
```

---

## GoReleaser Integration

Add microarchitecture matrix builds to your [`.goreleaser.yaml`](.goreleaser.yaml) for both AMD64 and ARM64:

```yaml
version: 2

builds:
  - id: myapp-amd64
    main: ./main.go
    binary: myapp
    goos: [linux]
    goarch: [amd64]
    goamd64: [v1, v3, v4]

  - id: myapp-arm64
    main: ./main.go
    binary: myapp
    goos: [linux]
    goarch: [arm64]
    goarm64: [v8.0, v8.2, v9.0]
```

---

## License

Apache 2.0
