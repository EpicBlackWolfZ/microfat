# ADR-0013: Promotion of Native Rust Launcher Stubs (`minimal` Default + `full`) & Go Stub Retirement

- **Status**: Accepted
- **Date**: 2026-11-27
- **Deciders**: Project Maintainer (@EpicBlackWolfZ)
- **Related Issues**: [#497](https://github.com/EpicBlackWolfZ/microfat/issues/497), [#523](https://github.com/EpicBlackWolfZ/microfat/issues/523), [#525](https://github.com/EpicBlackWolfZ/microfat/issues/525), [#526](https://github.com/EpicBlackWolfZ/microfat/issues/526), [#531](https://github.com/EpicBlackWolfZ/microfat/issues/531), [#532](https://github.com/EpicBlackWolfZ/microfat/issues/532), [#533](https://github.com/EpicBlackWolfZ/microfat/issues/533), [#534](https://github.com/EpicBlackWolfZ/microfat/issues/534), [#535](https://github.com/EpicBlackWolfZ/microfat/issues/535), [#536](https://github.com/EpicBlackWolfZ/microfat/issues/536)
- **Related Specs**: [SPEC Index](../specs/README.md) (`SPEC-0002`, `SPEC-0003`, `SPEC-0004`, `SPEC-0010`), [ADR-0001](../design/adr-0001-formal-verification-and-v3-lab.md), [Verification Ledger](../verification/README.md)
- **Supersedes**: Legacy Go launcher stub (`cmd/microfat-stub` retired in `#535` once full parity passes)
- **Superseded By**: None

## Context

In `v0.4.1`, the Go launcher stub (`cmd/microfat-stub`) imposed a ~3.77–3.92 MiB static binary floor on every fat executable because it linked the full Go runtime just to inspect `MICROFAT_*` env vars, parse the 56-byte trailer + binary index, probe `CPUID`/`AT_HWCAP`/cgroups, decompress payloads, and call `execve`. [ADR-0001](../design/adr-0001-formal-verification-and-v3-lab.md) Track B prototyped `microfat-stub-rs` under `experiments/v3lab/rust-stub/` targeting ~150 KiB static musl binaries while passing security, chaos, cgroup, and Lean-conformance parity checks.

While cold `zstd` Format v2 measured +2.86–4.22 ms slower than Go's hand-tuned assembly decoder in v3lab trials, the native Rust stub yielded a **-33.3% fat binary size reduction** and a **-19.6% `warm-auto` startup improvement**, and Format v3 parallel block decoding mitigates cold decompression further. Moreover, keeping two separate production stubs (Go + Rust) long-term would double maintenance, vulnerability, and qualification overhead.

## Decision

For `v0.5.0`, `microfat` promotes the native Rust launcher stub to production and retires `cmd/microfat-stub`:

1. **Modular Cargo Workspace (`rust/`):** Promote `experiments/v3lab/rust-stub/` into a pinned Cargo workspace (`#531`) containing `#![no_std]` `#![forbid(unsafe_code)]` core logic (`microfat-stub-core`), Linux syscall/runtime primitives (`microfat-stub-runtime`), and two binary profiles:
   - **`microfat-stub-minimal` (New Default Profile):** Static musl minimal launcher stub (~150 KiB) selected by default in `microfat pack` (`--stub-profile=minimal`), supporting full Format v2 + v3 execution, cgroup autotuning, sealed `memfd`, cache-first dispatch (with `ST_NOEXEC` detection), telemetry, and refusing interactive `--microfat:*` meta-commands (`#533`, `#535`).
   - **`microfat-stub` (`--stub-profile=full`):** Native `#![no_std]` Rust full troubleshooting stub implementing `--microfat:help`, `--microfat:info` (text + JSON), `--microfat:prewarm`, `--microfat:trim`, `--microfat:optimize`, `--microfat:optimize-to`, and `--microfat:specialize` across Format v2 and v3 (`#534`).
2. **Stub Provenance Note & Dual-Era Transition:** Embed a `.note.microfat.stub` ELF note identifying stub profile/format capabilities (`#531`), validate Rust stubs cleanly in `microfat pack` (`#518`), execute atomic switch + Go stub retirement in `#535` once full Go E2E parity passes, and ship reproducible `x86_64`/`aarch64` musl builds with CycloneDX 1.7 + SPDX 3.0.1 Cargo inventories and `cargo-audit`/`cargo-deny` (`#536`).
3. **Mandatory Multi-Toolchain Verification:** Enforce `clippy --all-targets -D warnings` (native + `aarch64-musl`), `>= 95%` line coverage (`cargo-llvm-cov`), Kani model checking, **Verus** inductive proofs (`#525`, `#532`), NIST CAVP SHA-256 (`#544`), and Lean 4 cross-language conformance vectors.

### Alternatives Rejected

- **Retaining Go `cmd/microfat-stub` as default indefinitely alongside an opt-in Rust stub:** Rejected because leaving Go stub as default forfeits the primary ~3.6 MiB size reduction and warm-cache speedup while forcing dual stub maintenance across releases.
- **Shipping only a `minimal` Rust stub and dropping `--microfat:*` meta-commands:** Rejected because standalone deployments without `microfat` CLI installed depend on `--microfat:info`, `--microfat:trim`, `--microfat:optimize`, and `--microfat:prewarm` for field troubleshooting.

## Consequences & Tradeoffs

- **Size vs Cold Format v2 Zstd Speed:** Fat binaries drop by ~3.6 MiB cleanly; cold Format v2 `zstd` launches trade ~3–4 ms (accepted in `#523`), mitigated by cache-first warm runs and Format v3 parallel block decoding.
- **Toolchain Scope:** CI and release workflows require pinned Rust (`RUST_VERSION` in `.config/tool-versions.env`), `aarch64-unknown-linux-musl` cross targets, Kani, Verus, `cargo-audit`, `cargo-deny`, and `cargo-llvm-cov` (`task lint-rust test-rust coverage-rust vuln-rust verify-kani verify-verus`).

## Rollback

Until `#535` lands atomically in Phase 4, `cmd/microfat-stub` remains untouched and functional; after `#535`, operators needing legacy `v0.4.x` Go stub behavior for specific workloads can select `--stub <path/to/v0.4.1/microfat-stub>` or re-enable `--stub-profile=full` for troubleshooting flows, and release archives remain cryptographically immutable per tag.
