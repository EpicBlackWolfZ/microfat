# Format v2 segment bounds laws

[`internal/format/bounds.go`](../../internal/format/bounds.go) is both the Go build
input and the Gobra input for Format v2 dictionary and variant segment bounds
validation. The `@` comments add proof obligations without emitting runtime code,
using mathematical integers in ghost predicates while checking signed 64-bit
overflow via Gobra's `--overflow` pass.

| Law | Checked contract |
| --- | --- |
| **Dictionary bounds** | `checkDictionarySegmentBounds(indexOffset, dictOffset, dictSize)` succeeds iff `0 <= indexOffset`, `0 <= dictSize <= MaxDictionarySize`, and (when `dictSize > 0`) `0 <= dictOffset`, `dictOffset + dictSize <= math.MaxInt64`, and `dictOffset + dictSize <= indexOffset`. Success returns `lastEnd` equal to `0` or `dictOffset + dictSize` (`0 <= lastEnd <= indexOffset`). Failure returns zero and `ErrOutOfBounds` or `ErrInvalidDictionary`. |
| **Variant bounds** | `checkVariantSegmentBounds(indexOffset, lastEnd, hasPrev, offset, compressedSize, uncompressedSize)` succeeds iff `0 <= indexOffset`, `0 <= offset`, `0 < compressedSize`, `0 < uncompressedSize <= MaxPayloadSize`, `offset + compressedSize <= math.MaxInt64`, `offset + compressedSize <= indexOffset`, and `(!hasPrev || 0 <= lastEnd <= offset)`. Success returns `nextEnd = offset + compressedSize` (`0 <= nextEnd <= indexOffset`). Failure returns zero and `ErrOutOfBounds`, `ErrPayloadTooLarge`, or `ErrOverlappingVariant`. |

The laws assume `ErrOutOfBounds`, `ErrPayloadTooLarge`, `ErrOverlappingVariant`, and
`ErrInvalidDictionary` are the nonnil, comparable sentinels initialized by `errors.New`
without external reassignment, and trust Gobra's `errors` model plus
[`verification/gobra/stubs`](../../verification/gobra/stubs/math/limits.gobra). They
do not verify SHA-256 verification, arch-rank string parsing, or JSON/binary decoding
elsewhere in `internal/format/format.go`, which remain verified by unit/fuzz tests and
Lean 4 / Z3 models. See [`cgroup-memory-verification.md`](cgroup-memory-verification.md)
for the companion memory-budget proofs.

## Reproduce

```sh
task verify-format-bounds
task verify-format-bounds GOBRA_ENGINE=podman
go test -race ./internal/format
```

Both `verify-cgroup-memory` and `verify-format-bounds` share
[`scripts/verify-gobra.sh`](../../scripts/verify-gobra.sh) and the pinned `GOBRA_LINUX_AMD64`
OCI digest in [`.config/tool-versions.env`](../../.config/tool-versions.env). Because
Gobra v26.09's `--overflow` pass verifies bounds per arithmetic expression rather than
inhaling parameter upper limits on entry, explicit `math.MaxInt64` subtraction guards
(`dictSize > math.MaxInt64-dictOffset` and `compressedSize > math.MaxInt64-offset`)
establish signed overflow freedom alongside the `indexOffset` ceiling checks.

To verify regression sensitivity on a disposable copy, change the final variant
return from `return offset + compressedSize, nil` to `return offset + compressedSize - 1, nil`
(Silicon must reject the exact `nextEnd` postcondition with `Postcondition might not hold.`).

## Gate and rollback

`ci.yml` (`lint`) and `release.yml` (`gate`) both run `task verify-format-bounds` as
a mandatory step (`id: format-bounds-proof`), enforced by `internal/cigate`. Reverting
or loosening the contract requires explicit review.
