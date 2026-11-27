# ADR-0014: Promotion of Format v3 Block-Deduplicated Container Coexisting with Format v2

- **Status**: Accepted
- **Date**: 2026-11-27
- **Deciders**: Project Maintainer (@EpicBlackWolfZ)
- **Related Issues**: [#410](https://github.com/EpicBlackWolfZ/microfat/issues/410), [#500](https://github.com/EpicBlackWolfZ/microfat/issues/500), [#523](https://github.com/EpicBlackWolfZ/microfat/issues/523), [#526](https://github.com/EpicBlackWolfZ/microfat/issues/526), [#529](https://github.com/EpicBlackWolfZ/microfat/issues/529), [#530](https://github.com/EpicBlackWolfZ/microfat/issues/530), [#533](https://github.com/EpicBlackWolfZ/microfat/issues/533), [#537](https://github.com/EpicBlackWolfZ/microfat/issues/537)
- **Related Specs**: [SPEC Index](../specs/README.md) (`SPEC-0001`, `SPEC-0010`), [ADR-0001](../design/adr-0001-formal-verification-and-v3-lab.md), [ADR-0002](ADR-0002-format-v2-compact-binary-index-and-v1-removal.md)
- **Supersedes**: None (extends Format v2 with Format v3 coexistence)
- **Superseded By**: None

## Context

Format v2 stores each variant (`v1`..`v4` or `v8.0`..`v9.5`) as a separate contiguous compressed frame. Even with a 112 KiB shared Zstandard dictionary, standard Zstd windows cannot match identical long code/rodata sections located >128 KiB apart across 4+ static Go ELFs, nor share identical uncompressed chunks directly. Prior monolithic delta/CDC schemes (`#410`) achieved >5% savings only by reconstructing whole bases on disk/RAM, causing 1.5–3.2× cold slowdowns and 256 MiB OOM failures. [ADR-0001](../design/adr-0001-formal-verification-and-v3-lab.md) Track B designed a zero-base-materialization block-deduplicated layout with reversible ISA normalization.

## Decision

For `v0.5.0`, `microfat` promotes Format v3 (`IndexMagicV3 = "\x00\xFAM3"`, `FormatVersion3 = 3`) into production Go tooling (`#530`) and Rust stubs (`#533`–`#534`) while **coexisting with complete Format v2 support**:

1. **Zero-Base-Materialization Block Deduplication & ISA Filters (`#529`, `#530`):**
   - Retains the exact 56-byte EOF trailer (`\x00\xFA\x7FMICRO`) so Format v2 and Format v3 share unified trailer framing and outer integrity checking.
   - Splits each variant ELF into instruction-aligned FastCDC chunks backed by a deduplicated block pool compressed independently (permitting exact O(variant_size) memory limits without materializing unused variants) and fused L1-cache reversible ISA filters (`x86-64 BCJ+RIP` and `ARM64 BL/B/ADRP`) proven bijective in Lean 4 and Kani (`LAW-LEAN-04`, `LAW-KANI-04`).
   - Supports parallel `pwrite` block decompression directly into preallocated sealed `memfd` descriptors (with automatic single-threaded fallback under restrictive seccomp profiles, `#533`) and full `trim` compaction over shared block reference counts (`#529`, `#530`).
2. **Coexistence & Evidence-Gated Default (`#537`):**
   - `microfat` launchers (`minimal` and `full`), verifiers, and lifecycle commands support **both** Format v2 and Format v3 transparently via `--format-version 2|3`.
   - Whether `--format-version 3` or `2` is the default packer selection in the final `v0.5.0` release tag is strictly governed by native `amd64` and `arm64` paired benchmark and qualification gates (`#537`): Format v3 becomes default only when complete-cost size, cold `memfd`/`cache` startup latency, peak RSS, and pack cost win cleanly over `v0.4.1` Format v2 baselines without regressions; otherwise Format v2 remains default and Format v3 ships opt-in (`--format-version 3`).

### Alternatives Rejected

- **Dropping Format v2 in `v0.5.0` (Format v3 only):** Rejected to preserve seamless compatibility with existing `v0.2`–`v0.4` workflows, tools, and conservative latency profiles (`--compression lz4`/`none`).
- **Whole-file cross-variant delta encoding (`#410`):** Rejected because single-chunk random access and low peak RSS require independent chunk boundaries rather than whole-base decompression.

## Consequences & Tradeoffs

- **Deduplication vs Metadata Overhead:** Multi-variant Go binaries with substantial shared code/data compress significantly smaller under Format v3 while maintaining single-variant `memfd` streaming; single-variant ELFs can retain Format v2 cleanly without block-table overhead.
- **Formal & Conformance Obligations:** Requires frozen normative `SPEC-0010` plus Lean 4 (`FormatV3.lean`), Z3 (`format_v3_bounds.smt2`), Gobra (`v3_bounds.go`), and Kani/Verus proofs (`#529`, `#532`) before Go/Rust production code lands.

## Rollback

Format v2 (`--format-version 2`) remains fully supported across `microfat pack`, `verify`, `inspect`, `trim`, `optimize`, and both Rust launcher stubs (`minimal` and `full`). If Format v3 encounters any edge defect post-promotion, `--format-version 2` provides immediate build-time mitigation without downgrading toolchains or stubs.
