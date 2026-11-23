# Post-`v0.4.1` Formal Verification & Compression Research Results (`v3lab`)

- **Topic Branch**: `feat/formal-verification-and-v3-lab` (rebased cleanly onto `origin/main` `4dd5dbf`)
- **Design Decision & Promotion Gates**: [ADR-0001](../../docs/design/adr-0001-formal-verification-and-v3-lab.md)
- **Related Issues**: [#378](https://github.com/EpicBlackWolfZ/microfat/issues/378), [#497](https://github.com/EpicBlackWolfZ/microfat/issues/497), [#498](https://github.com/EpicBlackWolfZ/microfat/issues/498), [#499](https://github.com/EpicBlackWolfZ/microfat/issues/499), [#500](https://github.com/EpicBlackWolfZ/microfat/issues/500), [#501](https://github.com/EpicBlackWolfZ/microfat/issues/501)

This document summarizes all observed verification results, benchmarks, and promotion-gate assessments from the post-`v0.4.1` dual-track engineering sprint. Every figure below comes directly from artifacts and command runs on this machine; unverified production claims or absent hardware backends (specifically absent CUDA Toolkit / `nvrtc.h` for Bend GPU codegen) are explicitly noted.

---

## 1. Track A — Multi-Toolchain Formal Verification Scorecard

| Toolchain | Version & Target Scope | Status | Verified Targets / Properties | Claim Boundaries & Assumptions |
| :--- | :--- | :--- | :--- | :--- |
| **Lean 4** | `v4.34.0` on mathematical specs & filter algebra ([`formal/lean/Microfat.lean`](../../formal/lean/Microfat.lean)) | **PASS** (`lake build`, 7 jobs, 0 warnings) | **26 theorems** across `FormatV2` (marshal/unmarshal round-trip, exact consumption, trailing-byte rejection, segment bounds/disjointness), `Microarch` (AMD64 rank iff, monotonicity, ARM64 DAG acyclicity, SIGILL freedom, unforced optimality), `CgroupMath` (overflow guard, retained limit, `GOMEMLIMIT` ceiling & monotonicity, worker admission sum), and `ElfFilter` (word & byte-stream bijections for ARM64 BL/B/ADRP and x86-64 BCJ+RIP). Uses **only** `{propext, Classical.choice, Quot.sound}`; zero `sorry`, `sorryAx`, `native_decide`, or custom axioms. | Proves properties of the Lean model rather than directly compiling Go/Rust binaries; linked to shipped Go (`experiments/v3lab/compress/elffilter_test.go`) and Rust (`experiments/v3lab/rust-stub/src/elffilter.rs`) via shared `#eval` conformance vectors and `cargo test` / `go test -race`. |
| **Z3 (`QF_BV`)** | `v5.3.3` on 64-bit bitvector SMT-LIB2 ([`formal/smt/`](../../formal/smt/README.md)) | **PASS** | `format_v2_bounds.smt2`: `sat` (premise satisfiability) + 3× `unsat` (negated overflow, bounds, and overlap checks). `cgroup_memory.smt2`: 3× `sat` + 3× `unsat` (overflow guard, `RetainedMemoryLimit`, `GOMEMLIMIT` headroom/ceiling bounds across all `2^64` words). | Models exact two's-complement 64-bit signed/unsigned arithmetic of the guards; independent of allocator/sysfs I/O. |
| **Gobra** | `0.1.0-SNAPSHOT` (`viperproject/gobra-ide v3.1.2` local `--overflow`) + canonical CI gate (`v26.09`, [#479](https://github.com/EpicBlackWolfZ/microfat/pull/479)) directly on **production Go** [`internal/cgroup/memory.go`](../../internal/cgroup/memory.go) | **PASS** (`--overflow`, `Gobra found 0 errors.`) | Directly proves signed 64-bit overflow freedom and exact functional pre/postconditions on production `ExtractionMemory` and `RetainedMemoryLimit`, matching the canonical `scripts/verify-cgroup-memory.sh` / [`cgroup-memory-verification.md`](../../docs/design/cgroup-memory-verification.md) contract. | Scoped via `-i internal/cgroup/memory.go` with [`verification/gobra/stubs/math/limits.gobra`](../../verification/gobra/stubs/math/limits.gobra) (`MaxInt64`); remaining sysfs/float helpers and Format v2 bounds (`#501`) remain covered by Go tests, Lean 4, and Z3. |
| **Bend 2** | `v2.0.40` (`--verdict`) on [`experiments/v3lab/bend/filter_proof.bend`](bend/filter_proof.bend) | **PASS** (`ALL PROOFS CHECK`, zero `@unsafe`) | Machine-checks pure bidirectional step & wraparound round-trip properties for normalized x86/ARM64 offset transforms inside pure Bend code. | Model-level check in Bend (`filter_proof.bend`); does not directly inspect Go ELF parsing. |
| **Kani** | `v0.68.0` standalone (`CBMC 6.11.0`, `nightly-2026-08-21`) directly on **Rust stub core** [`experiments/v3lab/rust-stub/src/lib.rs`](rust-stub/src/lib.rs) | **PASS** (`8/8 harnesses`, 0 failures) | Verifies compiled `#![no_std]` `#![forbid(unsafe_code)]` Rust code directly: `verify_arm64_step_word_bijection` (`0.21s`), `verify_x86_step_bijection` (`1.39s`), `verify_x86_transform_roundtrip_8` (`28.88s`), `verify_evaluate_amd64_exhaustive` over all `2^21` feature masks (`0.08s`), `verify_trailer_never_panics_and_enforces_exact_layout` (`0.69s`), `verify_cursor_primitives_safe` (`1.76s`), `verify_decode_hex_digest_no_panic` (`3.65s`), `verify_segment_bounds_disjointness_and_selection` for `1..=4` symbolic variants (`34.40s`). | Checks Rust implementation under explicit symbolic buffer/loop bounds (`len <= 8` for multi-step x86 filter, `1..=4` variants for segment layout, `<= 64B` cursor slice); full arbitrarily-long x86 stream bijection is proven unbounded in Lean 4. `main.rs` Linux syscalls (`memfd_create`, `fcntl`, `execve`) and `cpuid.rs` asm probe are tested E2E rather than Kani-modeled. |

### Alignment with `origin/main` Hardening (`#479` & `v0.4.1`)

`origin/main` (`4dd5dbf`) already hardened [`internal/format/format.go`](../../internal/format/format.go) (rejecting trailing index bytes, negative dictionary/variant dimensions, and non-zero offsets/IDs on zero-size dictionaries while allowing negative `CreatedUnix`) and merged direct production Gobra verification of [`internal/cgroup/memory.go`](../../internal/cgroup/memory.go) via [#479](https://github.com/EpicBlackWolfZ/microfat/pull/479). Accordingly, this branch introduces no unpromoted production Format v2 or Go stub behavior edits; all research code stays isolated under `formal/` and `experiments/v3lab/`.

Follow-up issues track the measured gaps before promotion: [#499](https://github.com/EpicBlackWolfZ/microfat/issues/499) (Rust stub SHA-NI, `opt-level=3` crypto/Zstd, static `musl`, chaos parity), [#500](https://github.com/EpicBlackWolfZ/microfat/issues/500) (Format v3 fused per-extent parallel unfilter, seeded Zstd block dictionary, ARM64 corpus), and [#501](https://github.com/EpicBlackWolfZ/microfat/issues/501) (window-accurate Zstd extraction budget in production + expanded Gobra/CI checks).

---

## 2. Track B1 — Bend 2 Parallel Kernel vs Go Goroutines

Workload: [`experiments/v3lab/bend/block_match.bend`](bend/block_match.bend) and parity routine `runBendParityWorkers` in [`experiments/v3lab/cmd/v3bench/main.go`](cmd/v3bench/main.go) (`16,384` leaves × `256` outer × `64` inner LCG probes = `268,435,456` bitwise checks; exact deterministic `hits = 16,777,216`).

- **Hardware / Backend Reality Check**: Host has an `NVIDIA GeForce RTX 2080 Ti` (`driver 620.70.06`), **no CUDA Toolkit (`nvrtc.h`)**, and no `clang`. Consequently `bend` emitted `no CUDA toolkit (include/nvrtc.h ...), so ! runs on the CPU` and compiled the generated parallel C (`gcc -O3 -pthread`) onto the host CPU thread pool.

| Worker / Thread Count | Bend 2 (`gcc -O3 -pthread` CPU Pool) | Go `v3bench` (Goroutines, 3-Run Median) | Exact Match Result |
| :---: | :---: | :---: | :--- |
| `1` | `450 ms` | `1091.36 ms` | `hits = 16,777,216` (both) |
| `4` | `124 ms` | `287.56 ms` | `hits = 16,777,216` (both) |
| `16` | `34 ms` | `76.85 ms` | `hits = 16,777,216` (both) |
| `32` | `19 ms` | `51.24 ms` | `hits = 16,777,216` (both) |

Even strictly on CPU threads (`32` cores), Bend's compiled C thread-pool kernel sustained ~2.4–2.7× higher bitwise probe throughput than Go goroutines on identical arithmetic (`19 ms` vs `51.24 ms`), making it an effective rapid prototyping target if CUDA Toolkit / `nvrtc.h` is added later for GPU-accelerated offline dictionary/CDC research (`Pack`-side only; runtime stubs never require GPU).

---

## 3. Track B2 — Format v2 vs Format v3 Lab Corpus Benchmark

Built from real `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1|v2|v3` Go binaries (`./examples/demo` and `./cmd/microfat`, both stripped `-s -w` and unstripped). Format v2 baseline controls use `internal/pack.Pack` at `balanced` and `size` (`zstd:best`) with `net` excluding stub bytes for apples-to-apples archive payload+dict+index+trailer comparison; Format v3 prototypes use [`experiments/v3lab/compress/`](compress/) (`ChunkFixed` and `ChunkSectionCDC`, optional `x86-64 BCJ+RIP` executable-section filter, parallel `zstd.SpeedBestCompression` block encoding, parallel `unix.Pwrite` decode into 4-seal `memfd` with end-to-end SHA-256 verification).

### Summary Across All 4 Targets

| Corpus Target (`v1+v2+v3` Raw Sum) | Best v2 (`size-dictprof`) Net Bytes (Ratio) | Best v3 Config | v3 Total Archive Bytes (Ratio) | v3 Size Delta vs Best v2 | v2 Pack / Cold Decode (`1w`) | Best v3 Pack / Decode (`4w`) |
| :--- | :---: | :--- | :---: | :---: | :---: | :---: |
| **`demo-stripped`** (`12,997,088 B`) | `5,294,442 B` (`40.74%`) | `v3-cdc-64k-bcj` | `5,098,961 B` (`39.23%`) | **`-3.69%`** | `418.3 ms` / `12.27 ms` | `188.4 ms` / `9.39 ms` (`4.91 ms` no-BCJ) |
| **`demo-unstripped`** (`19,251,685 B`) | `10,719,553 B` (`55.68%`) | `v3-cdc-64k-bcj` | `10,559,847 B` (`54.85%`) | **`-1.49%`** | `480.2 ms` / `10.09 ms` | `205.2 ms` / `11.25 ms` (`6.90 ms` fixed-no-BCJ) |
| **`microfat-stripped`** (`34,767,328 B`) | `13,445,848 B` (`38.67%`) | `v3-cdc-128k-bcj` | **`12,187,709 B`** (`35.06%`) | **`-9.36%`** | `1118.4 ms` / `28.66 ms` | `328.9 ms` / `24.98 ms` (`12.46 ms` no-BCJ) |
| **`microfat-unstripped`** (`50,803,762 B`) | `27,323,023 B` (`53.78%`) | `v3-cdc-128k-bcj` | `25,938,745 B` (`51.06%`) | **`-5.07%`** | `1289.7 ms` / `31.30 ms` | `354.9 ms` / `29.90 ms` (`17.48 ms` fixed-no-BCJ) |

### Full Variant Matrix (`demo-stripped` and `microfat-stripped`)

#### `demo-stripped` (`v1=4,337,824 B`, `v2=4,333,728 B`, `v3=4,325,536 B`, `raw_sum=12,997,088 B`)

| Variant | Archive Bytes | Dict / Index Bytes | Unique / Total Blocks | Dedup Raw Saved | Delta vs `v2-size-nodict` | Pack (`ms`) | Cold `memfd` `1w` / `4w` (`ms`) |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| `v2-balanced` (net) | `5,599,468 B` | `-` | mono | `0 B` | `+5.76%` | `51.3` | `8.05` / `-` |
| `v2-size-nodict` (net) | `5,294,442 B` | `-` | mono | `0 B` | baseline | `224.1` | `15.50` / `-` |
| `v2-size-dictprof` (net) | `5,294,442 B` | `-` (rejected) | mono | `0 B` | `0.00%` | `418.3` | `12.27` / `-` |
| `v3-fixed-128k-nobcj-nodict` | `5,513,508 B` | `0` / `6,705` | `102 / 102` | `0 B` | `+4.14%` | `150.7` | `9.96` / **`4.83`** |
| `v3-fixed-128k-bcj-nodict` | `5,252,887 B` | `0` / `6,753` | `102 / 102` | `0 B` | `-0.78%` | `178.9` | `14.19` / `9.20` |
| `v3-fixed-512k-bcj-dict` | `5,106,979 B` | `0` / `1,953` | `27 / 27` | `0 B` | `-3.54%` | `171.8` | `13.97` / `9.25` |
| `v3-cdc-64k-bcj-dict` | **`5,098,961 B`** | `0` / `11,249` | `167 / 188` | `299,524 B` | **`-3.69%`** | `188.4` | `14.46` / `9.39` |
| `v3-cdc-128k-nobcj-nodict` | `5,402,833 B` | `0` / `7,457` | `110 / 125` | `181,718 B` | `+2.05%` | `177.0` | `9.97` / **`4.91`** |
| `v3-cdc-128k-bcj-nodict` | `5,118,333 B` | `0` / `7,313` | `107 / 122` | `181,718 B` | **`-3.33%`** | `185.8` | `14.33` / `9.30` |

#### `microfat-stripped` (`v1=11,591,840 B`, `v2=11,591,840 B`, `v3=11,583,648 B`, `raw_sum=34,767,328 B`)

| Variant | Archive Bytes | Dict / Index Bytes | Unique / Total Blocks | Dedup Raw Saved | Delta vs `v2-size` | Pack (`ms`) | Cold `memfd` `1w` / `4w` (`ms`) |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| `v2-balanced` (net) | `14,215,839 B` | `-` | mono | `0 B` | `+5.73%` | `163.8` | `13.28` / `-` |
| `v2-size-nodict` (net) | `13,445,848 B` | `-` | mono | `0 B` | baseline | `608.4` | `23.01` / `-` |
| `v2-size-dictprof` (net) | `13,445,848 B` | `-` (rejected) | mono | `0 B` | `0.00%` | `1118.4` | `28.66` / `-` |
| `v3-fixed-128k-nobcj-nodict` | `14,120,761 B` | `0` / `17,169` | `265 / 267` | `262,144 B` | `+5.02%` | `273.8` | `26.86` / **`12.58`** |
| `v3-fixed-128k-bcj-nodict` | `13,352,257 B` | `0` / `17,217` | `265 / 267` | `262,144 B` | `-0.70%` | `297.6` | `38.28` / `24.88` |
| `v3-fixed-512k-bcj-dict` | `13,038,554 B` | `0` / `4,641` | `69 / 69` | `0 B` | `-3.03%` | `322.0` | `37.90` / `25.09` |
| `v3-cdc-64k-bcj-dict` | `12,292,140 B` | `0` / `24,177` | `361 / 414` | `2,966,864 B` | **`-8.58%`** | `314.8` | `39.46` / `25.10` |
| `v3-cdc-128k-nobcj-nodict` | `13,006,293 B` | `0` / `14,097` | `209 / 243` | `2,643,931 B` | **`-3.27%`** | `291.1` | `26.10` / **`12.46`** |
| `v3-cdc-128k-bcj-nodict` | **`12,187,709 B`** | `0` / `14,081` | `208 / 242` | `2,643,931 B` | **`-9.36%`** | `328.9` | `38.51` / `24.98` |

### Key Technical Observations from Track B2

1. **Both techniques are essential together**: Fixed-block CDC without BCJ (`+4.14%` to `+5.02%`) actually *regresses* size compared to monolithic Zstd because block splits lose LZ77 long-range history across 128 KiB boundaries; adding `BCJ+RIP` + section-aligned FastCDC flips that into a **`-3.33%` to `-9.36%`** net win AND cuts packing wall time by ~2–3× via block-level concurrency.
2. **Decoder speedup without sequential post-pass**: Parallel `4w` Zstd block decode directly into `memfd` without BCJ unfiltering runs **1.9×–2.5× faster** (`4.91 ms` vs `12.27–15.50 ms` on `demo-stripped`; `12.46 ms` vs `23.01–28.66 ms` on `microfat-stripped`).
3. **Identified Lab Bottleneck — Sequential BCJ Unfilter**: Our prototype currently runs `ApplyELFFilter(..., false)` sequentially on an in-memory buffer *after* assembling blocks. On `microfat-stripped` (`11.6 MiB`), that adds `~12.5 ms` (`24.98 ms` vs `12.46 ms`). Fixing this is straightforward: push per-extent `(ExecRange intersection, section_base_offset)` into worker decode buffers before `unix.Pwrite` (tracked as open lab follow-up).
4. **Shared Zstd Dict (`dict=0` in `v3bench`)**: In `trainBlockDict`, `zstd.BuildDict` with empty `History` returned `nil` on block inputs, so all reported v3 wins come strictly from BCJ+RIP and section-aligned CDC dedup, leaving block-shared dictionary training as additional future headroom.

---

## 4. Track B3 — Ultra-Lean Rust Stub vs Go Stubs (`#497` & `#498`)

Experimental prototype [`experiments/v3lab/rust-stub/`](rust-stub/) (`microfat-stub-rs`) packed alongside real `demo-stripped` `v1`/`v2`/`v3` ELF payloads via `bin/microfat pack`.

### Binary Footprint & Linkage

| Launcher Stub | File Size (Bytes) | `.text` / `.data` / `.bss` (`size`) | Linkage (`ldd`) | Host Selection & Verify Parity |
| :--- | :---: | :---: | :--- | :--- |
| **`bin/microfat-stub`** (Go full) | `3,940,512 B` (`3.76 MiB`) | `3,839,067 / 92,339 / 33,799,016` | Static (`CGO_ENABLED=0`) | Baseline `v3` (`MICROFAT_EXEC_MODE=memfd`) |
| **`bin/microfat-stub-minimal`** (Go minimal) | `2,916,512 B` (`2.78 MiB`) | `2,835,037 / 71,251 / 33,798,240` | Static (`CGO_ENABLED=0`) | Baseline `v3` (`MICROFAT_EXEC_MODE=memfd`) |
| **`microfat-stub-rs`** (Rust prototype) | **`436,400 B`** (`426.2 KiB`) | **`414,480 / 17,864 / 3,882`** | Dynamic (`libc.so.6`, `libgcc_s.so.1`, `ld-linux-x86-64.so.2`) | Verified `v3` selection (skipped `v1`/`v2`/unsafe `v4`), `microfat verify` PASS |

### End-to-End Compatibility & Cold `memfd` Latency (`15` runs, `demo-stripped --help`, `MICROFAT_EXEC_MODE=memfd`)

All **12** fat binaries produced identical exit code `0` and identical stdout SHA-256 (`48ecd80e6b9b63edb0fcd0f2d2ddc7a821c87e427d1693a86b91d40893f3e45e`):

| Stub | Codec Mode | Total Fat Binary Size | Cold `memfd` Median (Min–Max) | Notes |
| :--- | :--- | :---: | :---: | :--- |
| Go `stub-full` (`3.94 MB`) | `none` | `16,937,998 B` | `6.30 ms` (`6.03–10.57`) | Uses Go hardware SHA-NI |
| Go `stub-full` | `lz4` | `11,511,190 B` | `9.60 ms` (`9.16–13.36`) | |
| Go `stub-full` | `zstd` | `9,539,968 B` | `8.87 ms` (`8.00–14.59`) | |
| Go `stub-full` | `zstd --dict` | `9,515,796 B` | `10.66 ms` (`8.64–16.08`) | |
| Go `stub-minimal` (`2.92 MB`) | `none` | `15,913,998 B` | **`6.22 ms`** (`5.92–9.54`) | Baseline size-vs-speed trade |
| Go `stub-minimal` | `lz4` | `10,487,190 B` | `9.19 ms` (`8.93–12.78`) | |
| Go `stub-minimal` | `zstd` | `8,515,968 B` | `9.42 ms` (`7.93–15.76`) | |
| Go `stub-minimal` | `zstd --dict` | `8,491,796 B` | `13.33 ms` (`8.73–23.98`) | |
| **Rust `microfat-stub-rs`** (`436.4 KB`) | `none` | **`13,433,886 B`** | `20.34 ms` (`20.09–21.56`) | Pure scalar SHA-256 @ `opt-level="z"` bottleneck! |
| **Rust `microfat-stub-rs`** | `lz4` | **`8,007,078 B`** | `28.61 ms` (`28.25–30.61`) | Scalar SHA-256 + `lz4_flex` @ `opt-level="z"` |
| **Rust `microfat-stub-rs`** | `zstd` | **`6,035,856 B`** | `49.53 ms` (`49.08–50.58`) | Pure-Rust `ruzstd` @ `opt-level="z"` |
| **Rust `microfat-stub-rs`** | `zstd --dict` | **`6,011,684 B`** (`-36.8%` vs full) | `49.82 ms` (`49.45–50.50`) | Full Format v2 dict compatibility proven |

### Actionable Diagnosis Linking `#497` and `#498`

Benchmarking `microfat-stub-rs` directly quantified the `#498` bottleneck hypothesis:
1. On uncompressed `none` (`4.33 MiB` payload), Go `stub-minimal` executes cold `memfd` in `6.22 ms` because Go's `crypto/sha256` uses hardware **x86 SHA-NI** instructions; our size-optimized `opt-level = "z"` Rust stub uses scalar software SHA-256 without loop unrolling, spending **`~14 ms` solely hashing `4.33 MiB`** (`20.34 ms` total).
2. On `zstd`, pure-Rust `ruzstd` compiled at `opt-level = "z"` adds another `~29 ms` over `none` (`49.53 ms` vs Go `8.87 ms` using tuned `klauspost/compress/zstd`).
3. **Conclusion**: `microfat-stub-rs` achieves a dramatic **6.68× stub reduction** (`436 KB` vs `2.92 MB`, `-29.2%` to `-36.8%` on a real 3-variant CLI binary), proving the Rust stub footprint goal cleanly; **before promotion**, it needs runtime-dispatched x86 SHA-NI (tracking `#498`) and hot-path `opt-level=3` / faster Zstd kernels (tracking `#497`) to beat Go `stub-minimal` on cold-start wall latency as well as binary size.

---

## 5. ADR-0001 Promotion Gate Assessment

Per [ADR-0001 §3](../../docs/design/adr-0001-formal-verification-and-v3-lab.md#3-promotion--rollback-criteria), **production Format v2 and Go stubs remain the default in this PR**:

| Promotion Criterion | Format v3 Status | Rust Stub (`#497`) Status | Open Work to Promote |
| :--- | :---: | :---: | :--- |
| Complete-cost archive size win | **PASS** (`-3.33%` to `-9.36%` stripped vs best v2) | **PASS** (`-29.2%` vs minimal, `-36.8%` vs full) | Fix `zstd.BuildDict` history seeding in `v3container` for extra dictionary gain |
| Cold `memfd` & warm-cache latency | **Partial** (`2×` faster without BCJ; sequential post-BCJ slower) | **FAIL (`20–50 ms` vs `6–13 ms`)** | Fuse per-extent BCJ unfilter into `Pwrite` workers (v3); add x86 SHA-NI + `opt-level=3` crypto/Zstd paths + warm cache / `fs-verity` (`#498`) |
| Cross-arch & static ABI parity | **Partial** (x86-64 + ARM64 ELF filters implemented/tested; native bench x86-64 only) | **Partial** (dynamic glibc x86-64 only; `< 500 KiB`) | Static `x86_64-unknown-linux-musl` + `aarch64-unknown-linux-musl` builds, native ARM64 `AT_HWCAP` probing, cgroup tuning |
| Security & chaos parity | **PASS in prototype** (SHA-256 index/block/full-image checks + 4-seal `memfd`) | **Partial** (`AT_SECURE`/uid/gid + SHA-256 + 4-seal `memfd` present; full chaos harness not ported yet) | Port full `tests/e2e` chaos & warm-cache symlink/FIFO tests |
| Repository CI gate (`NO_COLOR=1 task all`) | **PASS** | **PASS** (Rust crate isolated under `experiments/v3lab/`) | Wire Rust/Kani/Lean/Gobra checks into optional/CI lab workflows if promoted |
