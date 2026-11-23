# ADR-0001: Multi-Toolchain Formal Verification & Format v3 / Native Rust Stub Research Lab

- **Status**: Accepted (Research & Verification Baseline; Format v2 production remains unchanged until promoted)
- **Date**: 2026-11-23
- **Related Issues**: [#378](https://github.com/EpicBlackWolfZ/microfat/issues/378), [#478](https://github.com/EpicBlackWolfZ/microfat/issues/478), [#479](https://github.com/EpicBlackWolfZ/microfat/pull/479), [#497](https://github.com/EpicBlackWolfZ/microfat/issues/497), [#498](https://github.com/EpicBlackWolfZ/microfat/issues/498), [#499](https://github.com/EpicBlackWolfZ/microfat/issues/499), [#500](https://github.com/EpicBlackWolfZ/microfat/issues/500), [#501](https://github.com/EpicBlackWolfZ/microfat/issues/501), [#45](https://github.com/EpicBlackWolfZ/microfat/issues/45), [#46](https://github.com/EpicBlackWolfZ/microfat/issues/46), [#163](https://github.com/EpicBlackWolfZ/microfat/issues/163), [#375](https://github.com/EpicBlackWolfZ/microfat/issues/375), [#423](https://github.com/EpicBlackWolfZ/microfat/issues/423)

## 1. Context

With `v0.4.1` (`118fd76` / `origin/main` `4dd5dbf`) released, Microfat's Format v2 packaging, `cache-first` + sealed `memfd` dispatch, cgroup tuning (including direct Gobra production proof in [#479](https://github.com/EpicBlackWolfZ/microfat/pull/479)), and CPU capability selection are stable and covered by >= 95% race-checked Go tests. Two gaps motivate a post-`v0.4.1` dual-track engineering effort:

1. **Mathematical & bit-level certainty over core invariants**: Fuzzing and unit tests establish behavior only on sampled inputs. Key invariants—Format v2 canonical bijection and non-overlapping interval bounds, SIGILL-freedom over the non-linear ARM64 `v8.x`/`v9.x` DAG, overflow-free 64-bit cgroup/memory budget arithmetic, and exact reversibility of any future binary transform—benefit from machine-checked formal proofs.
2. **Breaking past v0.4.1 size and cold-start ceilings**:
   - The Go launcher stubs (`stub-full` ~3.92 MiB, `stub-minimal` ~3.77 MiB) impose a fixed multi-MiB footprint and Go runtime bootstrap latency on every fat executable.
   - Prior delta/CDC deduplication experiments (`#410`, `#45`, `#47`, `#379`) cleared 5% size savings only with monolithic whole-base/pool materialization, causing 1.5–3.2× cold-path slowdowns and 256 MiB OOMs on unstripped `Task`.
   - `checkExtractionBudget` (`cmd/microfat-stub/memory_linux.go:41`) charges `512 MiB` (`DefaultDecoderMaxMemory`) for every Zstd payload despite `<= 8 MiB` producer windows, and warm-cache verification performs a sequential full-file SHA-256 `pread` scan (`internal/cache/cache_unix.go:257`).

## 2. Decision

We establish two concurrent tracks on topic branch `feat/formal-verification-and-v3-lab` while preserving production Format v2 stability:

### Track A: Multi-Toolchain Formal Verification Complementary to Production Gobra

Each tool verifies the layer where its formal guarantees are strongest, with explicit separation between model proofs and shipped-code verification:

| Tool | Scope & Artifact | Claim Boundary |
| :--- | :--- | :--- |
| **Lean 4** (`formal/lean/`) | Mathematical specs & total proofs (`FormatV2`, `Microarch`, `CgroupMath`, `ElfFilter` reversible bijections). Zero `sorry`, zero `sorryAx`, zero `native_decide`/custom axioms. | Proves properties of the exact Lean specification and reversible filter algebra. Linked to production Go/Rust via shared constants and conformance tests. |
| **Z3 SMT-LIB2** (`formal/smt/`) | `QF_BV` 64-bit signed/unsigned two's-complement bitvector queries (`format_v2_bounds.smt2`, `cgroup_memory.smt2`). | Exhaustive over all 64-bit machine words for the modeled arithmetic guards (`sat` premises + `unsat` negated safety obligations). |
| **Gobra** (canonical baseline from [#479](https://github.com/EpicBlackWolfZ/microfat/pull/479)) | Direct verification on production Go (`internal/cgroup/memory.go`) via `scripts/verify-cgroup-memory.sh` and `verification/gobra/stubs/math/limits.gobra` (CI-enforced in `task verify-cgroup-memory`). | See [`cgroup-memory-verification.md`](cgroup-memory-verification.md); covers `ExtractionMemory` and `RetainedMemoryLimit`, not sysfs discovery or package callers (`#501` scopes Zstd budget & Format v2 extension). |
| **Bend 2 + BendTT** (`experiments/v3lab/bend/`) | Pure parallel search kernels and `--verdict` proof checks for reversible positional offset transforms and block matching on CPU / RTX 2080 Ti GPU. | `--verdict` covers pure non-`@unsafe` Bend definitions; runtime speedups require checking which backend actually ran against a Go baseline. |
| **Kani / Verus** (`experiments/v3lab/rust-stub/`) | Symbolic model checking (`#[cfg(kani)]`) directly over production Rust stub parsing, feature-lattice selection, and `ElfFilter` bounds/bijections. | Checks the exact compiled Rust source (matching integer widths and target) up to stated harness bounds; complements release and E2E execution tests. |

### Track B: Format v3 & Native Rust Stub Research Lab (`experiments/v3lab/`)

1. Prototype reversible ELF/ISA normalization (`x86-64 BCJ+RIP` covering `E8`/`E9`, `0F 80..8F`, and `REX.W/WR 8D/8B/89 ModRM(mod=00,rm=101)` + `ARM64 BL/B/ADRP`) proven bijective in Lean 4 and Bend 2.
2. Prototype zero-base-materialization independent block deduplication (128 KiB / 256 KiB / 512 KiB / section-aligned CDC) with direct parallel `Pwrite` decoding into preallocated sealed `memfd`.
3. Prototype an ultra-lean static Linux Rust launcher stub (`microfat-stub-rs`) targeting ~60–150 KiB with direct Linux syscalls, privilege refusal, CPUID selection, streaming decompression + SHA-256 verification, and 4-seal `memfd_create`.

## 3. Promotion & Rollback Criteria

- **Production Format v2 remains unchanged** during research and verification.
- Any promotion of Format v3 or `microfat-stub-rs` to production requires:
  1. Strict complete-cost wins (total fat binary size = stub + metadata + payloads + trailer, cold `memfd` and `cold-cache` launch latency, peak cgroup RSS, and Pack wall/CPU/RSS) over `v0.4.1` `balanced` and `size` controls across the pinned Go corpus on native `amd64` and `arm64`.
  2. Full security and chaos parity with `SECURITY.md` (elevated launch refusal, FIFO/symlink/corruption rejection, `O_NOFOLLOW`, mandatory 4-seal `memfd`, cgroup tuning).
  3. Zero regression in `NO_COLOR=1 task all` (`tidy-check`, `fmt-check`, `golangci-lint`, `shellcheck`, `govulncheck`, `-race` tests, >= 95% coverage, build).
- **Rollback**: Because all lab prototypes reside under `experiments/v3lab/` and formal proofs under `formal/`, reverting or parking any unsuccessful prototype leaves Format v2 binaries, `cmd/microfat`, and `cmd/microfat-stub` unaffected.
