# Z3 Bit-Vector SMT Proofs (`QF_BV`)

64-bit signed bit-vector models of `int64` arithmetic and boundary checks in `microfat`.

## Pinned Tool

- **Solver**: Z3 `5.3.3` (`z3-5.3.3-x64-glibc-2.39.zip`)
- **Upstream Release**: <https://github.com/Z3Prover/z3/releases/tag/z3-5.3.3>
- **SHA-256 (Linux x64 zip)**: `d56f60b06f432f33de5810f7f1a384a339131f361e2d54d08ea83eea14326880`
- **Local Scratch Path**: `.work/tools/z3/z3-5.3.3-x64-glibc-2.39/bin/z3` (gitignored)

## Verification Protocol

Every `.smt2` file enforces two mandatory checks via `(push 1)` / `(pop 1)`:
1. **Non-vacuity (`sat`)**: Premises admit at least one concrete 64-bit assignment.
2. **Safety (`unsat`)**: Negated safety conditions (signed `int64` wraparound, subtraction
   underflow, overlapping segments, out-of-bounds slices) have zero counterexamples across
   all `2^64` 64-bit signed values.

Run locally from the repository root:

```bash
.work/tools/z3/z3-5.3.3-x64-glibc-2.39/bin/z3 formal/smt/format_v2_bounds.smt2
.work/tools/z3/z3-5.3.3-x64-glibc-2.39/bin/z3 formal/smt/cgroup_memory.smt2
```

## Claim Boundary

These SMT queries check exact 64-bit signed bit-vector encodings (`QF_BV`) derived directly
from `internal/format/format.go` (`ReadTrailerAndIndex`, `ValidateBounds`,
`validateDictionaryBounds`, `validateVariantBounds`) and `internal/cgroup/{memory,cgroup}.go`
(`ExtractionMemory`, `RetainedMemoryLimit`, `CalculateGOMEMLIMIT` integer headroom path).
Go runtime string/map handling (`seenLevels`) and IEEE-754 floating-point `ratio`
rounding are out of scope of `QF_BV` and verified via Go unit/fuzz tests and Lean 4.
