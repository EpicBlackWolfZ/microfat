# Microfat

[![Go Reference](https://pkg.go.dev/badge/github.com/ghostnetorg/microfat.svg)](https://pkg.go.dev/github.com/ghostnetorg/microfat)
[![Go Report Card](https://goreportcard.com/badge/github.com/ghostnetorg/microfat)](https://goreportcard.com/report/github.com/ghostnetorg/microfat)

**Microfat** is a high-performance Go-native tool and single-file executable format that combines multiple microarchitecture-specific ELF variants (`v1`, `v2`, `v3`, `v4`) into a single self-dispatching Linux binary with zero persistent process overhead, automatic container resource tuning, and cryptographic verification.

---

## Key Features

- **Dynamic CPU Microarchitecture Dispatch**: Automatically detects host CPU instruction sets (`SSE4.2`, `AVX2/FMA/BMI2`, `AVX-512`) and selects the optimal binary variant at startup.
- **Zero Persistent Process Overhead**: Dispatches via Linux `memfd_create` and `syscall.Exec` (`/proc/self/fd/N`). The launcher process image is replaced in kernel space (same PID, no wrapper daemon).
- **Container Auto-Tuning (`GOMEMLIMIT` & `GOMAXPROCS`)**:
  - Automatically parses Linux cgroup v1 and cgroup v2 limits (`memory.max`, `cpu.max`, `cfs_quota_us`).
  - Sets `GOMEMLIMIT` to 90% of the container memory ceiling (preventing OOMKills while optimizing GC pacing).
  - Sets `GOMAXPROCS` to match the CFS CPU quota (preventing Linux CFS scheduler throttling and p99 latency spikes).
  - Preserves any existing user-configured environment variables.
- **Zero Disk Footprint in Containers**: Executes directly from anonymous RAM without touching the disk—compatible with read-only root filesystems (`readOnlyRootFilesystem: true` in Kubernetes) and non-root users.
- **Cryptographic Integrity & Bounds Protection**: Fixed 56-byte trailer with SHA-256 index hashing and uncompressed payload checksums, validated with `microfat verify`.
- **Permanent Specialization (`--microfat:optimize`)**: Shrinks the executable on disk by permanently shedding unneeded variants and replacing itself with the optimal binary.
- **Explicit Materialization (`--microfat:optimize-to <path>`)**: Extracts the optimal variant to a specific file path for clean multi-stage container builds.
- **Transparent CLI Passthrough**: Forward all application flags, subcommands, standard I/O, signals, and exit codes without collision.

---

## Architecture & Layout

```
+-------------------------------------------------------------------+
| Precompiled Baseline Stub Executable (ELF x86_64 v1)              |
+-------------------------------------------------------------------+
| Variant 1 Zstandard Stream (linux_amd64_v1)                       |
+-------------------------------------------------------------------+
| Variant 2 Zstandard Stream (linux_amd64_v2)                       |
+-------------------------------------------------------------------+
| Variant 3 Zstandard Stream (linux_amd64_v3)                       |
+-------------------------------------------------------------------+
| Variant 4 Zstandard Stream (linux_amd64_v4)                       |
+-------------------------------------------------------------------+
| JSON Index Table:                                                 |
| {                                                                 |
|   "version": 1,                                                   |
|   "app_name": "neko",                                             |
|   "os": "linux",                                                  |
|   "arch": "amd64",                                                |
|   "variants": [ ... ]                                             |
| }                                                                 |
+-------------------------------------------------------------------+
| Fixed Trailer (56 bytes):                                         |
|   - uint64 (8 bytes, Little Endian): Index Offset                 |
|   - uint64 (8 bytes, Little Endian): Index Size                   |
|   - bytes32 (32 bytes): SHA-256 Checksum of Index Table           |
|   - bytes8 (8 bytes): Magic ("\x00\xFA\x7FMICRO")                 |
+-------------------------------------------------------------------+
```

---

## CLI Usage

### 1. Detect Host CPU Level

```bash
microfat detect
# Or JSON output:
microfat detect --json
```

Output:
```text
OS:       linux
Arch:     amd64
Level:    v3
Features: cx16, popcnt, sse3, ssse3, sse4.1, sse4.2, avx, avx2, bmi1, bmi2, fma, osxsave
```

### 2. Package a Fat Binary (`microfat pack`)

```bash
microfat pack \
  --stub bin/microfat-stub \
  --name myapp \
  -v v1=dist/myapp_linux_amd64_v1/myapp \
  -v v3=dist/myapp_linux_amd64_v3/myapp \
  -v v4=dist/myapp_linux_amd64_v4/myapp \
  --output dist/myapp
```

### 3. Inspect a Fat Binary (`microfat inspect`)

```bash
microfat inspect dist/myapp
```

Output:
```text
Binary Path:       dist/myapp
App Name:          myapp
Target Platform:   linux/amd64
Total Size:        18491200 bytes
Created At:        2026-08-25T07:30:00Z

Embedded Variants (3 total):
  • v1     offset:    1845200 | comp:    4201000 B | raw:   12400100 B (33.9%) | sha256: 9f86d081884c...
  • v3     offset:    6046200 | comp:    4350000 B | raw:   12510000 B (34.8%) | sha256: 5e884898da28...
  • v4     offset:   10396200 | comp:    4390000 B | raw:   12600000 B (34.8%) | sha256: 4b227777d4dd...
```

### 4. Verify Payload Integrity (`microfat verify`)

```bash
microfat verify dist/myapp
```

Output:
```text
Verifying 'dist/myapp' (myapp - linux/amd64)...

  [PASS] Variant v1     (size: 12400100 B, sha256: 9f86d081884c7d65...)
  [PASS] Variant v3     (size: 12510000 B, sha256: 5e884898da280471...)
  [PASS] Variant v4     (size: 12600000 B, sha256: 4b227777d4dd1fc6...)

Result: All embedded variants verified successfully with matching SHA-256 checksums.
```

---

## Executable Runtime Meta-Commands & Configuration

Every fat executable created with `microfat` supports reserved prefix flags without colliding with your application commands:

```bash
# Print runtime CPU level, cgroup auto-tuning limits, and embedded variants
./myapp --microfat:info

# Permanently shrink and replace executable in-place with the host's optimal variant
./myapp --microfat:optimize

# Materialize optimal variant directly to a target path (e.g. in Containerfile)
./myapp --microfat:optimize-to /usr/local/bin/myapp

# All standard arguments forward transparently to the payload
./myapp server start --port 8080
```

### Environment Variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `MICROFAT_AUTOTUNE` | `1` / `true` | Set to `0` or `false` to disable automatic `GOMEMLIMIT` and `GOMAXPROCS` injection. |
| `MICROFAT_MEM_RATIO` | `0.90` | Fraction of container cgroup memory limit to assign to `GOMEMLIMIT` (e.g. `0.85`). |
| `GOMEMLIMIT` | *(unset)* | If already set by the user or Kubernetes YAML, `microfat` **never** overrides it. |
| `GOMAXPROCS` | *(unset)* | If already set by the user or Kubernetes YAML, `microfat` **never** overrides it. |

---

## Advanced & Future Optimization Strategies

### Strategy C: AVX-512 Downclocking Protection
- **The Problem**: Older Intel microarchitectures (such as Skylake-X, Cascade Lake, and early Xeon Scalable processors) throttle CPU core clock frequencies by 10%–20% when executing wide 512-bit vector instructions (`AVX-512`). On these older chips, executing `v4` machine code in general application logic can cause overall throughput to drop despite vectorization.
- **Modern Hardware Contrast**: AMD Zen 4/Zen 5 and newer Intel processors (Golden Cove / Ice Lake / Sapphire Rapids / Emerald Rapids) maintain full boost clock frequencies during 512-bit instruction execution with no frequency penalty.
- **Recommended Strategy**:
  - In workloads where vector math is sparse, prioritize `v3` (AVX2 + FMA) unless the host CPU family is identified as AMD Zen 4/5 or Intel Sapphire Rapids+.
  - `pkg/microarch` allows querying CPU model families to distinguish between downclocking-vulnerable CPUs and full-frequency vector processors.

---

### Strategy D: Profile-Guided Optimization (PGO) Matrix
- **The Concept**: Go 1.20+ includes native support for Profile-Guided Optimization (`go build -pgo=profile.pprof`). PGO uses production CPU profiles to optimize hot code paths, inline frequent function calls, and devirtualize interface calls, delivering an additional **5% to 15% CPU efficiency gain**.
- **Packaging with Microfat**:
  - Developers can build variants combining both microarchitecture levels and representative workload PGO profiles:
    ```bash
    # Build v3 variant with web-traffic profile
    GOAMD64=v3 go build -pgo=profiles/web.pprof -o dist/app_v3_pgo ./cmd/app

    # Package into fat binary
    microfat pack --stub bin/microfat-stub -v v1=dist/app_v1 -v v3=dist/app_v3_pgo -o dist/app
    ```
  - This combines hardware instruction specialization with compiler profile specialization in a single binary.

---

### Strategy E: Native C Allocator Selection (mimalloc / jemalloc for CGO)
- **The Concept**: When building Go applications that utilize CGO or native C/C++ libraries (such as RocksDB, SQLite, DuckDB, or image processing pipelines), the standard glibc `malloc` can experience severe lock contention on high-core-count machines (32+ cores).
- **Alternative Allocator Benefits**:
  - **`mimalloc`** (Microsoft) & **`jemalloc`** (FreeBSD / Meta) utilize thread-local heaps, size-class segregation, and arena pooling to eliminate lock contention and reduce heap fragmentation.
- **Packaging with Microfat**:
  - For CGO-heavy tools, compile variants statically linked with `mimalloc` or `jemalloc`:
    ```bash
    # Statically link mimalloc into variant
    CGO_ENABLED=1 CGO_LDFLAGS="-lmimalloc" GOAMD64=v3 go build -o dist/app_v3_mimalloc ./cmd/app
    ```
  - Enables high-performance native memory allocation on large multi-core nodes without requiring host-level `LD_PRELOAD` setup.

---

## GoReleaser Integration

In your `.goreleaser.yaml`:

```yaml
version: 2

builds:
  - id: myapp
    main: ./cmd/myapp
    binary: myapp
    goos: [linux]
    goarch: [amd64]
    goamd64: [v1, v3, v4]

# After goreleaser build finishes:
# microfat pack --stub bin/microfat-stub -v v1=dist/myapp_linux_amd64_v1/myapp -v v3=dist/myapp_linux_amd64_v3/myapp -v v4=dist/myapp_linux_amd64_v4/myapp -o dist/myapp
```

---

## License

Apache 2.0 / MIT
