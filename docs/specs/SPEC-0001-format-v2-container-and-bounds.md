# SPEC-0001: Format v2 Container, 56-Byte Trailer, Binary Index & Bounds Validation

- **Spec ID**: SPEC-0001
- **Status**: Normative
- **Date**: 2026-11-28
- **Related ADRs**: [`ADR-0001`](../design/adr-0001-formal-verification-and-v3-lab.md), [`ADR-0002`](../adr/ADR-0002-format-v2-compact-binary-index-and-v1-removal.md), [`ADR-0014`](../adr/ADR-0014-format-v3-block-deduplicated-container-with-v2-coexistence.md)
- **Related Issues**: [#427](https://github.com/EpicBlackWolfZ/microfat/issues/427), [#468](https://github.com/EpicBlackWolfZ/microfat/issues/468), [#527](https://github.com/EpicBlackWolfZ/microfat/issues/527), [#528](https://github.com/EpicBlackWolfZ/microfat/issues/528)
- **Verified By**: [`internal/format`](../../internal/format/format_test.go), [`internal/conformance`](../../internal/conformance/conformance_test.go), [`formal/lean/Microfat/FormatV2.lean`](../../formal/lean/Microfat/FormatV2.lean), [`formal/smt/format_v2_bounds.smt2`](../../formal/smt/format_v2_bounds.smt2), `LAW-FORMAT-*`, `LAW-SMT-01`, `LAW-LEAN-01`, `LAW-KANI-01`

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in RFC 2119.

## Scope & Terminology

This specification defines the physical byte layout, EOF trailer structure, Format v2 binary index table encoding, bounds invariants, and integrity validation rules for `microfat` fat executables produced by [`internal/pack`](../../internal/pack/pack.go) and parsed by [`internal/format`](../../internal/format/format.go) and [`experiments/v3lab/rust-stub/src/format.rs`](../../experiments/v3lab/rust-stub/src/format.rs).

- **EOF Trailer**: Fixed 56-byte footer located at `[fileSize - 56, fileSize)` containing `indexOffset` (`uint64` LE), `indexSize` (`uint64` LE), `indexSHA256` (`[32]byte`), and `Magic` (`"\x00\xFA\x7FMICRO"`).
- **Binary Index Table**: Format v2 compact binary payload (`IndexMagic = "MFAT"`) located immediately before the trailer (`indexOffset + indexSize == fileSize - 56`).
- **Format v1 (Removed)**: Legacy JSON index format (`{` prefix); permanently rejected per [`ADR-0002`](../adr/ADR-0002-format-v2-compact-binary-index-and-v1-removal.md).

## Normative Requirements

- `REQ-0001-01`: A valid Format v2 fat executable SHALL terminate with the exact 56-byte EOF trailer, and readers SHALL reject any file smaller than 56 bytes (`ErrBinaryTooSmall`) or lacking trailing magic `"\x00\xFA\x7FMICRO"` (`ErrInvalidMagic`).
- `REQ-0001-02`: Readers SHALL validate that `indexOffset >= 0`, `indexSize > 0`, `indexSize <= MaxIndexSize` (1 MiB), and `indexOffset + indexSize == fileSize - 56` without signed integer overflow, and SHALL verify the raw index bytes against the trailer's 32-byte `indexSHA256` before decoding index fields (`ErrIndexCorrupted`).
- `REQ-0001-03`: Both writers and readers SHALL enforce `Version == 2` (`FormatVersionCurrent`) and SHALL fail closed with `ErrUnsupportedVersion` for Format v1 JSON manifests or unrecognized version tags.
- `REQ-0001-04`: `UnmarshalBinaryIndex` and Rust `parse_index` SHALL reject truncated payloads, duplicate or invalid variant levels, malformed 64-character hex SHA-256 digests, and ANY trailing unread bytes after decoding the declared variant count (`ErrTruncatedIndex`).
- `REQ-0001-05`: Every embedded optional shared dictionary and variant segment SHALL satisfy non-negative bounds (`dictOffset >= 0`, `dictSize <= MaxDictionarySize`, `dictOffset + dictSize <= indexOffset`; and for each variant, `offset > 0`, `offset >= dictEnd`, monotonic non-overlapping offsets `prevEnd <= offset`, `offset + compressedSize <= indexOffset`, and `uncompressedSize <= MaxPayloadSize`).
- `REQ-0001-06`: Binary index marshaling and unmarshaling across Go and Rust SHALL match the canonical Lean 4 `FormatV2` bit-for-bit specification and golden conformance vectors at [`formal/conformance/vectors.json`](../../formal/conformance/vectors.json).

## Formal Laws & Invariants

- `LAW-FORMAT-01`: (`checkDictionarySegmentBounds`) Dictionary segment `[dictOffset, dictOffset + dictSize)` stays strictly inside `[0, indexOffset]` with `0 <= dictSize <= MaxDictionarySize` and zero `int64` overflow (Gobra proof, `task verify-format-bounds`).
- `LAW-FORMAT-02`: (`checkVariantSegmentBounds`) Variant segments form a strictly bounded, non-overlapping monotone chain inside `[dictEnd, indexOffset]` with zero `int64` overflow (Gobra proof, `task verify-format-bounds`; sensitivity mutant `nextEnd - 1`).
- `LAW-FORMAT-03`: (`checkTrailerAndIndexBounds` in [`internal/format/bounds.go`](../../internal/format/bounds.go), called by `ReadTrailerAndIndex` in [`internal/format/format.go`](../../internal/format/format.go)) Trailer and index size arithmetic (`TrailerSize`, `MaxIndexSize`, EOF adjacency) are free of overflow and enforce fail-closed validation (Gobra proof, `task verify-format-bounds`).
- `LAW-SMT-01`: Exhaustive `QF_BV` proof over all `2^64` 64-bit unsigned words verifying Format v2 offset, size, overflow, and overlap guards ([`formal/smt/format_v2_bounds.smt2`](../../formal/smt/format_v2_bounds.smt2), `task verify-z3`).
- `LAW-LEAN-01`: Marshal/unmarshal round-trip bijection, trailing-byte rejection, and segment disjointness with zero `sorryAx` ([`formal/lean/Microfat/FormatV2.lean`](../../formal/lean/Microfat/FormatV2.lean), `task verify-lean`).
- `LAW-KANI-01`: Bounded symbolic verification (`1..=4` variants, `<= 64B` cursor) on `#![no_std]` `#![forbid(unsafe_code)]` Rust parser ([`experiments/v3lab/rust-stub/src/format.rs`](../../experiments/v3lab/rust-stub/src/format.rs), `task verify-kani`).

## Traceability Matrix

| ID | Description | Implementation | Verification (Tests / Proofs) |
| :--- | :--- | :--- | :--- |
| `REQ-0001-01` | Fixed 56-byte EOF trailer & magic validation | [`internal/format/format.go`](../../internal/format/format.go), [`experiments/v3lab/rust-stub/src/format.rs`](../../experiments/v3lab/rust-stub/src/format.rs) | [`internal/format/format_test.go`](../../internal/format/format_test.go), [`internal/format/trailer_single_read_test.go`](../../internal/format/trailer_single_read_test.go) |
| `REQ-0001-02` | Index bounds, EOF adjacency & index SHA-256 check | [`internal/format/format.go`](../../internal/format/format.go), [`internal/format/bounds.go`](../../internal/format/bounds.go) | [`internal/format/bounds_test.go`](../../internal/format/bounds_test.go), [`tests/e2e/corruption_test.go`](../../tests/e2e/corruption_test.go) |
| `REQ-0001-03` | Format v2 enforcement & Format v1 rejection | [`internal/format/format.go`](../../internal/format/format.go) | [`tests/e2e/legacy_json_test.go`](../../tests/e2e/legacy_json_test.go) |
| `REQ-0001-04` | Trailing bytes, duplicate levels & fuzz robustness | [`internal/format/format.go`](../../internal/format/format.go), [`experiments/v3lab/rust-stub/src/format.rs`](../../experiments/v3lab/rust-stub/src/format.rs) | [`internal/format/format_fuzz_test.go`](../../internal/format/format_fuzz_test.go), [`internal/format/property_test.go`](../../internal/format/property_test.go) |
| `REQ-0001-05` | Disjoint, overflow-free dictionary & variant bounds | [`internal/format/bounds.go`](../../internal/format/bounds.go) | `TestValidateBounds`, [`docs/design/format-bounds-verification.md`](../design/format-bounds-verification.md) |
| `REQ-0001-06` | Cross-language Go/Rust/Lean binary parity | [`internal/conformance/conformance_test.go`](../../internal/conformance/conformance_test.go) | `TestLeanConformance_FormatV2`, `lean_conformance_format_v2` |
| `LAW-FORMAT-01` | Dictionary segment overflow & bounds freedom | [`internal/format/bounds.go`](../../internal/format/bounds.go) | `task verify-format-bounds` |
| `LAW-FORMAT-02` | Monotonic non-overlapping variant segment bounds | [`internal/format/bounds.go`](../../internal/format/bounds.go) | `task verify-format-bounds` |
| `LAW-FORMAT-03` | Trailer & index decoding safety | [`internal/format/bounds.go`](../../internal/format/bounds.go) | `task verify-format-bounds` |
| `LAW-SMT-01` | Exhaustive 64-bit Format v2 SMT bounds proof | [`formal/smt/format_v2_bounds.smt2`](../../formal/smt/format_v2_bounds.smt2) | `task verify-z3` |
| `LAW-LEAN-01` | Lean 4 Format v2 bijection & disjointness | [`formal/lean/Microfat/FormatV2.lean`](../../formal/lean/Microfat/FormatV2.lean) | `task verify-lean` |
| `LAW-KANI-01` | Kani bounded model check on Rust Format v2 | [`experiments/v3lab/rust-stub/src/format.rs`](../../experiments/v3lab/rust-stub/src/format.rs) | `task verify-kani` |
