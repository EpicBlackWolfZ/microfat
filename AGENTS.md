# Agent Guidelines for microfat

Welcome to `microfat`! This document serves as the primary technical runbook, architecture reference, and development guide for AI coding assistants working within this repository.

---

## 1. Project Overview & Architecture

`microfat` is a high-performance developer and CI toolkit that packages multiple microarchitecture-specialized Go ELF binaries (e.g., `amd64_v1`, `v2`, `v3`, `v4` or `arm64_v8.0`..`v9.2`) into a single self-dispatching executable.

### Core Architecture
- **`cmd/microfat`**: The primary developer CLI for detecting host CPU capabilities, packing fat binaries, inspecting embedded manifests, verifying integrity, and trimming variants.
- **`cmd/microfat-stub`**: The minimal launcher stub stitched to the front of fat executables. At launch time, it probes the host CPU via `internal/microarch`, detects container resource limits via `internal/cgroup`, and executes the optimal variant directly in RAM using Linux `memfd_create` (falling back to `$XDG_CACHE_HOME/microfat` if memfd is restricted).
- **`internal/format`**: Binary format definition, JSON index manifest serialization, and fixed 56-byte trailer verification (`\x00\xFA\x7FMICRO` magic).
- **`internal/pack`**: Compression engine utilizing `klauspost/compress/zstd` for parallel frame compression, variant sorting, ELF header validation, and atomic file replacement.
- **`internal/cgroup`**: Linux cgroup v1 and v2 memory/CPU limit parser. Computes optimal runtime parameters (`GOMEMLIMIT` at 90% container ceiling, `GOMAXPROCS` matching CPU CFS quotas).
- **`internal/microarch`**: Dynamic runtime CPU microarchitecture level detection (AMD64 v1-v4, ARM64 v8.0-v9.2) and ranking engine.
- **`internal/version`**: Build-time metadata container injected via ldflags (`Version`, `Commit`, `Date`, `BuiltBy`, `Vendor`).

---

## 2. Binary Layout Specification

A `microfat` fat binary consists of contiguous segments:
```
+-------------------------------------------------------------+
| Launcher Stub Binary (ELF)                                  |
+-------------------------------------------------------------+
| Compressed Variant Payload 1 (zstd)                         |
+-------------------------------------------------------------+
| Compressed Variant Payload 2 (zstd)                         |
+-------------------------------------------------------------+
| ...                                                         |
+-------------------------------------------------------------+
| Index Manifest (JSON)                                       |
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

- **Go Version**: Always target and prefer **Go 1.27**.
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

## 5. Git & Release Conventions

- **Conventional Commits**: PR titles and commit messages must follow the Conventional Commits specification:
  - `feat:` New feature or capability
  - `fix:` Bug fix
  - `docs:` Documentation updates
  - `perf:` Performance improvements
  - `refactor:` Code refactoring without behavior changes
  - `test:` Test additions or enhancements
  - `chore:` Dependency bumps, CI updates, or housekeeping
- **Release Automation**: Releases are tag-driven (`v*`) via GoReleaser on official GitHub Actions runners (`ubuntu-latest`).
