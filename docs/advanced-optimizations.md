# Advanced Compiler & Architecture Optimizations

[**← Lifecycle Modes**](lifecycle-modes.md) | [**Main Index**](../README.md#documentation-guide) | [**Troubleshooting & Runbook →**](troubleshooting.md)

---

This guide covers advanced tuning techniques that can be combined with Microfat packaging to explore throughput and latency trade-offs in Go and CGO applications.

---

## 1. AVX-512 Frequency Downclocking Protection

### The Challenge
On Intel Skylake-X and Cascade Lake Xeon, sustained AVX-512 work can reduce core frequency. The magnitude and recovery period depend on the processor and workload; measure throughput and tail latency before selecting a policy.

### Modern Architecture Status
- **AMD Zen 4 & Zen 5**: The Intel-specific downclock protection rule does not target these processors. Measure their behavior with the intended workload.
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
PGO and ISA specialization may improve some workloads, but their gains are not fixed or additive. Compare identical source, dependencies and runtime settings with PGO on/off and specialization on/off; retain raw trials before publishing a percentage.

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
  --stub bin/microfat-stub \
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
Allocator selection and `GOMEMLIMIT` tuning can change memory pressure, but neither prevents OOM termination. Measure the application's peak process and cgroup memory.

---

## 4. Compression Profiles & Decision Matrix

`microfat` supports multi-codec compression (`zstd`, `lz4`, `none`), inter-variant dictionary training (`--dict`), and profile-based configuration to balance cold-start launch overhead against binary size and container image transfer speed.

### Decision Matrix

| Goal | Profile / codec | Trade-off to measure |
| --- | --- | --- |
| Avoid decompression | `latency` / `none` | Larger artifact; hashing, copying and process startup still cost time |
| Fast decompression | `latency` / `lz4` | Compare startup and size against zstd on the same payload |
| General purpose | `balanced` / `zstd` | Default size/startup balance; workload-dependent |
| Reduce transfer/storage | `size` / `zstd:best` | More packing effort; evaluate extraction memory and startup |
| Similar variants | `size` + `--dict` | Measure dictionary benefit and its memory cost |

These are configuration choices, not measured latency or compression guarantees. Use `make bench-matrix`
for the supported format/profile/codec/mode combinations and preserve raw results with host and commit
metadata before publishing numbers.

---

## 5. Programmatic CLI Integration

External applications invoke `microfat pack` as a subprocess. See the tested
[README example](../README.md#programmatic-cli-integration) for argument handling and error propagation.
The repository's `internal/pack` package is not an externally importable API.

---

[**← Lifecycle Modes**](lifecycle-modes.md) | [**Main Index**](../README.md#documentation-guide) | [**Troubleshooting & Runbook →**](troubleshooting.md)
