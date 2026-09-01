# Agent Guidelines for microfat

Welcome to `microfat`! This document serves as the primary technical runbook, architecture reference, and development guide for AI coding assistants working within this repository.

---

## 1. Project Overview & Architecture

`microfat` is a high-performance developer and CI toolkit that packages multiple microarchitecture-specialized Go ELF binaries (e.g., `amd64_v1`, `v2`, `v3`, `v4` or `arm64_v8.0`..`v9.5`) into a single self-dispatching executable.

### Core Architecture
- **`cmd/microfat`**: The primary developer CLI for detecting host CPU capabilities, packing fat binaries, inspecting embedded manifests, verifying integrity, and trimming variants.
- **`cmd/microfat-stub`**: The minimal launcher stub stitched to the front of fat executables. At launch time, it probes the host CPU via `internal/microarch`, detects container resource limits via `internal/cgroup`, and executes the optimal variant directly in RAM using Linux `memfd_create` (falling back to `$XDG_CACHE_HOME/microfat` if memfd is restricted).
- **`internal/format`**: Binary format definition (Format v2 compact binary table and Format v1 JSON manifest), shared dictionary metadata, and fixed 56-byte trailer verification (`\x00\xFA\x7FMICRO` magic).
- **`internal/pack`**: Compression engine utilizing `klauspost/compress/zstd` for parallel frame compression, trained dictionary compression, variant sorting, ELF header validation, and atomic file replacement.
- **`internal/cgroup`**: Linux cgroup v1 and v2 memory/CPU limit parser. Computes optimal runtime parameters (`GOMEMLIMIT` at 90% container ceiling, `GOMAXPROCS` matching CPU CFS quotas).
- **`internal/microarch`**: Dynamic runtime CPU microarchitecture level detection (AMD64 v1-v4, ARM64 v8.0-v9.5) and ranking engine.
- **`internal/version`**: Build-time metadata container injected via ldflags (`Version`, `Commit`, `Date`, `BuiltBy`, `Vendor`).

---

## 2. Binary Layout Specification

A `microfat` fat binary consists of contiguous segments:
```
+-------------------------------------------------------------+
| Launcher Stub Binary (ELF)                                  |
+-------------------------------------------------------------+
| Shared Inter-Variant Dictionary (Optional Zstd Dict)        |
+-------------------------------------------------------------+
| Compressed Variant Payload 1 (zstd / lz4 / none)            |
+-------------------------------------------------------------+
| Compressed Variant Payload 2 (zstd / lz4 / none)            |
+-------------------------------------------------------------+
| ...                                                         |
+-------------------------------------------------------------+
| Metadata Index Table (Format v2 Binary / Format v1 JSON)    |
+-------------------------------------------------------------+
| Trailer (56 Bytes Fixed at EOF)                             |
|  - 8 Bytes uint64 LE : Index Offset                         |
|  - 8 Bytes uint64 LE : Index Size                           |
|  - 32 Bytes Raw      : Index SHA-256 Checksum               |
|  - 8 Bytes Magic     : "\x00\xFA\x7FMICRO"                  |
+-------------------------------------------------------------+
```

---

## 3. Go Toolchain & Quality Invariants

- **Go Version & Toolchain Integrity**:
  - Always target and strictly operate on **Go 1.27** (`go 1.27.1` in `go.mod`).
  - **NEVER downgrade `go.mod` or test toolchain directives** (e.g., to Go 1.26) to bypass local sandbox network restrictions or proxy 403 fetch errors.
  - If the Go toolchain needs to be fetched or updated, run the command with `BypassSandbox: true` instead of altering project version definitions.
- **Module Path**: `github.com/EpicBlackWolfZ/microfat`
- **Strict Linting**: Strictly adhere to `golangci-lint` baseline rules:
  - Zero tolerance for unchecked errors (`errcheck`).
  - No magic numbers (`mnd`). Extract constants.
  - No unclosed response bodies or resources (`bodyclose`).
  - Line length limit <= 140 characters (`lll`).
  - No naked returns (`nakedret`).
- **Testing Standard**:
  - Table-driven tests with descriptive subtests (`t.Run`).
  - Concurrent execution where safe (`t.Parallel()`).
  - Fail-fast assertions with `github.com/stretchr/testify/require` and property checks with `github.com/stretchr/testify/assert`.
  - Data race detection enabled (`-race`).
  - **Coverage Gate**: Strict threshold of **>= 95%** overall code coverage across all packages.

---

## 4. Development Commands & Workflows

All development operations are automated through the root `Makefile`:

```bash
make help       # View all available targets and descriptions
make all        # Run tidy, lint, vuln, test, and build
make build      # Compile microfat and microfat-stub into bin/
make test       # Run unit tests with race detection
make coverage   # Generate coverage profile and enforce 95% threshold gate
make lint       # Run golangci-lint across all packages
make vuln       # Run govulncheck vulnerability scan
make tidy       # Run go mod tidy and go mod verify
make snapshot   # Test local GoReleaser release packaging without publishing
make demo       # Build the demo fat binary in examples/demo
make bench      # Run benchmark suite in examples/demo
make clean      # Remove build artifacts and coverage files
```

---

## 5. Git, CI & Release Conventions

- **Branching & PR Workflow**:
  - **NEVER commit or push directly to the default branch (`main`)**.
  - Always create a dedicated topic branch: `feat/<name>`, `fix/<name>`, `perf/<name>`, `refactor/<name>`, or `docs/<name>`.
  - Open a Pull Request against `main` using `gh pr create` with a Conventional Commit title and detailed markdown body referencing the target issue (`Resolves #<id>`).
  - **Issue Lifecycle**: Do NOT manually close issues via `gh issue close` upon pushing. Issues remain open until the associated PR is merged.
  - **CI Monitoring**: Actively watch and monitor GitHub Actions CI checks (`gh pr checks <pr-number>` or `gh run list`) until all jobs pass green.
  - **Merge Policy**: The project maintainer reviews and merges pull requests manually. Report back once CI is green and matrix verification is complete.
- **Conventional Commits**: PR titles and commit messages must strictly follow the Conventional Commits specification:
  - `feat:` New feature or capability
  - `fix:` Bug fix
  - `docs:` Documentation updates
  - `perf:` Performance improvements
  - `refactor:` Code refactoring without behavior changes
  - `test:` Test additions or enhancements
  - `chore:` Dependency bumps, CI updates, or housekeeping
- **Benchmark & Performance Rigor**:
  - Always distinguish between isolated micro-benchmarks (e.g., sub-microsecond in-memory parsing) and macro end-to-end process cold-start latency (dominated by ELF bootstrap, decompression, and kernel syscalls).
  - When introducing format changes or optimizations, run a full combinatorial matrix benchmark across format versions, stub profiles, compression codecs, and execution modes (`memfd` vs `cache`) to ensure zero runtime anomalies.
- **Release Automation**: Releases are tag-driven (`v*`) via GoReleaser on official GitHub Actions runners (`ubuntu-latest`).
