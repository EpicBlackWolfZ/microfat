# SPEC-0004: Cgroup Discovery, Extraction Memory Budgets & Go Runtime Autotuning

- **Spec ID**: SPEC-0004
- **Status**: Normative
- **Date**: 2026-11-28
- **Related ADRs**: [`ADR-0001`](../design/adr-0001-formal-verification-and-v3-lab.md), [`ADR-0005`](../adr/ADR-0005-cgroup-autotuning-and-extraction-memory-budgets.md), [`ADR-0011`](../adr/ADR-0011-observability-runtimeinfo-and-benchmark-methodology.md)
- **Related Issues**: [#501](https://github.com/EpicBlackWolfZ/microfat/issues/501), [#516](https://github.com/EpicBlackWolfZ/microfat/issues/516), [#525](https://github.com/EpicBlackWolfZ/microfat/issues/525), [#527](https://github.com/EpicBlackWolfZ/microfat/issues/527)
- **Verified By**: [`internal/cgroup`](../../internal/cgroup/cgroup_test.go), [`runtimeinit`](../../runtimeinit/runtimeinit_test.go), [`formal/lean/Microfat/CgroupMath.lean`](../../formal/lean/Microfat/CgroupMath.lean), [`formal/smt/cgroup_memory.smt2`](../../formal/smt/cgroup_memory.smt2), `LAW-CGROUP-*`, `LAW-SMT-02`, `LAW-LEAN-03`, `LAW-KANI-03`, `LAW-VERUS-*`

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in RFC 2119.

## Scope & Terminology

This specification governs Linux cgroup v1/v2 container limit discovery, extraction memory admission budgets, and automatic Go runtime tuning (`GOMEMLIMIT`, `GOMAXPROCS`, `GOGC`) across [`internal/cgroup`](../../internal/cgroup/cgroup.go), [`cmd/microfat-stub`](../../cmd/microfat-stub/runtime_tuning_linux.go), [`experiments/v3lab/rust-stub/src/cgroup.rs`](../../experiments/v3lab/rust-stub/src/cgroup.rs), [`runtimeinit`](../../runtimeinit/runtimeinit.go), and [`runtimeinfo`](../../runtimeinfo/runtimeinfo.go).

## Normative Requirements

- `REQ-0004-01`: Cgroup discovery SHALL inspect `/proc/self/cgroup` and walk ancestor controllers bounded under `/sys/fs/cgroup`, computing the tightest finite `effectiveMemoryLimit = min(memory.max, memory.high)` on cgroup v2 or `memory.limit_in_bytes` on cgroup v1 while treating `"max"` and v1 sentinel thresholds (`>= 1 << 50`) as unlimited.
- `REQ-0004-02`: Prior to cold decompression, the launcher SHALL compute window-accurate extraction memory (`payload + dict + decoderWindow + headroom`, rejecting any Zstd frame window above `128 MiB` or decoder memory above `264 MiB`) and SHALL fail closed with `ErrMemoryBudget` before creating a `memfd` if the estimate exceeds any finite container ceiling.
- `REQ-0004-03`: When computing child `GOMEMLIMIT`, `memfd` execution SHALL subtract page-aligned retained binary memory (`retained = align_up(uncompressedSize, pageSize)`) from the container ceiling via `RetainedMemoryLimit(ceiling, retained)` prior to applying the profile ratio (default 90%) and capped headroom guard (`[8 MiB, 128 MiB]`). Cached file execution uses `retained = 0`.
- `REQ-0004-04`: `GOMAXPROCS` SHALL default to host-responsive `default` CPU policy and SHALL ONLY inject `GOMAXPROCS = max(1, floor(quota / period))` under explicit `MICROFAT_CPU_POLICY=static`. Caller-supplied `GOMEMLIMIT`, `GOMAXPROCS`, and `GOGC` environment variables SHALL NEVER be overwritten, and `MICROFAT_AUTOTUNE=0` / `false` or `MICROFAT_DRY_RUN=1` / `true` SHALL suppress child env mutation while still emitting `MICROFAT_CGROUP_*` diagnostics.
- `REQ-0004-05`: `MICROFAT_GC_PROFILE` SHALL support `default`, `low_latency`, `throughput`, and `batch_etl`, skipping `batch_etl` `GOGC` optimization cleanly with `MICROFAT_CGROUP_GOGC_SKIPPED_REASON` when no finite memory ceiling exists.
- `REQ-0004-06`: [`runtimeinit`](../../runtimeinit/runtimeinit.go) SHALL support standalone Go applications running outside a stub (via [`runtimeinit/autoload`](../../runtimeinit/autoload/autoload.go) or explicit API), and [`runtimeinfo`](../../runtimeinfo/runtimeinfo.go) SHALL expose read-only parsed `MICROFAT_*` runtime state without performing side effects.

## Formal Laws & Invariants

- `LAW-CGROUP-01`: (`ExtractionMemory`) Checks nonnegativity and `int64` overflow freedom across payload, dictionary, decoder, and headroom terms (Gobra proof, `task verify-cgroup-memory`).
- `LAW-CGROUP-02`: (`RetainedMemoryLimit`) Enforces exact `limit - retained` when `0 <= retained < limit`, else `0`, without overflow/underflow (Gobra proof, `task verify-cgroup-memory`; mutant check).
- `LAW-CGROUP-03`: (`ZstdDecoderMemory`) Enforces `2*window + overhead` bounded by `maxWindow` and `maxDecoder` without overflow (Gobra proof, `task verify-cgroup-memory`).
- `LAW-SMT-02`: Exhaustive `QF_BV` SMT proof of cgroup memory and `ZstdDecoderMemory` overflow/monotonicity guards ([`formal/smt/cgroup_memory.smt2`](../../formal/smt/cgroup_memory.smt2), `task verify-z3`).
- `LAW-LEAN-03`: Overflow guards, `GOMEMLIMIT` monotonicity, worker admission sum, and `zstdDecoderMemory` proof with zero `sorryAx` ([`formal/lean/Microfat/CgroupMath.lean`](../../formal/lean/Microfat/CgroupMath.lean), `task verify-lean`).
- `LAW-KANI-03`: Symbolic bounds/overflow proof for Rust `extraction_memory`, `retained_memory_limit`, `zstd_decoder_memory`, `GOMEMLIMIT`, and `GOMAXPROCS` ([`experiments/v3lab/rust-stub/src/cgroup.rs`](../../experiments/v3lab/rust-stub/src/cgroup.rs), `task verify-kani`).
- `LAW-VERUS-01`: Verus unbounded `int` equivalence and `i64` overflow-freedom for `extraction_memory` ([`experiments/v3lab/rust-stub/src/cgroup.rs`](../../experiments/v3lab/rust-stub/src/cgroup.rs), `task verify-verus`).
- `LAW-VERUS-02`: Verus exact subtraction proof for `retained_memory_limit` with automated CI mutant rejection (`task verify-verus`).
- `LAW-VERUS-03`: Verus bounds proof for `zstd_decoder_memory` (`task verify-verus`).
- `LAW-VERUS-04`: Verus `effective_memory_limit` exact minimum proof across optional `memory.max` and `memory.high` limits (`task verify-verus`).

## Traceability Matrix

| ID | Description | Implementation | Verification (Tests / Proofs) |
| :--- | :--- | :--- | :--- |
| `REQ-0004-01` | Cgroup v1/v2 hierarchy & tightest ceiling walk | [`internal/cgroup/cgroup.go`](../../internal/cgroup/cgroup.go), [`internal/cgroup/proc.go`](../../internal/cgroup/proc.go) | [`internal/cgroup/v1_detection_test.go`](../../internal/cgroup/v1_detection_test.go), [`internal/cgroup/proc_test.go`](../../internal/cgroup/proc_test.go) |
| `REQ-0004-02` | Window-accurate extraction memory admission budget | [`internal/cgroup/memory.go`](../../internal/cgroup/memory.go), [`internal/codec/zstd.go`](../../internal/codec/zstd.go) | [`cmd/microfat-stub/memory_fallback_linux_test.go`](../../cmd/microfat-stub/memory_fallback_linux_test.go), [`cmd/microfat-stub/memory_linux_test.go`](../../cmd/microfat-stub/memory_linux_test.go) |
| `REQ-0004-03` | Retained `memfd` memory subtraction & `GOMEMLIMIT` | [`internal/cgroup/cgroup.go`](../../internal/cgroup/cgroup.go), [`experiments/v3lab/rust-stub/src/cgroup.rs`](../../experiments/v3lab/rust-stub/src/cgroup.rs) | [`cmd/microfat-stub/cgroup_boundaries_linux_test.go`](../../cmd/microfat-stub/cgroup_boundaries_linux_test.go), `TestLeanConformance_CgroupMath` |
| `REQ-0004-04` | Static CPU policy & ambient variable preservation | [`internal/cgroup/cpu_policy.go`](../../internal/cgroup/cpu_policy.go) | [`internal/cgroup/cpu_policy_test.go`](../../internal/cgroup/cpu_policy_test.go), [`tests/e2e/cpu_quota_test.go`](../../tests/e2e/cpu_quota_test.go) |
| `REQ-0004-05` | `MICROFAT_GC_PROFILE` & `batch_etl` safety | [`internal/cgroup/batch_gc.go`](../../internal/cgroup/batch_gc.go) | [`internal/cgroup/batch_gc_test.go`](../../internal/cgroup/batch_gc_test.go), [`cmd/microfat-stub/batch_gc_linux_test.go`](../../cmd/microfat-stub/batch_gc_linux_test.go) |
| `REQ-0004-06` | `runtimeinit` & `runtimeinfo` SDK contracts | [`runtimeinit/runtimeinit.go`](../../runtimeinit/runtimeinit.go), [`runtimeinfo/runtimeinfo.go`](../../runtimeinfo/runtimeinfo.go) | [`runtimeinit/memory_test.go`](../../runtimeinit/memory_test.go), [`runtimeinfo/runtimeinfo_test.go`](../../runtimeinfo/runtimeinfo_test.go) |
| `LAW-CGROUP-01` | Gobra `ExtractionMemory` overflow freedom | [`internal/cgroup/memory.go`](../../internal/cgroup/memory.go) | `task verify-cgroup-memory` |
| `LAW-CGROUP-02` | Gobra `RetainedMemoryLimit` exactness & mutant | [`internal/cgroup/memory.go`](../../internal/cgroup/memory.go) | `task verify-cgroup-memory` |
| `LAW-CGROUP-03` | Gobra `ZstdDecoderMemory` bounds proof | [`internal/cgroup/memory.go`](../../internal/cgroup/memory.go) | `task verify-cgroup-memory` |
| `LAW-SMT-02` | Z3 cgroup & decoder memory bounds proof | [`formal/smt/cgroup_memory.smt2`](../../formal/smt/cgroup_memory.smt2) | `task verify-z3` |
| `LAW-LEAN-03` | Lean 4 `CgroupMath` formal proof & vectors | [`formal/lean/Microfat/CgroupMath.lean`](../../formal/lean/Microfat/CgroupMath.lean) | `task verify-lean` |
| `LAW-KANI-03` | Kani cgroup & runtime tuning symbolic check | [`experiments/v3lab/rust-stub/src/cgroup.rs`](../../experiments/v3lab/rust-stub/src/cgroup.rs) | `task verify-kani` |
| `LAW-VERUS-01` | Verus `extraction_memory` contract | [`experiments/v3lab/rust-stub/src/cgroup.rs`](../../experiments/v3lab/rust-stub/src/cgroup.rs) | `task verify-verus` |
| `LAW-VERUS-02` | Verus `retained_memory_limit` & mutant check | [`experiments/v3lab/rust-stub/src/cgroup.rs`](../../experiments/v3lab/rust-stub/src/cgroup.rs) | `task verify-verus` |
| `LAW-VERUS-03` | Verus `zstd_decoder_memory` contract | [`experiments/v3lab/rust-stub/src/cgroup.rs`](../../experiments/v3lab/rust-stub/src/cgroup.rs) | `task verify-verus` |
| `LAW-VERUS-04` | Verus `effective_memory_limit` minimum proof | [`experiments/v3lab/rust-stub/src/cgroup.rs`](../../experiments/v3lab/rust-stub/src/cgroup.rs) | `task verify-verus` |
