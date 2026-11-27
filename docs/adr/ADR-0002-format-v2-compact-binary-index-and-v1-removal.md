# ADR-0002: Format v2 Compact Binary Index, 56-Byte Trailer & Format v1 Removal

- **Status**: Accepted (Retroactive — v0.2–v0.4)
- **Date**: 2026-11-27
- **Deciders**: Project Maintainer (@EpicBlackWolfZ)
- **Related Issues**: [#427](https://github.com/EpicBlackWolfZ/microfat/issues/427), [#468](https://github.com/EpicBlackWolfZ/microfat/issues/468), [#523](https://github.com/EpicBlackWolfZ/microfat/issues/523), [#526](https://github.com/EpicBlackWolfZ/microfat/issues/526)
- **Related Specs**: [SPEC Index](../specs/README.md) (`SPEC-0001`), [Architecture Specification](../architecture.md)
- **Supersedes**: Legacy Format v1 JSON index schema
- **Superseded By**: None ([ADR-0014](ADR-0014-format-v3-block-deduplicated-container-with-v2-coexistence.md) adds Format v3 alongside Format v2)

## Context

Early `microfat` prototypes encoded container manifests as tail-appended Format v1 JSON payloads, which required JSON unmarshaling in the hot path of every cold and warm binary launch, inflated launcher code size with reflection-heavy decoders, and exposed non-canonical parsing edge cases (trailing data, nested JSON depth, signed/unsigned bounds confusion, and digest casing drift). As `microfat` matured through `v0.2.x`–`v0.4.x`, launcher startup needed sub-microsecond, zero-allocation index parsing and strict binary bounds verification before touching any payload bytes.

## Decision

`microfat` standardizes Format v2 as a compact, length-prefixed binary container format anchored by a fixed 56-byte EOF trailer (`internal/format/`):

1. **Fixed 56-Byte EOF Trailer (`TrailerSize = 56`):** Encodes `IndexOffset` (`uint64` LE), `IndexSize` (`uint64` LE), `IndexSHA256` (`[32]byte`), and `Magic` (`\x00\xFA\x7FMICRO`). Launchers and verifiers validate `IndexOffset + IndexSize == file_size - 56` and `MaxIndexSize` (`1 MiB`) overflow-safely before reading the index, and verify `sha256(index_bytes) == IndexSHA256` before interpreting any field (`ErrIndexCorrupted`).
2. **Reflection-Free Format v2 Index (`IndexMagicV2 = "\x00\xFAM2"`, `FormatVersion2 = 2`):** Stores `CreatedUnix` (`int64` two's complement supporting `SOURCE_DATE_EPOCH` including negative pre-1970 epochs), optional shared Zstd dictionary metadata (`DictOffset`, `DictSize`, `DictID`, `DictSHA256`), target OS/arch, application name, and `K` variant records (`Level`, `Offset`, `CompressedSize`, `UncompressedSize`, `SHA256`, `Compression`).
3. **Tight Index & Bounds Invariants:** `UnmarshalBinaryIndex` rejects trailing bytes (`ErrInvalidIndexSize`), rejects `uint64` offsets/sizes exceeding `math.MaxInt64`, requires zeroed `DictOffset`/`DictID`/`DictSHA256` when `DictSize == 0`, enforces non-overlapping sorted segments, and normalizes valid hex digests to lowercase after trailer checksum verification.
4. **Permanent Format v1 Removal (`v0.4.0`):** Legacy Format v1 JSON manifests were deprecated in `v0.2.3` and permanently removed in `v0.4.0` ([#427](https://github.com/EpicBlackWolfZ/microfat/issues/427)). Format v1 binaries and `--format-version 1` fail closed with `ErrUnsupportedVersion`.

### Alternatives Rejected

- **Retaining Format v1 JSON with streaming parsers:** Rejected because reflection/allocating parsers increase stub size and latency and introduce JSON ambiguity risks at trust boundaries.
- **Keeping Format v1 read support indefinitely in `v0.4.x+`:** Rejected to eliminate unmaintained legacy attack surface and simplify Gobra/Lean/Z3/Kani bounds proofs (`format_v2_bounds.smt2`, `FormatV2.lean`, `format/bounds.go`).

## Consequences & Tradeoffs

- **Security & Correctness:** Exact bijection between marshaled and unmarshaled indexes with strict bounds checking prevents integer overflow, overlapping-slice aliasing, and post-index trailing smuggling; SHA-256 detects bit-flips and corruption (though external signatures such as Cosign remain required for producer authenticity).
- **Compatibility:** Format v1 bundles must be rebuilt with `microfat pack` before running on `v0.4.0+`; Format v2 remains fully supported alongside Format v3 in `v0.5.0` per [ADR-0014](ADR-0014-format-v3-block-deduplicated-container-with-v2-coexistence.md).
- **Verification:** Bounds and bijection laws are mechanically verified by Gobra (`LAW-FORMAT-01`, `LAW-FORMAT-02`), Z3 (`LAW-SMT-01`), Lean 4 (`LAW-LEAN-01`), and Kani (`LAW-KANI-01`).

## Rollback

Format v2 is the permanent wire baseline for `v0.2`–`v0.5`. Rebuilding any affected bundle using an earlier `v0.3.x` binary restores historical Format v1 readability outside `v0.4+` streams if an un-migrated archive is encountered; Format v2 writers remain selectable via `--format-version 2`.
