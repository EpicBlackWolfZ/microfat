# ADR-0003: Dual-Tier CPU Probing & Non-Linear ARM64 DAG Selection Policy

- **Status**: Accepted (Retroactive — v0.1–v0.3)
- **Date**: 2026-11-27
- **Deciders**: Project Maintainer (@EpicBlackWolfZ)
- **Related Issues**: [#523](https://github.com/EpicBlackWolfZ/microfat/issues/523), [#526](https://github.com/EpicBlackWolfZ/microfat/issues/526)
- **Related Specs**: [SPEC Index](../specs/README.md) (`SPEC-0002`), [Architecture Specification](../architecture.md#8-arm64-aarch64-microarchitecture-matrix)
- **Supersedes**: None
- **Superseded By**: None

## Context

`microfat` embeds multiple microarchitecture-specialized ELF variants inside a single binary targeting Linux `amd64` (`v1`–`v4`) and `arm64` (`v8.0`–`v9.5`). Selecting an incompatible variant produces immediate `SIGILL` crashes in production, whereas conservative fallback to baseline wastes ISA gains. Crucially, `GOAMD64=v2+` requires `LAHF`/`SAHF` in addition to `POPCNT`/`SSSE3`/`SSE4.x`, Intel Skylake-X / Cascade Lake Xeons suffer thermal/frequency downclocking under heavy 512-bit AVX-512 execution, and `ARM64` `v9.x` is a non-linear feature DAG where higher version strings (such as `v9.0` vs `v8.6` `BF16`/`I8MM` or `v8.8`/`v8.9` with unobservable `NMI`/`THE`) do **not** form a total linear order.

## Decision

Variant probing and selection follow a deterministic, fail-safe dual-tier engine implemented in `internal/microarch/` and mirrored in Rust:

1. **Direct Host Capability Probing:**
   - **Linux `amd64`:** Probes `CPUID` and OS-enabled `XGETBV` (`XMM`, `YMM`, and AVX-512 `opmask`/`ZMM` state) to classify `v1`..`v4` strictly, enforcing `LAHF`/`SAHF` for `>= v2` (as documented in `v0.2.4`).
   - **Linux `arm64`:** Reads kernel auxiliary vector bits (`AT_HWCAP`, `AT_HWCAP2`, including `MOPS`, `HBC`, and `GCS` added in `v0.3.0`) over the non-linear `v8.x` / `v9.x` dependency DAG rather than linear string comparison. Variants requiring features unobservable from userspace (such as `NMI`/`THE` on `v8.8` and `v8.9`) are never auto-selected without compatible fallbacks.
2. **Strict Selection Precedence & Fail-Closed Overrides:** Operators control selection using a strict precedence chain evaluated against compatible host capabilities:
   - `MICROFAT_FORCE_LEVEL` (fails fast if unsupported by host or missing from manifest),
   - `MICROFAT_MAX_LEVEL` (caps maximum tier),
   - `MICROFAT_DISABLE_VARIANTS` (excludes listed variants),
   - `MICROFAT_POLICY` (`safe_avx512`, `no_downclock`) and `MICROFAT_AVX512_DOWNCLOCK_PROTECTION=1` (caps AVX-512 to `v3` on affected Skylake-X / Cascade Lake CPUs while recording `MICROFAT_POLICY_APPLIED` and `MICROFAT_OVERRIDE_REASON`),
   - Highest compatible embedded variant by DAG/tier rank.
3. **Unforced Hardware-Safe Selection:** When unforced, selection picks the highest hardware-compatible non-disabled variant (`SIGILL`-freedom) and emits exact diagnostics (`ErrIncompatibleVariant` when none match).

### Alternatives Rejected

- **Linear version ordering for ARM64 `v8.0`..`v9.5`:** Rejected because `v9.0` (SVE2) omits features present in `v8.6` (`BF16`/`I8MM`) or later `v8.x` revisions; linear comparison causes `SIGILL` traps on real silicon.
- **Selecting `v2`–`v4` using `/proc/cpuinfo` string matching alone:** Rejected due to parser fragility across kernels/containers and missing `XGETBV` OS-XSAVE validation.

## Consequences & Tradeoffs

- **Correctness & Safety:** Eliminates `SIGILL` from missing OS state (`XGETBV`) or non-linear ARM64 extensions while allowing safe Skylake-X/Cascade Lake workloads to avoid AVX-512 frequency drops.
- **Verification:** Selection DAG acyclicity, `SIGILL`-freedom, AMD64 monotonicity, and precedence laws are proven in Lean 4 (`LAW-LEAN-02`) and checked exhaustively across `2^21` AMD64 and `2^24` ARM64 feature masks via Kani (`LAW-KANI-02`), with cross-language conformance vectors verified in Go and Rust.

## Rollback

Operators hitting unexpected host overrides or CPU quirk policies can immediately pin any baseline tier with `MICROFAT_FORCE_LEVEL=v1` (or `v8.0`) or cap selection with `MICROFAT_MAX_LEVEL` without rebuilding or re-packing binaries.
