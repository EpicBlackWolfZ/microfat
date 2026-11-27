# ADR-0006: Compression Engine, Codec Policy & Shared Inter-Variant Zstd Dictionaries

- **Status**: Accepted (Retroactive — v0.2–v0.4)
- **Date**: 2026-11-27
- **Deciders**: Project Maintainer (@EpicBlackWolfZ)
- **Related Issues**: [#410](https://github.com/EpicBlackWolfZ/microfat/issues/410), [#415](https://github.com/EpicBlackWolfZ/microfat/pull/415), [#416](https://github.com/EpicBlackWolfZ/microfat/pull/416), [#423](https://github.com/EpicBlackWolfZ/microfat/issues/423), [#519](https://github.com/EpicBlackWolfZ/microfat/issues/519), [#523](https://github.com/EpicBlackWolfZ/microfat/issues/523), [#526](https://github.com/EpicBlackWolfZ/microfat/issues/526)
- **Related Specs**: [SPEC Index](../specs/README.md) (`SPEC-0005`), [Architecture Specification](../architecture.md#4-shared-inter-variant-dictionary-mechanics---dict), [Zstd Profitability Analysis](../zstd-frame-and-dict-profitability.md)
- **Supersedes**: None
- **Superseded By**: None ([ADR-0014](ADR-0014-format-v3-block-deduplicated-container-with-v2-coexistence.md) extends codec options for Format v3)

## Context

Packaging 2–4+ ELF variants per binary magnifies artifact size quickly, especially across statically linked Go binaries sharing large runtime/stdlib regions. Conversely, high-ratio compression can penalize cold `memfd` startup latency or memory usage during parallel `microfat pack`. Historical delta/CDC container experiments in `#410` slowed cold reconstruction by 1.5–3.2× and OOMed at 256 MiB container caps, requiring a predictable, fast, memory-bounded codec policy for Format v2.

## Decision

`microfat` standardizes a deterministic multi-codec compression engine in `internal/codec/` and `internal/pack/`:

1. **Supported Codecs & Exact Bounds:** Supports `zstd` (single-shot for small payloads, window-capped at `128 MiB` for launcher compatibility), `lz4` (with bounded block concurrency), and `none`, enforcing exact declared `UncompressedSize` checks (including zero-size payloads) on decode (`ErrSizeMismatch`, `ErrWindowSizeExceeded`).
2. **Presets & Per-Variant Overrides (`v0.4.0`):** Offers `latency`, `balanced`, and `size` profiles alongside `--compression <codec>[:level]` and repeatable `--variant-compression <level>=<codec>[:level]` (with inline explicit variant specs outranking root flags).
3. **Shared Inter-Variant Zstd Dictionaries (`--dict`, `--dict-profitable`):** Trains a bounded (up to 112 KiB) Zstandard dictionary across bounded input samples when explicitly requested (`--dict`, skipped automatically if no variant codec can use a dictionary per `#363`), or only when complete-package bytes strictly shrink under `--dict-profitable` (extended to `--profile size` gating in `#519`).
4. **Parallel Pack Admission (`#415`, `#423`):** Hashes and compresses multiple variants concurrently when effective container CPU and memory budgets permit multiple workers, falling back cleanly to serial execution under tight caps while writing segments in canonical deterministic order (`SOURCE_DATE_EPOCH`).

### Alternatives Rejected

- **Replacing independent Format v2 frames with monolithic base+delta materialization (`#410`):** Rejected for Format v2 because matched cold reconstruction exceeded time and 256 MiB OOM budgets on unstripped binaries (re-solved with block-independent deduplication in Format v3 per [ADR-0014](ADR-0014-format-v3-block-deduplicated-container-with-v2-coexistence.md)).
- **Enabling shared dictionaries unconditionally when payloads are nearly identical or incompressible:** Rejected when total binary bytes increase relative to no-dict frames (`zstd-frame-and-dict-profitability.md`).

## Consequences & Tradeoffs

- **Reproducibility & Predictability:** Deterministic canonical variant ordering and explicit seed/epoch propagation produce byte-identical packages given identical inputs and toolchain pins.
- **Resource Limits:** Even single-worker packing of large multi-variant ELFs can exceed tight 512 MiB finite container memory caps (`#423`), requiring workload-level sizing when packing huge unstripped tools.

## Rollback

Operators can pin any individual variant or package to `--compression lz4:fastest`, `--compression none`, or omit `--dict` without changing downstream stub compatibility or Format v2 readers.
