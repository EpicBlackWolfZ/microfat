# SPEC-0005: Packaging, Compression Codecs, Shared Zstd Dictionaries & ABI Consistency

- **Spec ID**: SPEC-0005
- **Status**: Normative
- **Date**: 2026-11-28
- **Related ADRs**: [`ADR-0001`](../design/adr-0001-formal-verification-and-v3-lab.md), [`ADR-0002`](../adr/ADR-0002-format-v2-compact-binary-index-and-v1-removal.md), [`ADR-0006`](../adr/ADR-0006-compression-engine-codec-policy-and-shared-dictionaries.md)
- **Related Issues**: [#410](https://github.com/EpicBlackWolfZ/microfat/issues/410), [#423](https://github.com/EpicBlackWolfZ/microfat/issues/423), [#527](https://github.com/EpicBlackWolfZ/microfat/issues/527), [#528](https://github.com/EpicBlackWolfZ/microfat/issues/528)
- **Verified By**: [`internal/pack`](../../internal/pack/pack_test.go), [`internal/codec`](../../internal/codec/codec_test.go), [`formal/lean/Microfat/ElfFilter.lean`](../../formal/lean/Microfat/ElfFilter.lean), [`tests/e2e/abi_consistency_e2e_test.go`](../../tests/e2e/abi_consistency_e2e_test.go), `LAW-LEAN-04`, `LAW-KANI-04`, `LAW-PACK-01`, `LAW-SMT-04`

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in RFC 2119.

## Scope & Terminology

This specification defines input binary validation, compression codec resolution, streaming decompression bounds, shared inter-variant Zstd dictionary training, reproducible build timestamps, and atomic packaging output guarantees in [`internal/pack`](../../internal/pack/pack.go) and [`internal/codec`](../../internal/codec/codec.go).

## Normative Requirements

- `REQ-0005-01`: Before writing any container output, `pack` SHALL inspect every variant binary via bounded `ReaderAt` ELF validation, asserting matching target OS/architecture across the stub and all variants, valid normalized microarchitecture levels with zero duplicates, and fail-closed rejection of mismatched Go compiler/ABI metadata unless explicitly overridden per ABI policy.
- `REQ-0005-02`: Codec resolution SHALL support `zstd`, `lz4`, and `none` with explicit per-variant overrides taking precedence over global `--compression`, deterministic canonical ordering, and rejection of unknown codecs (`ErrUnsupportedCodec`).
- `REQ-0005-03`: Both compression and decompression SHALL enforce `MaxPayloadSize` (2 GiB), `MaxZstdWindow` (`128 MiB`), exact `UncompressedSize` output byte caps (rejecting zip-bomb expansion cleanly with `ErrSizeMismatch`), and worker memory admission limits in [`internal/codec/admission.go`](../../internal/codec/admission.go).
- `REQ-0005-04`: When shared dictionary compression is enabled with at least two compatible Zstd variants meeting minimum size thresholds, `pack` SHALL train a deterministic bounded Zstd dictionary (`<= 1 MiB`), record its `DictionaryOffset`, `DictionarySize`, `DictionaryID`, and lowercase SHA-256 checksum in the Format v2 index, and abort if runtime verification detects dictionary mismatch (`ErrDictionaryCorrupted`).
- `REQ-0005-05`: Packaging SHALL support reproducible `SOURCE_DATE_EPOCH` timestamps and deterministic variant sorting so identical inputs, stub, and toolchain produce byte-identical fat binaries.
- `REQ-0005-06`: Output container publication SHALL write to a staging temporary file on the destination filesystem, flush buffers (`fsync`), preserve safe permissions, and publish via atomic rename so partial containers NEVER replace an existing target.

## Formal Laws & Invariants

- `LAW-LEAN-04`: Reversible x86-64 BCJ+RIP and ARM64 `BL`/`B`/`ADRP` pre-filter bijections and chunk-local boundary preservation with zero `sorryAx` ([`formal/lean/Microfat/ElfFilter.lean`](../../formal/lean/Microfat/ElfFilter.lean), `task verify-lean`).
- `LAW-KANI-04`: Bounded (`len <= 8`) symbolic bijection and chunk-split round-trip checks for Rust ELF filters ([`experiments/v3lab/rust-stub/src/elffilter.rs`](../../experiments/v3lab/rust-stub/src/elffilter.rs), `task verify-kani`).
- `LAW-PACK-01`: (*Scheduled [#528](https://github.com/EpicBlackWolfZ/microfat/issues/528)*) Variant count bounds, offset/size overflow safety, and atomic replacement guarantees in [`internal/pack/pack.go`](../../internal/pack/pack.go) (Gobra backfill target).
- `LAW-SMT-04`: (*Scheduled [#528](https://github.com/EpicBlackWolfZ/microfat/issues/528)*) Bounded LZ4/Zstd window & content-size arithmetic SMT proof (`lz4_zstd_bounds.smt2`, Z3 backfill target).

## Traceability Matrix

| ID | Description | Implementation | Verification (Tests / Proofs) |
| :--- | :--- | :--- | :--- |
| `REQ-0005-01` | Bounded ELF & ABI consistency validation | [`internal/pack/pack.go`](../../internal/pack/pack.go), [`internal/pack/abi.go`](../../internal/pack/abi.go) | [`tests/e2e/abi_consistency_e2e_test.go`](../../tests/e2e/abi_consistency_e2e_test.go), [`tests/e2e/elf_edge_cases_test.go`](../../tests/e2e/elf_edge_cases_test.go) |
| `REQ-0005-02` | Codec precedence (`none`/`lz4`/`zstd`) & rejection | [`cmd/microfat/variant_compression.go`](../../cmd/microfat/variant_compression.go), [`internal/codec/codec.go`](../../internal/codec/codec.go) | [`cmd/microfat/variant_compression_precedence_test.go`](../../cmd/microfat/variant_compression_precedence_test.go), [`tests/e2e/compression_matrix_test.go`](../../tests/e2e/compression_matrix_test.go) |
| `REQ-0005-03` | Bounded Zstd window (`<= 128 MiB`) & size limits | [`internal/codec/zstd.go`](../../internal/codec/zstd.go), [`internal/codec/admission.go`](../../internal/codec/admission.go) | [`internal/codec/zstd_window_test.go`](../../internal/codec/zstd_window_test.go), [`internal/codec/admission_test.go`](../../internal/codec/admission_test.go) |
| `REQ-0005-04` | Shared dictionary training & SHA-256 integrity | [`internal/pack/pack.go`](../../internal/pack/pack.go) | [`cmd/microfat/manifest_dictionary_test.go`](../../cmd/microfat/manifest_dictionary_test.go) |
| `REQ-0005-05` | Deterministic ordering & `SOURCE_DATE_EPOCH` | [`internal/pack/pack.go`](../../internal/pack/pack.go) | [`tests/e2e/reproducible_epoch_test.go`](../../tests/e2e/reproducible_epoch_test.go), [`cmd/microfat/source_date_epoch_test.go`](../../cmd/microfat/source_date_epoch_test.go) |
| `REQ-0005-06` | Staged `fsync` & atomic container publication | [`internal/pack/pack.go`](../../internal/pack/pack.go) | [`tests/e2e/input_safety_test.go`](../../tests/e2e/input_safety_test.go), [`tests/e2e/manifest_build_test.go`](../../tests/e2e/manifest_build_test.go) |
| `LAW-LEAN-04` | Lean 4 ELF pre-filter bijection proofs | [`formal/lean/Microfat/ElfFilter.lean`](../../formal/lean/Microfat/ElfFilter.lean) | `task verify-lean` |
| `LAW-KANI-04` | Kani Rust ELF pre-filter round-trip proof | [`experiments/v3lab/rust-stub/src/elffilter.rs`](../../experiments/v3lab/rust-stub/src/elffilter.rs) | `task verify-kani` |
| `LAW-PACK-01` | Pack overflow & replacement bounds (*#528 backfill*) | [`internal/pack/pack.go`](../../internal/pack/pack.go) | Tracked in [`#528`](https://github.com/EpicBlackWolfZ/microfat/issues/528) |
| `LAW-SMT-04` | Z3 `lz4_zstd_bounds.smt2` (*#528 backfill*) | Scheduled `formal/smt/lz4_zstd_bounds.smt2` | Tracked in [`#528`](https://github.com/EpicBlackWolfZ/microfat/issues/528) |
