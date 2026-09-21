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
- **Shell Script Static Analysis & Hardening**:
  - Pinned toolchain: **ShellCheck v0.11.0**.
  - Strict baseline enforced across all tracked `*.sh` scripts: `#!/usr/bin/env bash`, `set -euo pipefail`, and `IFS=$'\n\t'`.
  - Zero-warning tolerance under `.shellcheckrc` optional rules (`check-unassigned-uppercase`, `quote-safe-variables`, `check-extra-masked-returns`, `check-set-e-suppressed`, `require-variable-braces`).
  - No broad or global rule disables (`SC1090` must remain enabled).
- **Testing Standard**:
  - Table-driven tests with descriptive subtests (`t.Run`).
  - Concurrent execution where safe (`t.Parallel()`).
  - Fail-fast assertions with `github.com/stretchr/testify/require` and property checks with `github.com/stretchr/testify/assert`.
  - Data race detection enabled (`-race`).
  - **Coverage Gate**: Strict threshold of **>= 95%** overall code coverage across all packages.

---

## 4. Development Commands & Workflows

All development operations use pinned Task v3.53.1 through the root `Taskfile.yml`. Bootstrap with
`go install github.com/go-task/task/v3/cmd/task@v3.53.1`; CI pins the installer action and version.
Mandatory linters and scanners must fail when missing. Do not restore Make wrappers or Python helpers.

```bash
task help          # View all available targets and descriptions (respects NO_COLOR=1 and COLOR=0)
task all           # Run complete non-mutating pipeline: tidy-check, fmt-check, lint, vuln, test, coverage gate, build
task fmt           # Format and simplify all Go source files with gofmt -s
task fmt-check     # Check formatting and fail if any Go files need formatting (non-mutating)
task fix           # Apply Go API modernizations ('go fix'), linter auto-fixes, and gofmt -s
task build         # Compile microfat and microfat-stub into bin/
task test          # Run unit tests with race detection
task test-leaks    # Run tests with Go 1.27 goroutine leak detection enabled (MICROFAT_TEST_LEAKS=1)
task check-leaks   # Probe Go 1.27 goroutine leak endpoint during running benchmark workload
task test-dx       # Run regression tests for developer workflows (port safety, formatting, TTY detection)
task pprof         # Open interactive pprof web UI (PROFILE=heap|cpu|goroutine|allocs|mutex|block|goroutineleak)
task coverage      # Generate coverage profile and enforce 95% threshold gate
task lint          # Run all linters (Go via golangci-lint and Bash via shellcheck)
task lint-go       # Run golangci-lint across all packages
task lint-shell    # Run ShellCheck across all tracked Bash scripts
task vuln          # Run govulncheck vulnerability scan
task tidy          # Run go mod tidy and go mod verify
task tidy-check    # Verify module dependencies are tidy using non-mutating verification ('go mod tidy -diff')
task snapshot      # Test local GoReleaser release packaging without publishing
task demo          # Build the demo fat binary in examples/demo
task demo-check    # Verify finished fat binary stub commands (info, optimize, trim, prewarm) with trap cleanup
task bench         # Run benchmark suite in examples/demo
task clean         # Remove build artifacts and coverage files
```

> **Terminal Output & Colors**: All Task commands support the standard [`NO_COLOR`](https://no-color.org) environment variable (`NO_COLOR=1`) and `COLOR=0` Task variable to strip ANSI escape codes and switch Unicode symbols (`✔`/`✖`) to clean ASCII markers (`[OK]`/`[FAIL]`). Terminal detection checks standard output (`[ -t 1 ]`) to automatically disable colors when redirected or non-interactive.
> **Port Safety & Profiling**: Profiling targets (`check-leaks`, `pprof`) detect occupied ports cleanly and fail with an actionable message (`PORT=<port>`, `HTTP_PORT=<port>`) rather than terminating external processes. Profiling cleanup sends graceful SIGTERM before SIGKILL strictly to spawned child processes. Mutex and block profile sampling rates (`MICROFAT_PPROF_BLOCK_RATE`, `MICROFAT_PPROF_MUTEX_FRACTION`) are enabled only when `PROFILE=block` or `PROFILE=mutex` is requested.
> **CI Regression Gates**: Developer workflow regression tests (`test-dx`, `test-leaks`, `check-leaks`, `demo-check`) are CI-enforced on code-changing pull requests. Documentation-only pull requests still run required CI classification, title validation and secret scanning; see the documentation-only policy in `CONTRIBUTING.md`. Push and manual CI runs retain the full checks.


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
