# SPEC-0002: CPU Microarchitecture Probing & Dispatch Policy Precedence

- **Spec ID**: SPEC-0002
- **Status**: Normative
- **Date**: 2026-11-28
- **Related ADRs**: [`ADR-0001`](../design/adr-0001-formal-verification-and-v3-lab.md), [`ADR-0003`](../adr/ADR-0003-dual-tier-cpu-probing-and-arm64-dag-selection.md)
- **Related Issues**: [#523](https://github.com/EpicBlackWolfZ/microfat/issues/523), [#527](https://github.com/EpicBlackWolfZ/microfat/issues/527), [#528](https://github.com/EpicBlackWolfZ/microfat/issues/528)
- **Verified By**: [`internal/microarch`](../../internal/microarch/compatibility_test.go), [`internal/conformance`](../../internal/conformance/conformance_test.go), [`experiments/v3lab/rust-stub/src/microarch.rs`](../../experiments/v3lab/rust-stub/src/microarch.rs), [`formal/lean/Microfat/Microarch.lean`](../../formal/lean/Microfat/Microarch.lean), `LAW-LEAN-02`, `LAW-KANI-02`

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in RFC 2119.

## Scope & Terminology

This specification defines runtime CPU feature probing, microarchitecture level compatibility evaluation, non-linear ARM64 capability matching, Skylake-X/Cascade Lake AVX-512 downclock protection, and operator policy override precedence across Go ([`internal/microarch`](../../internal/microarch/microarch.go)) and Rust ([`experiments/v3lab/rust-stub/src/microarch.rs`](../../experiments/v3lab/rust-stub/src/microarch.rs), [`cpuid.rs`](../../experiments/v3lab/rust-stub/src/cpuid.rs)).

- **AMD64 Levels**: Total ordered levels `v1` < `v2` < `v3` < `v4` (`amd64_v1`..`amd64_v4`; aliases `x86_64` and `x86-64`).
- **ARM64 Levels**: Non-linear partial-order DAG spanning `v8.0`..`v8.9` and `v9.0`..`v9.5` (`arm64_v8.0`..`arm64_v9.5`).

## Normative Requirements

- `REQ-0002-01`: On AMD64 Linux, host capability detection SHALL treat native `CPUID` leaf instructions (`MOVBE`, `F16C`, `LZCNT`/`ABM`, `OSXSAVE`, AVX/AVX2/BMI/FMA, AVX-512F/BW/CD/DQ/VL) as the sole compatibility authority and SHALL NOT promote capabilities from `/proc/cpuinfo` flags or marketing strings (`SIGILL`-freedom).
- `REQ-0002-02`: On ARM64 Linux, host capability detection SHALL probe kernel-reported `AT_HWCAP` / `AT_HWCAP2` vectors and `/proc/self/auxv` conservatively, leaving unobservable architectural features (`nmi` in `v8.8`, `nmi`+`the` in `v8.9`) `false` so unforced selection NEVER selects an unverified target that could raise `SIGILL`.
- `REQ-0002-03`: Variant compatibility evaluation SHALL check exact required feature masks for every candidate variant independently (non-linear ARM64 DAG semantics: `v8.6` does not imply `v9.0`, nor does `v9.0` imply `v8.6`).
- `REQ-0002-04`: Dispatch selection policy precedence SHALL evaluate in strict order:
  1. `MICROFAT_FORCE_LEVEL` (MUST select that normalized level; MUST still satisfy host hardware compatibility unless forced failure occurs).
  2. `MICROFAT_MAX_LEVEL` (caps maximum rank/tier; combined with Skylake-X/Cascade Lake `safe_avx512` / `no_downclock` / `MICROFAT_AVX512_DOWNCLOCK_PROTECTION` capping `v4` to `v3` when `v3` is compatible, choosing the tighter cap).
  3. `MICROFAT_DISABLE_VARIANTS` (comma-separated blacklist excluded from selection).
  4. Highest compatible remaining variant in the index.
- `REQ-0002-05`: Normalized level parsing SHALL accept canonical and alias spellings case-insensitively with leading/trailing ASCII whitespace trimmed (e.g., `amd64_v3` -> `v3`, `arm64_v8.2` -> `v8.2`) and reject unknown levels deterministically.
- `REQ-0002-06`: No-compatible-variant selection SHALL fail closed before extraction or `execve` and emit diagnostic telemetry identifying the host level and policy overrides applied.

## Formal Laws & Invariants

- `LAW-LEAN-02`: AMD64 feature monotonicity, ARM64 DAG acyclicity, unforced optimality, and hardware `SIGILL`-freedom ([`formal/lean/Microfat/Microarch.lean`](../../formal/lean/Microfat/Microarch.lean), `task verify-lean`).
- `LAW-KANI-02`: Exhaustive symbolic proof over `2^21` AMD64 and `2^24` ARM64 feature combinations proving `SIGILL`-free selection and policy precedence ([`experiments/v3lab/rust-stub/src/microarch.rs`](../../experiments/v3lab/rust-stub/src/microarch.rs), `task verify-kani`).
- `LAW-SMT-03`: (*Scheduled [#528](https://github.com/EpicBlackWolfZ/microfat/issues/528)*) Bit-vector SMT encoding (`microarch_dag.smt2`) of non-linear ARM64 `v8.x`/`v9.x` partial order and policy precedence (Z3 backfill target).
- `LAW-MICROARCH-01`: (*Scheduled [#528](https://github.com/EpicBlackWolfZ/microfat/issues/528)*) Go selector contract & compatibility table verification in [`internal/microarch`](../../internal/microarch/microarch.go) (Gobra / qualification backfill target).

## Traceability Matrix

| ID | Description | Implementation | Verification (Tests / Proofs) |
| :--- | :--- | :--- | :--- |
| `REQ-0002-01` | AMD64 CPUID-only authoritative probing | [`internal/microarch/cpuid_amd64.go`](../../internal/microarch/cpuid_amd64.go), [`experiments/v3lab/rust-stub/src/cpuid.rs`](../../experiments/v3lab/rust-stub/src/cpuid.rs) | [`internal/microarch/amd64_contract_test.go`](../../internal/microarch/amd64_contract_test.go), [`tests/e2e/amd64_masked_test.go`](../../tests/e2e/amd64_masked_test.go) |
| `REQ-0002-02` | ARM64 conservative `auxv` probing (`v8.8`/`v8.9` safe) | [`internal/microarch/microarch.go`](../../internal/microarch/microarch.go), [`experiments/v3lab/rust-stub/src/cpuid.rs`](../../experiments/v3lab/rust-stub/src/cpuid.rs) | [`internal/microarch/arm64_fixtures_test.go`](../../internal/microarch/arm64_fixtures_test.go), [`tests/e2e/arm64_detect_test.go`](../../tests/e2e/arm64_detect_test.go) |
| `REQ-0002-03` | Non-linear ARM64 DAG requirement checking | [`internal/microarch/microarch.go`](../../internal/microarch/microarch.go), [`experiments/v3lab/rust-stub/src/microarch.rs`](../../experiments/v3lab/rust-stub/src/microarch.rs) | [`internal/microarch/compatibility_test.go`](../../internal/microarch/compatibility_test.go), `TestLeanConformance_Microarch` |
| `REQ-0002-04` | Policy override precedence & AVX-512 protection | [`internal/microarch/microarch.go`](../../internal/microarch/microarch.go), [`experiments/v3lab/rust-stub/src/main.rs`](../../experiments/v3lab/rust-stub/src/main.rs) | [`cmd/microfat-stub/cpu_policy_info_test.go`](../../cmd/microfat-stub/cpu_policy_info_test.go), [`tests/e2e/meta_selection_test.go`](../../tests/e2e/meta_selection_test.go) |
| `REQ-0002-05` | Case/whitespace-insensitive level normalization | [`internal/microarch/microarch.go`](../../internal/microarch/microarch.go) | [`internal/microarch/microarch_edge_test.go`](../../internal/microarch/microarch_edge_test.go), [`internal/microarch/microarch_fuzz_test.go`](../../internal/microarch/microarch_fuzz_test.go) |
| `REQ-0002-06` | Fail-closed when zero compatible variants exist | [`cmd/microfat-stub/selection_diagnostics_test.go`](../../cmd/microfat-stub/selection_diagnostics_test.go) | [`tests/e2e/rust_stub_parity_test.go`](../../tests/e2e/rust_stub_parity_test.go) |
| `LAW-LEAN-02` | Lean 4 AMD64/ARM64 DAG & policy proof | [`formal/lean/Microfat/Microarch.lean`](../../formal/lean/Microfat/Microarch.lean) | `task verify-lean` |
| `LAW-KANI-02` | Kani exhaustive mask & policy verification | [`experiments/v3lab/rust-stub/src/microarch.rs`](../../experiments/v3lab/rust-stub/src/microarch.rs) | `task verify-kani` |
| `LAW-SMT-03` | Z3 `microarch_dag.smt2` (*#528 backfill*) | Scheduled `formal/smt/microarch_dag.smt2` | Tracked in [`#528`](https://github.com/EpicBlackWolfZ/microfat/issues/528) |
| `LAW-MICROARCH-01` | Go selector contract (*#528 backfill*) | [`internal/microarch/microarch.go`](../../internal/microarch/microarch.go) | Tracked in [`#528`](https://github.com/EpicBlackWolfZ/microfat/issues/528) |
