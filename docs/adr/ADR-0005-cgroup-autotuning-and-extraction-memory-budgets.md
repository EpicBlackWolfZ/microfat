# ADR-0005: Cgroup v1/v2 Runtime Autotuning & Window-Accurate Extraction Budgets

- **Status**: Accepted (Retroactive — v0.1–v0.4)
- **Date**: 2026-11-27
- **Deciders**: Project Maintainer (@EpicBlackWolfZ)
- **Related Issues**: [#479](https://github.com/EpicBlackWolfZ/microfat/pull/479), [#501](https://github.com/EpicBlackWolfZ/microfat/issues/501), [#523](https://github.com/EpicBlackWolfZ/microfat/issues/523), [#526](https://github.com/EpicBlackWolfZ/microfat/issues/526)
- **Related Specs**: [SPEC Index](../specs/README.md) (`SPEC-0004`), [Runtime Tuning Guide](../runtime-tuning.md), [Cgroup Memory Verification](../design/cgroup-memory-verification.md)
- **Supersedes**: None
- **Superseded By**: None

## Context

Go applications running inside CPU- and memory-constrained Linux containers default `GOMAXPROCS` (pre-Go container-aware limits or mixed CFS ceilings) and garbage collection without accounting for cgroup `memory.high` throttles, `memfd`-retained executable RAM, or container decompression bursts. Furthermore, naive extraction budget checks (`512 MiB` worst-case decoder reservation regardless of frame window) falsely rejected small containers running payloads packed with bounded `<= 8 MiB` Zstd windows, while unguarded 64-bit arithmetic risks integer overflow.

## Decision

`microfat` embeds a Linux cgroup v1/v2 prober and verified runtime/extraction planner shared across launchers (`cmd/microfat-stub`), `runtimeinit`, `microfat doctor`, and Rust (`internal/cgroup/`):

1. **Cgroup v1/v2 Probing:** Resolves `/proc/self/cgroup` and cgroup hierarchy ceilings, picking the tightest finite bound across `memory.max` and `memory.high` (preserving finite `0` distinct from `"max"` / unlimited), and computes CPU quota/period ratios cleanly even when a v1 CPU controller exists without a memory controller (`v0.3.0`).
2. **Soft Go Runtime Budget & Precedence:**
   - Never overrides explicit `GOMEMLIMIT`, `GOMAXPROCS`, or `GOGC` from the caller environment (including empty values).
   - Subtracts page-aligned `RetainedExecutableBytes` when executing from anonymous RAM (`memfd`) via `RetainedMemoryLimit`, then computes `GOMEMLIMIT` using `MICROFAT_MEM_RATIO` (default `0.90` when `MICROFAT_GC_PROFILE=default`, respecting configured headroom bounds), floor-rounded `GOMAXPROCS` (`MICROFAT_CPU_POLICY=static` vs `native`), and optional `GOGC` profiles (`default`, `latency_critical`, `memory_constrained`, `batch_etl` with finite-memory safety guard, and `adaptive` requiring `MICROFAT_LIVE_HEAP_ESTIMATE`).
   - Supports dry-run (`MICROFAT_DRY_RUN=1`) and opt-out (`MICROFAT_AUTOTUNE=0`).
3. **Window-Accurate Extraction Preflight (`v0.4+` / `#501`):** Computes `ExtractionMemory` and `ZstdDecoderMemory` from the frame header's actual Zstd window size (`InspectZstdFrameWindow`) plus bounded overhead within `[0, 128 MiB]` rather than charging `512 MiB` unconditionally, failing closed with `cgroup.ErrMemoryBudget` on overflow or budget violation before creating/mutating any `memfd` or cache entry (verified warm cache hits bypass extraction checks).

### Alternatives Rejected

- **Unconditionally charging `DefaultDecoderMaxMemory` (`512 MiB`):** Rejected because small containers (`128 MiB`–`512 MiB`) were unnecessarily blocked from launching payloads whose Zstd window is `<= 8 MiB`.
- **Setting `GOGC=off` unconditionally under `batch_etl`:** Rejected because disabling percentage GC when memory limits are unlimited risks unbounded heap growth (`v0.2.4`).

## Consequences & Tradeoffs

- **Assurance:** Core memory and overflow arithmetic (`ExtractionMemory`, `RetainedMemoryLimit`, `ZstdDecoderMemory`, `effective_memory_limit`) carries machine-checked proofs in Gobra (`LAW-CGROUP-01`..`03`), Z3 (`LAW-SMT-02`), Lean 4 (`LAW-LEAN-03`), Kani (`LAW-KANI-03`), and Verus (`LAW-VERUS-01`..`04`).
- **Limits:** `GOMEMLIMIT` remains a soft Go runtime target rather than an OS hard RSS cap and cannot eliminate container OOMs under rapid native/cgo allocation spikes.

## Rollback

Operators can bypass runtime tuning per process via `MICROFAT_AUTOTUNE=0`, leave Go CPU scheduling to Go via `MICROFAT_CPU_POLICY=native`, or override individual variables (`GOMEMLIMIT`, `GOMAXPROCS`, `GOGC`) directly in the environment.
