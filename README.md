# Microfat

[![Go Reference](https://pkg.go.dev/badge/github.com/ghostnetorg/microfat.svg)](https://pkg.go.dev/github.com/ghostnetorg/microfat)
[![Go Report Card](https://goreportcard.com/badge/github.com/ghostnetorg/microfat)](https://goreportcard.com/report/github.com/ghostnetorg/microfat)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

**Microfat** combines multiple CPU microarchitecture-specific ELF binaries (`v1`, `v2`, `v3`, `v4`) into a single, self-dispatching Linux executable with zero persistent process overhead, automatic container resource tuning (`GOMEMLIMIT` & `GOMAXPROCS`), and cryptographic integrity validation.

---

## Key Highlights

- 🚀 **Dynamic Hardware Dispatch**: Automatically detects host CPU instruction sets (`AVX2`, `FMA`, `BMI2`, `AVX-512`) and boots the optimal machine code at startup.
- ⚡ **Zero Persistent Process Overhead**: Dispatches via Linux `memfd_create` and `syscall.Exec` directly from anonymous RAM (no wrapper daemon, PID 1 preserved in containers).
- 🛡️ **Container Auto-Tuning**: Automatically parses Linux cgroup v1 & v2 limits to set safe `GOMEMLIMIT` pacing (preventing OOMKills) and `GOMAXPROCS` (preventing CPU throttling).
- ✂️ **Flexible Lifecycle Modes**:
  - **Universal Fat Binary**: Distribute a single file that runs everywhere (`v1`–`v4`).
  - **Trimmed Fat Binary (`--microfat:trim`)**: Discard unneeded variants on disk (~50% size reduction) while retaining launcher auto-tuning and RAM execution.
  - **Raw Native ELF (`--microfat:optimize`)**: Permanently specialize to raw uncompressed ELF machine code with 0.0ms launch overhead.
- 🔒 **Cryptographic Verification**: 56-byte cryptographic trailer with SHA-256 index hashing and variant checksum validation.

---

## Documentation Guide

Explore the specialized deep-dive documentation in the [`docs/`](docs/) directory:

| Guide | Description |
| :--- | :--- |
| 📖 [**Architecture & Binary Format**](docs/architecture.md) | Technical specification of the 56-byte trailer, JSON Index schema, and `memfd_create` lifecycle. |
| ⚙️ [**Container Resource Auto-Tuning**](docs/runtime-tuning.md) | Linux cgroup v1/v2 formulas for `GOMEMLIMIT` (90% pacing) and `GOMAXPROCS` (CFS quota). |
| 🔄 [**Binary Lifecycle Modes**](docs/lifecycle-modes.md) | Comprehensive comparison and workflows for Universal Fat, Trimmed Fat, and Native ELF modes. |
| 🚀 [**Advanced Compiler Optimizations**](docs/advanced-optimizations.md) | AVX-512 downclocking protection, Profile-Guided Optimization (PGO), and C allocators (`mimalloc`). |

---

## Quick Start

### 1. Installation

```bash
go install github.com/ghostnetorg/microfat/cmd/microfat@latest
go install github.com/ghostnetorg/microfat/cmd/microfat-stub@latest
```

### 2. Detect Host CPU Capabilities

```bash
microfat detect
```

```text
OS:       linux
Arch:     amd64
Level:    v3
Features: cx16, popcnt, sse3, ssse3, sse4.1, sse4.2, avx, avx2, bmi1, bmi2, fma, osxsave
```

### 3. Compile & Package a Fat Binary

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

### 4. Inspect & Verify

```bash
# Inspect embedded variants and compression ratios:
microfat inspect bin/myapp

# Verify SHA-256 integrity and boundary checksums:
microfat verify bin/myapp
```

---

## Runtime Meta-Commands

Every fat executable supports reserved meta-commands for diagnostics and disk specialization:

```bash
# View host capabilities, cgroup limits, and embedded variants
./myapp --microfat:info

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

## Environment Variable Configuration

| Variable | Default | Description |
| :--- | :--- | :--- |
| `MICROFAT_AUTOTUNE` | `1` / `true` | Set to `0` or `false` to disable automatic `GOMEMLIMIT` and `GOMAXPROCS` injection. |
| `MICROFAT_MEM_RATIO` | `0.90` | Fraction of container cgroup memory limit to assign to `GOMEMLIMIT` (e.g. `0.85`). |
| `GOMEMLIMIT` | *(unset)* | If already set by the user or Kubernetes YAML, `microfat` **never** overrides it. |
| `GOMAXPROCS` | *(unset)* | If already set by the user or Kubernetes YAML, `microfat` **never** overrides it. |

---

## GoReleaser Integration

Add microarchitecture matrix builds to your [`.goreleaser.yaml`](.goreleaser.yaml):

```yaml
version: 2

builds:
  - id: myapp
    main: ./main.go
    binary: myapp
    goos: [linux]
    goarch: [amd64]
    goamd64: [v1, v3, v4]
```

---

## License

Apache 2.0 / MIT
