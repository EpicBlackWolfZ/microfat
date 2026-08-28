# Advanced Compiler & Architecture Optimizations

This guide covers advanced tuning techniques that can be combined with Microfat packaging to achieve optimal throughput and latency in Go and CGO applications.

---

## 1. AVX-512 Frequency Downclocking Protection

### The Challenge
On older Intel microarchitectures (Intel Skylake-X, Cascadelake), executing heavy 512-bit vector instructions causes the CPU core power license to switch from `License 0` to `License 2`. This reduces CPU core clock frequencies by **15% to 25%** for all scalar operations on that core for ~2 milliseconds after the vector instruction executes.

### Modern Architecture Status
- **Modern AMD Zen 4 & Zen 5**: Uses native dual 256-bit or full 512-bit vector pipelines with **zero frequency penalty**.
- **Modern Intel Sapphire Rapids / Emerald Rapids**: Frequency scaling penalties are negligible.

### Mitigation Strategies & Policy Controls in Microfat
Microfat provides runtime dispatch policies to automatically mitigate frequency scaling penalties or pin variants:

1. **Automated Skylake-X / Cascade Lake Downclocking Protection**:
   - Set `MICROFAT_POLICY=safe_avx512` or `MICROFAT_AVX512_DOWNCLOCK_PROTECTION=1`.
   - Microfat detects Intel Family 6 Model 85 CPUs and automatically caps selection at `v3` (AVX2 + FMA), preventing `v4` AVX-512 frequency downclocking while continuing to execute `v4` at full speed on AMD Zen 4/5 and Intel Sapphire Rapids.
2. **Explicit Variant Pinning (`MICROFAT_FORCE_LEVEL`)**:
   - Pin execution strictly to a specific tier: `MICROFAT_FORCE_LEVEL=v3`.
   - Fails fast with an explanatory error if the requested variant exceeds the host CPU's hardware capabilities.
3. **Maximum Level Ceiling (`MICROFAT_MAX_LEVEL`)**:
   - Cap selection ceiling: `MICROFAT_MAX_LEVEL=v3`. Automatically falls back to the highest compatible level $\le$ ceiling.
4. **Denylisting Variants (`MICROFAT_DISABLE_VARIANTS`)**:
   - Exclude specific variants: `MICROFAT_DISABLE_VARIANTS=v4` or `MICROFAT_DISABLE_VARIANTS=v4,v9.2`.
5. **CLI Trim Policy Flags**:
   - When preparing containers via `microfat trim`:
     ```bash
     microfat trim app.fat --max-level v3 --policy safe_avx512 -o app_trimmed.fat
     ```

---

## 2. Profile-Guided Optimization (PGO) Matrix

Profile-Guided Optimization (PGO) feeds runtime CPU profiling data (`default.pgo`) into the Go compiler to optimize inlining, register allocation, and branch probability.

### Performance Uplift
- Standard Go: **+7% to +14%** throughput reduction in CPU cycles.
- Microfat + PGO: Combined **+20% to +35%** latency improvements when PGO is combined with `GOAMD64=v3`/`v4`.

### Declarative Matrix Packaging with `microfat pgo-pack`

Microfat provides built-in PGO matrix compilation and packaging via `microfat pgo-pack`. Define a build manifest (`pgo.yaml` or `pgo.json`):

```yaml
name: myapp
package: ./cmd/myapp
output: bin/myapp
stub: bin/microfat-stub
target_os: linux
target_arch: amd64
default_pgo: profiles/default.pgo # Optional fallback profile
build_flags:
  - "-trimpath"
variants:
  - level: v1
    pgo: "off"                      # Baseline without PGO
  - level: v3
    pgo: profiles/v3.pgo            # AVX2 profile
  - level: v4
    pgo: profiles/v4.pgo            # AVX-512 profile
```

Compile and package in a single step with concurrent worker orchestration:

```bash
microfat pgo-pack --manifest pgo.yaml
```

Alternatively, use the `pack` shorthand:
```bash
microfat pack --manifest pgo.yaml -o bin/myapp
```

### Manual Compilation Workflow (Alternative)
If building outside of `microfat pgo-pack`:
```bash
# 1. Build v1 baseline without or with baseline profile
GOAMD64=v1 go build -pgo=off -o bin/app_v1 ./cmd/myapp

# 2. Build v3 with AVX2 profile
GOAMD64=v3 go build -pgo=profiles/v3.pgo -o bin/app_v3 ./cmd/myapp

# 3. Build v4 with AVX-512 profile
GOAMD64=v4 go build -pgo=profiles/v4.pgo -o bin/app_v4 ./cmd/myapp

# 4. Pack into universal self-dispatching fat binary
microfat pack \
  --stub ~/.local/bin/microfat-stub \
  --name myapp \
  -v v1=bin/app_v1 \
  -v v3=bin/app_v3 \
  -v v4=bin/app_v4 \
  -o bin/myapp
```

---

## 3. High-Performance C Allocators (CGO + `mimalloc` / `jemalloc`)

When compiling Go services with CGO (e.g. SQLite, Kafka librdkafka, TensorRT, OpenCV), default glibc `malloc` experiences lock contention under high concurrency.

### Recommended C Allocators
1. **`mimalloc`** (Microsoft):
   - Fastest general-purpose allocator with thread-local free lists and huge page support.
   - Low fragmentation in long-running containerized daemon workloads.
2. **`jemalloc`** (FreeBSD / Meta):
   - Advanced multi-arena allocation designed to eliminate lock contention on high-core-count servers.

### Integration with CGO Builds
```bash
# Build v3 CGO variant linked against mimalloc
CGO_ENABLED=1 \
GOAMD64=v3 \
CGO_LDFLAGS="-lmimalloc" \
go build -o bin/app_v3 main.go
```
Combined with `microfat-stub`'s automated `GOMEMLIMIT` pacing, memory fragmentation and OOM terminations are virtually eliminated.

---

## 4. Compression Profiles & Decision Matrix

`microfat` supports multi-codec compression (`zstd`, `lz4`, `none`), inter-variant dictionary training, and profile-based configuration to balance cold-start launch overhead against binary size and container image transfer speed.

### Decision Matrix

| Optimization Goal | Profile | Recommended Codec / Level | Binary Payload Size | Cold-Start Overhead | Typical Compression Ratio | Best-Fit Workload Archetypes |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **Instant Launch / Zero Decompression** | `latency` | `none` (raw uncompressed) | `< 10 MB` | **< 80 µs** | 0% (full size) | Sub-millisecond serverless functions, low-latency CLI tools, node boot hooks |
| **Ultra-Fast Decompression** | `latency` | `lz4` | `10 MB – 50 MB` | **< 350 µs** | ~40% – 48% | Latency-sensitive microservices, frequently restarted worker pods |
| **General Purpose (Default)** | `balanced` | `zstd` (level 3 / better) | `10 MB – 50 MB` | **< 1.5 ms** | ~50% – 60% | Kubernetes daemon sets, general cloud microservices, CI/CD pipeline builds |
| **Maximum Disk & Network Reduction** | `size` | `zstd:best` | `> 50 MB` | **< 4.5 ms** | ~62% – 70% | Bandwidth-constrained deployments, registry storage optimization, large monoliths |
| **Multi-Architecture Matrix (Shared Dict)** | `size` + `--dict` | `zstd` + trained dictionary | `> 50 MB` (multi-variant) | **< 6.0 ms** | **~70% – 78%** | 4+ variant matrices (`v1`–`v4`, `v8.0`–`v9.2`), edge IoT gateways, golden VM images |

### Programmatic Go API Integration

When creating fat executables programmatically via the `pack` Go package, use `pack.DefaultOptions()` to obtain a safe baseline pre-configured for Format v2 and balanced Zstandard compression:

```go
package main

import (
	"log"

	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

func main() {
	// Initialize default configuration (Format v2, balanced Zstd, 0755 permissions, linux/amd64)
	opts := pack.DefaultOptions()
	opts.StubPath = "bin/microfat-stub"
	opts.OutputPath = "bin/myapp-fat"
	opts.AppName = "myapp"

	// Register microarchitecture variant binaries
	opts.Variants["v1"] = "dist/app_v1"
	opts.Variants["v3"] = "dist/app_v3"
	opts.Variants["v4"] = "dist/app_v4"

	// Package universal executable
	idx, err := pack.Pack(opts)
	if err != nil {
		log.Fatalf("Packaging failed: %v", err)
	}

	log.Printf("Successfully packaged %d variants into Format v%d fat binary", len(idx.Variants), idx.Version)
}
```

