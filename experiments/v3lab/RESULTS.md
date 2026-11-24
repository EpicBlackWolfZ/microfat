# Post-`v0.4.1` Formal Verification & Compression Research Results (`v3lab`)

- **Topic Branches**: Phase 1 (`#502`), Phase 2 Tracks 1–3 (`#504`, `#505`, `#506`), `#507`, and `#501` (`533f6cb`, window-accurate Zstd budget + Gobra bounds) merged onto `origin/main`; `#499` (`perf/rust-stub-e2e-chaos-parity`) ports the full Go black-box `tests/e2e` chaos/security/codec/privilege/telemetry parity suite to `microfat-stub-rs` v0.4 and extends Kani to `17/17` harnesses
- **Design Decision & Promotion Gates**: [ADR-0001](../../docs/design/adr-0001-formal-verification-and-v3-lab.md)
- **Related Issues**: [#378](https://github.com/EpicBlackWolfZ/microfat/issues/378), [#497](https://github.com/EpicBlackWolfZ/microfat/issues/497), [#498](https://github.com/EpicBlackWolfZ/microfat/issues/498), [#499](https://github.com/EpicBlackWolfZ/microfat/issues/499), [#500](https://github.com/EpicBlackWolfZ/microfat/issues/500), [#501](https://github.com/EpicBlackWolfZ/microfat/issues/501)

This document summarizes all observed verification results, benchmarks, and promotion-gate assessments from the post-`v0.4.1` research tracks. Every figure below comes directly from artifacts and command runs on this machine; unverified production claims or absent/emulated hardware backends (no CUDA Toolkit / `nvrtc.h` for Bend GPU codegen; QEMU user-mode emulation rather than native ARM64 hardware timing, which was explicitly skipped without ARM64 hardware) are explicitly noted.

---

## 1. Track A — Multi-Toolchain Formal Verification Scorecard

| Toolchain | Version & Target Scope | Status | Verified Targets / Properties | Claim Boundaries & Assumptions |
| :--- | :--- | :--- | :--- | :--- |
| **Lean 4** | `v4.34.0` on mathematical specs & filter algebra ([`formal/lean/Microfat.lean`](../../formal/lean/Microfat.lean)) | **PASS** (`lake build`, 7 jobs, 0 warnings) | **34 theorems** across `FormatV2` (marshal/unmarshal round-trip, exact consumption, trailing-byte rejection, segment bounds/disjointness), `Microarch` (AMD64 rank iff, monotonicity, ARM64 DAG acyclicity, SIGILL freedom, unforced optimality), `CgroupMath` (overflow guard, retained limit, `GOMEMLIMIT` ceiling & monotonicity, worker admission sum, `zstdDecoderMemory` iff/ceiling/monotonicity), and `ElfFilter` (ARM64 BL/B/ADRP and x86-64 BCJ+RIP word/byte bijections plus chunk-local reassembly & multi-offset dedup soundness). Uses **only** `{propext, Classical.choice, Quot.sound}`; zero `sorry`, `sorryAx`, `native_decide`, or custom axioms. | Proves properties of the Lean model rather than directly compiling Go/Rust binaries; linked to shipped Go (`experiments/v3lab/compress/elffilter_test.go`) and Rust (`experiments/v3lab/rust-stub/src/elffilter.rs`) via shared `#eval` conformance vectors and `cargo test` / `go test -race`. |
| **Z3 (`QF_BV`)** | `v5.3.3` on 64-bit bitvector SMT-LIB2 ([`formal/smt/`](../../formal/smt/README.md)) | **PASS** | `format_v2_bounds.smt2`: `sat` (premise satisfiability) + 3× `unsat` (negated overflow, bounds, and overlap checks). `cgroup_memory.smt2`: 4× `sat` + 4× `unsat` (overflow guard, `RetainedMemoryLimit`, `GOMEMLIMIT` headroom/ceiling bounds, and `ZstdDecoderMemory` across all `2^64` words). | Models exact two's-complement 64-bit signed/unsigned arithmetic of the guards; independent of allocator/sysfs I/O. |
| **Gobra** | `0.1.0-SNAPSHOT` (`v26.09`, [#479](https://github.com/EpicBlackWolfZ/microfat/pull/479) + `#501`) directly on **production Go** [`internal/cgroup/memory.go`](../../internal/cgroup/memory.go) & [`internal/format/bounds.go`](../../internal/format/bounds.go) | **PASS** (`--overflow`, `Gobra found 0 errors.` on both) | Directly proves signed 64-bit overflow freedom and exact functional pre/postconditions on production `ExtractionMemory`, `RetainedMemoryLimit`, `ZstdDecoderMemory`, `checkDictionarySegmentBounds`, and `checkVariantSegmentBounds` via shared [`scripts/verify-gobra.sh`](../../scripts/verify-gobra.sh) (CI-gated in `ci.yml` + `release.yml`). | Scoped to `internal/cgroup/memory.go` & `internal/format/bounds.go` with [`verification/gobra/stubs/math/limits.gobra`](../../verification/gobra/stubs/math/limits.gobra) (`MaxInt64`); remaining sysfs/float helpers, frame-header I/O, and binary decoding remain covered by Go tests, Lean 4, and Z3. |
| **Bend 2** | `v2.0.40` (`--verdict`) on [`experiments/v3lab/bend/filter_proof.bend`](bend/filter_proof.bend) | **PASS** (`ALL PROOFS CHECK`, zero `@unsafe`) | Machine-checks pure bidirectional step & wraparound round-trip properties for normalized x86/ARM64 offset transforms inside pure Bend code. | Model-level check in Bend (`filter_proof.bend`); does not directly inspect Go ELF parsing. |
| **Kani** | `v0.68.0` standalone (`CBMC 6.11.0`, `nightly-2026-08-21`) directly on **Rust stub core** [`experiments/v3lab/rust-stub/src/lib.rs`](rust-stub/src/lib.rs) | **PASS** (`17/17 harnesses`, 0 failures) | Verifies compiled `#![no_std]` `#![forbid(unsafe_code)]` Rust code directly: `verify_arm64_step_word_bijection` (`0.16s`), `verify_x86_step_bijection` (`1.46s`), `verify_x86_transform_roundtrip_8` (`40.08s`), `verify_x86_chunk_local_split_roundtrip` (`174.07s`), `verify_arm64_chunk_local_split_roundtrip` (`1.07s`), `verify_evaluate_amd64_exhaustive` (`0.08s`, `2^21` masks), `verify_evaluate_arm64_dag_exhaustive` (`0.21s`, `2^24` masks), `verify_trailer_never_panics_and_enforces_exact_layout` (`0.73s`), `verify_cursor_primitives_safe` (`10.42s`), `verify_decode_hex_digest_no_panic` (`4.30s`), `verify_segment_bounds_disjointness_and_selection` (`47.63s`, `1..=4` variants across AMD64 + ARM64 DAG), `verify_policy_selection_sigill_free` (`0.66s`), `verify_extraction_memory_matches_math_spec` (`0.18s`), `verify_retained_memory_limit_exact` (`0.09s`), `verify_zstd_decoder_memory_matches_math_spec` (`0.32s`), `verify_gomemlimit_no_overflow_and_bounds` (`25.10s`), and `verify_gomaxprocs_safe_and_positive` (`2.62s`). | Checks Rust core under explicit symbolic bounds (`len <= 8` for multi-step x86 filter, `1..=4` variants for segment layout, `<= 64B` cursor slice); full arbitrarily-long x86 stream & chunk-local bijections and unbounded cgroup `gomemlimit_monotone` are proven in Lean 4 and Z3 (`gomemlimit` monotonicity is also swept via unit test `gomemlimit_is_monotone_across_container_sizes`, while basis-point `u64` scaling keeps `compute_gomemlimit` CBMC-tractable). Hardware SHA-NI intrinsics ([`sha256_hw.rs`](rust-stub/src/sha256_hw.rs)), host probes ([`cpuid.rs`](rust-stub/src/cpuid.rs)), and Linux syscalls ([`main.rs`](rust-stub/src/main.rs)) are verified via unit/differential tests and full black-box Go E2E/QEMU checks rather than Kani models. |

### Alignment with `origin/main` Hardening (`#479`, `#501`, `#502`, `#504`, `#505`, `#506`, `#507`)

`origin/main` already hardened [`internal/format/format.go`](../../internal/format/format.go), merged direct production Gobra verification of [`internal/cgroup/memory.go`](../../internal/cgroup/memory.go) via [#479](https://github.com/EpicBlackWolfZ/microfat/pull/479), integrated Phase 1 + Phase 2 Tracks 1–3 (`#502`, `#504`, `#505`, `#506`), stabilized CI (`#507`), and merged [#501](https://github.com/EpicBlackWolfZ/microfat/pull/508) (`533f6cb`) upgrading production [`cmd/microfat-stub/`](../../cmd/microfat-stub/) with inspected per-frame Zstd window budgeting (`ZstdDecoderMemory` + `InspectZstdFrameWindow` + `DecompressWithDictAndWindow`), extending mandatory Gobra verification to [`internal/format/bounds.go`](../../internal/format/bounds.go), and enabling [`.github/workflows/formal-lab.yml`](../../.github/workflows/formal-lab.yml).

On `perf/rust-stub-e2e-chaos-parity` ([#499](https://github.com/EpicBlackWolfZ/microfat/issues/499)), we port the full black-box Go [`tests/e2e/`](../../tests/e2e/) chaos/security/codec/privilege/telemetry parity suite into [`tests/e2e/rust_stub_parity_test.go`](../../tests/e2e/rust_stub_parity_test.go), wire it into `task test-rust-stub-e2e` and `.github/workflows/formal-lab.yml`, upgrade `microfat-stub-rs` to v0.4 so all 10 suites pass red-to-green (`8` failed -> `10/10` passed), and extend Kani to `17/17` harnesses (`verify_zstd_decoder_memory_matches_math_spec`), completing all technical prerequisites for ADR-0001 promotion review.

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

## 3. Track B2 — Format v2 vs Format v3 Lab Corpus Benchmark (`#500`)

Built from real `CGO_ENABLED=0` Go binaries (`./examples/demo` and `./cmd/microfat`, stripped `-s -w` and unstripped) across **AMD64 (`GOAMD64=v1|v2|v3`)** and **ARM64 (`GOARM64=v8.0|v8.1|v8.2`)**. Format v2 controls use `internal/pack.Pack` at `balanced` and `size` (`zstd:best`) with `net` excluding stub bytes; Format v3 prototypes use [`experiments/v3lab/compress/`](compress/) (`ChunkFixed` and `ChunkSectionCDC` aligned to instruction-step boundaries via `SnapX86FilterCuts` / 4-byte ARM64 alignment, per-extent parallel Zstd decode + L1-cache unfilter fused directly before `unix.Pwrite` into 4-seal `memfd`, stratified `zstd.BuildDict` history seeding with optional `DictProfitable` gating, and full-image SHA-256 verification).

> **Architecture Measurement Scope**: All AMD64 and ARM64 archive sizes, ratios, dedup savings, and pack times come from real cross-compiled Go ELF binaries. In-memory `memfd` decode timings for both corpora ran on the 32-CPU x86-64 host; native ARM64 decode latency still requires an ARM64 runner.

### Summary Across All 8 Targets (Track 2 Updated Matrix)

| Corpus Target (Raw Variant Sum) | Best v2 (`size-dictprof`) Net Bytes (Ratio) | Best v3 Config | v3 Total Archive Bytes (Ratio) | v3 Size Delta vs Best v2 | Best v2 Cold Decode (`1w`) | Best v3 Decode (`4w` fast / smallest) |
| :--- | :---: | :--- | :---: | :---: | :---: | :---: |
| **`demo-stripped` (amd64)** (`12,997,088 B`) | `5,294,443 B` (`40.74%`) | `v3-cdc-64k-bcj-dict` | **`5,079,972 B`** (`39.09%`) | **`-4.05%`** | `6.83 ms` (`balanced`) / `10.85 ms` (`size-nodict`) | **`6.48 ms`** (`128k-bcj`) / `7.91 ms` (`64k-dict`) |
| **`demo-unstripped` (amd64)** (`19,251,685 B`) | `10,719,551 B` (`55.68%`) | `v3-cdc-64k-bcj-nodict` | **`10,560,409 B`** (`54.85%`) | **`-1.48%`** | `8.77 ms` (`size-dict`) | **`8.47 ms`** (`128k-bcj`) / `8.64 ms` (`64k-bcj`) |
| **`microfat-stripped` (amd64)** (`34,775,520 B`) | `13,447,225 B` (`38.67%`) | `v3-cdc-128k-bcj-dict` | **`12,122,493 B`** (`34.86%`) | **`-9.85%`** | `24.96 ms` (`balanced`) / `28.08 ms` (`size`) | **`17.35 ms`** (`128k-bcj`) / `20.21 ms` (`dictprof`) |
| **`microfat-unstripped` (amd64)** (`50,822,481 B`) | `27,335,076 B` (`53.79%`) | `v3-cdc-128k-bcj-dict` | **`25,925,597 B`** (`51.01%`) | **`-5.16%`** | `21.20 ms` (`size-dict`) | `22.17 ms` (`128k-bcj`) / `25.40 ms` (`dict`) |
| **`arm64-demo-stripped`** (`12,190,176 B`) | `4,681,369 B` (`38.40%`) | `v3-cdc-128k-bcj-nodict` | **`3,115,389 B`** (`25.56%`) | **`-33.45%`** | `6.30 ms` (`balanced`) / `7.76 ms` (`size`) | **`4.87 ms`** (`128k-bcj`, x86-64 host) |
| **`arm64-demo-unstripped`** (`17,994,814 B`) | `9,688,561 B` (`53.84%`) | `v3-cdc-128k-bcj-nodict` | **`6,485,494 B`** (`36.04%`) | **`-33.06%`** | `7.73 ms` (`balanced`) / `14.64 ms` (`size-dict`) | **`6.59 ms`** (`128k-bcj`, x86-64 host) |
| **`arm64-microfat-stripped`** (`31,654,368 B`) | `11,914,962 B` (`37.64%`) | `v3-cdc-128k-bcj-dict` | **`7,733,413 B`** (`24.43%`) | **`-35.09%`** | `15.98 ms` (`balanced`) / `20.24 ms` (`size-dict`) | `12.07 ms` (`nodict`) / `15.07 ms` (`dict`, x86-64 host) |
| **`arm64-microfat-unstripped`** (`46,557,082 B`) | `24,688,891 B` (`53.03%`) | `v3-cdc-128k-bcj-dict` | **`16,239,979 B`** (`34.88%`) | **`-34.22%`** | `21.84 ms` (`size-dict`) / `24.88 ms` (`balanced`) | `16.65 ms` (`nodict`) / `19.56 ms` (`dict`, x86-64 host) |

### Full Stripped Matrix (`microfat-stripped` AMD64 & ARM64)

#### AMD64 `microfat-stripped` (`v1=11,600,032 B`, `v2=11,591,840 B`, `v3=11,583,648 B`, `raw_sum=34,775,520 B`)

| Variant | Archive Bytes | Dict / Index Bytes | Unique / Total Blocks | Dedup Raw Saved | Delta vs `v2-size` | Pack (`ms`) | Cold `memfd` `1w` / `4w` (`ms`) |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| `v2-balanced` (net) | `14,217,421 B` | `-` | mono | `0 B` | `+5.73%` | `152.1` | `24.96` / `-` |
| `v2-size-nodict` (net) | `13,447,225 B` | `-` | mono | `0 B` | baseline | `575.7` | `28.08` / `-` |
| `v2-size-dictprof` (net) | `13,447,225 B` | `-` (rejected) | mono | `0 B` | `0.00%` | `1086.5` | `28.45` / `-` |
| `v3-fixed-128k-nobcj-nodict` | `14,245,958 B` | `0` / `17,532` | `267 / 267` | `0 B` | `+5.94%` | `253.8` | `26.17` / **`12.42`** |
| `v3-fixed-128k-bcj-nodict` | `13,473,979 B` | `0` / `18,498` | `273 / 273` | `0 B` | `+0.20%` | `456.0` | `45.18` / `17.04` |
| `v3-fixed-128k-bcj-dict` | `13,295,291 B` | `110,749` / `18,542` | `273 / 273` | `0 B` | `-1.13%` | `1224.7` | `57.77` / `20.38` |
| `v3-fixed-512k-bcj-dictprof` | `12,991,972 B` | `110,750` / `5,276` | `75 / 75` | `0 B` | `-3.39%` | `1128.4` | `56.67` / `20.94` |
| `v3-cdc-64k-bcj-nodict` | `12,335,926 B` | `0` / `25,245` | `361 / 412` | `2,883,217 B` | `-8.26%` | `458.4` | `47.90` / `17.63` |
| `v3-cdc-64k-bcj-dict` | `12,201,712 B` | `110,750` / `25,289` | `361 / 412` | `2,883,217 B` | `-9.26%` | `1379.8` | `58.10` / `20.46` |
| `v3-cdc-128k-nobcj-nodict` | `13,014,997 B` | `0` / `14,518` | `212 / 245` | `2,641,380 B` | `-3.21%` | `268.1` | `26.12` / **`12.52`** |
| `v3-cdc-128k-bcj-nodict` | `12,185,415 B` | `0` / `14,642` | `208 / 241` | `2,641,380 B` | `-9.38%` | `465.5` | `45.57` / **`17.35`** |
| `v3-cdc-128k-bcj-dict` | **`12,122,493 B`** | `110,751` / `14,686` | `208 / 241` | `2,641,380 B` | **`-9.85%`** | `1104.9` | `58.40` / `20.51` |
| `v3-cdc-128k-bcj-dictprof` | **`12,122,493 B`** | `110,751` / `14,686` | `208 / 241` | `2,641,380 B` | **`-9.85%`** | `1295.8` | `57.20` / `20.21` |

#### ARM64 `arm64-microfat-stripped` (`v8.0=v8.1=v8.2=10,551,456 B`, `raw_sum=31,654,368 B`)

| Variant | Archive Bytes | Dict / Index Bytes | Unique / Total Blocks | Dedup Raw Saved | Delta vs `v2-size` | Pack (`ms`) | Host `memfd` `1w` / `4w` (`ms`) |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| `v2-balanced` (net) | `12,696,979 B` | `-` | mono | `0 B` | `+6.56%` | `142.9` | `15.98` / `-` |
| `v2-size-nodict` (net) | `11,914,962 B` | `-` | mono | `0 B` | baseline | `549.5` | `27.10` / `-` |
| `v2-size-dictprof` (net) | `11,914,962 B` | `-` (rejected) | mono | `0 B` | `0.00%` | `1012.2` | `20.24` / `-` |
| `v3-fixed-128k-nobcj-nodict` | `8,439,800 B` | `0` / `12,042` | `161 / 243` | `10,617,152 B` | `-29.17%` | `197.5` | `24.18` / `11.33` |
| `v3-fixed-128k-bcj-nodict` | `8,020,751 B` | `0` / `12,828` | `165 / 249` | `10,744,128 B` | `-32.68%` | `239.0` | `27.12` / `12.02` |
| `v3-fixed-512k-bcj-dictprof` | `8,189,795 B` | `0` (rejected) / `3,828` | `48 / 69` | `9,579,268 B` | `-31.26%` | `691.5` | `26.88` / `12.42` |
| `v3-cdc-64k-bcj-dict` | `7,781,406 B` | `110,755` / `19,827` | `252 / 400` | `11,432,312 B` | `-34.69%` | `899.4` | `39.00` / `15.36` |
| `v3-cdc-128k-nobcj-nodict` | `8,144,555 B` | `0` / `11,183` | `147 / 232` | `11,117,093 B` | `-31.64%` | `230.4` | `23.94` / **`11.55`** |
| `v3-cdc-128k-bcj-nodict` | `7,769,620 B` | `0` / `11,348` | `144 / 229` | `11,117,093 B` | `-34.79%` | `250.5` | `27.16` / **`12.07`** |
| `v3-cdc-128k-bcj-dict` | **`7,733,413 B`** | `110,754` / `11,392` | `144 / 229` | `11,117,093 B` | **`-35.09%`** | `712.0` | `38.51` / `15.07` |
| `v3-cdc-128k-bcj-dictprof` | **`7,733,413 B`** | `110,754` / `11,392` | `144 / 229` | `11,117,093 B` | **`-35.09%`** | `855.0` | `38.62` / `15.44` |

### Key Technical Observations from Track B2 (`#500`)

1. **Fused Per-Extent Unfiltering Cuts BCJ+RIP Decode Latency by ~30%**: Moving unfiltering from a sequential post-assembly `pread`/`pwrite` pass directly into L1-resident worker buffers cut `microfat-stripped` `v3-cdc-128k-bcj-nodict` `decode4w` from **`24.98 ms`** (Phase 1) down to **`17.35 ms`** (`demo-stripped` dropped from `9.30 ms` to **`6.48 ms`**) — beating every single Format v2 control (`24.96–28.45 ms` and `6.83–14.31 ms`).
2. **Stratified `zstd.BuildDict` Seeding & `DictProfitable` Tradeoff**: Passing stratified history samples (`64 B..108 KiB` bounded before `zstd.BuildDict`, with 128 KiB sample splitting) reliably builds ~`110.7 KiB` dictionaries (`demo-stripped cdc-64k`: `-4.05%`; `microfat-stripped cdc-128k`: **`-9.85%`**, `12,122,493 B`), while `DictProfitable` rejects non-profitable dicts cleanly (`demo-stripped cdc-128k`, `arm64-demo-stripped cdc-128k`, `fixed-512k`). Note that attaching a shared Zstd dictionary adds ~`1.3–3.3 ms` decode time per invocation, keeping `-bcj-nodict` as the latency-optimal preset and `-bcj-dictprof` as the size-optimal preset.
3. **ARM64 Dedup + `BL`/`B`/`ADRP` Filter**: Across `GOARM64=v8.0/v8.1/v8.2`, section-aligned FastCDC deduplication plus 4-byte-aligned ARM64 `BL`/`B`/`ADRP` normalization cuts archive payload by **`-33.06%` to `-35.09%`** vs best Format v2, with the ARM64 filter itself saving `374,935 B` (`-4.6%`) on `arm64-microfat-stripped` (`7,769,620 B` vs `8,144,555 B` `nobcj`) at ~`0.5 ms` additional decode cost (`12.07 ms` vs `11.55 ms` on host).

---

## 4. Track B3 — Ultra-Lean Rust Stub vs Go Stubs (`#497`, `#498`, `#499`)

Experimental prototype [`experiments/v3lab/rust-stub/`](rust-stub/) (`microfat-stub-rs`) packed alongside real `demo-stripped` Go ELF payloads via `bin/microfat pack`.

In **v0.3 (`#504`/`#506`)** and **v0.4 (`#499`, `perf/rust-stub-e2e-chaos-parity`)**, `microfat-stub-rs` upgrades from the `libstd` + `ruzstd` v0.2 prototype (`#504`) to full production-contract parity:
- **`#![no_std]` + `#![no_main]` binary (`LibcAllocator`, direct C `main`, `rust-lld` `--gc-sections` + `--icf=safe`)**: Eliminates precompiled `libstd` unwind/backtrace/DWARF bloat (`223,906 B` identified via `nm`), cutting static `x86_64-unknown-linux-musl` size by **`2.93×`** (`540,864 B` -> `165,360 B` in v0.3 -> **`184,448 B`** in v0.4) and static `aarch64-unknown-linux-musl` by **`2.99×`** (`478,512 B` -> `140,416 B` in v0.3 -> **`160,040 B`** in v0.4) with zero `core::fmt` formatting bloat and zero external C cross-compiler ([`.cargo/config.toml`](rust-stub/.cargo/config.toml), [`Cargo.toml`](rust-stub/Cargo.toml), [`src/cpuid.rs`](rust-stub/src/cpuid.rs), [`src/main.rs`](rust-stub/src/main.rs)).
- **Fast `#![no_std]` Codecs (`[profile.release.package.*] opt-level = 3`)**: Replaces `ruzstd` with pure-Rust **`zrip-decode 0.8.18`** (`PushDecoder` streaming + frame-header window checks) and adds a zero-copy `mmap`ped **LZ4 frame decoder** (`lz4_flex 0.14.2` + `twox-hash 2.1.5` xxHash32, handling independent/linked/uncompressed blocks, content checksums, skippable frames, and legacy frames).
- **Hardened Dispatch, Cold Cache Materialization & `auto` Parity**: Checks `security.capability` extended attributes alongside `AT_SECURE`/uid/gid, passes `MFD_EXEC` with `EINVAL`-only retry, drops writable mappings before `F_ADD_SEALS`, materializes cold cache entries atomically (`getrandom` temp suffix, `openat(O_CREAT|O_EXCL, 0o700)`, `fchmod`, `fsync`, `renameat`, temp cleanup guard), preserves untouched safe corrupt regular files during successful `memfd` runs, treats absent or insecure (`0o777`) cache directories as a cold miss falling through to `memfd` in `auto` mode while failing closed on unsafe entries (`fifo`/`socket`/`symlink`/`directory`/group- or other-writable/`setuid`), and rejects `> 128 MiB` Zstd frame windows unconditionally (propagating inspected window caps into `ZstdPushDecoder::max_window` and charging Kani-verified `zstd_decoder_memory`).
- **v0.4 Telemetry, Policy, Runtime Info & Minimal Meta-Command Refusal (`#499`)**: Porting the full Go `tests/e2e` suite (`8` failed, `2` passed on v0.3 -> `10/10` passed on v0.4) added minimal-profile `--microfat:*` refusal on `argv[1]`, `MICROFAT_POLICY=safe_avx512`/`no_downclock` (Skylake-X / Cascade Lake Family 6 Model 85 CPUID check) + `MICROFAT_OVERRIDE_REASON`, zero-`core::fmt` `MICROFAT_DEBUG`/`MICROFAT_LOG=json` dispatch & `[microfat] error:` stderr/JSON telemetry, 20-key hostile internal `MICROFAT_*` env scrubbing across `execve`, `MICROFAT_CPU_POLICY=native`/`quota`, `MICROFAT_GC_PROFILE` / `GOGC` resolution, and ARM64 `MICROFAT_HOST_LEVEL` formatting.

### Binary Footprint & Linkage

| Launcher Stub | File Size (Bytes) | `.text` / `.data` / `.bss` (`size`) | Linkage | Notes |
| :--- | :---: | :---: | :--- | :--- |
| **`bin/microfat-stub`** (Go full x86-64) | `3,940,512 B` (`3.76 MiB`) | `3,839,067 / 92,339 / 33,799,016` | Static (`CGO_ENABLED=0`) | Production baseline (`#502` `size`) |
| **`bin/microfat-stub-minimal`** (Go minimal x86-64) | `2,916,512 B` (`2.78 MiB`) | `2,835,037 / 71,251 / 33,798,240` | Static (`CGO_ENABLED=0`) | Production minimal baseline (`#502` `size`) |
| Go `stub-minimal` (cross `arm64`) | `2,752,672 B` (`2.63 MiB`) | `-` | Static (`CGO_ENABLED=0 GOOS=linux GOARCH=arm64`) | ARM64 QEMU control |
| `microfat-stub-rs` **v0.1** (`#502`, glibc x86-64) | `436,400 B` (`426.2 KiB`) | `414,480 / 17,864 / 3,882` | Dynamic (`libc.so.6`, `libgcc_s.so.1`, `ld-linux`) | Prior dynamic baseline (`ruzstd` scalar) |
| `microfat-stub-rs` **v0.2** (`#504`, `x86_64-musl`) | `540,864 B` (`528.2 KiB`) | `524,105 / 3,472 / 4,184` | Static (`x86_64-unknown-linux-musl`, `std`) | Prior static baseline (`ruzstd @ opt=3`) |
| `microfat-stub-rs` **v0.2** (`#504`, `aarch64-musl`) | `478,512 B` (`467.3 KiB`) | `461,094 / 15,368 / 5,017` | Static (`aarch64-unknown-linux-musl`, `std`) | Prior ARM64 static baseline |
| `microfat-stub-rs` **v0.3** (`x86_64-musl`) | `165,360 B` (`161.5 KiB`) | `163,322 / 196 / 5,504` | Static (`x86_64-unknown-linux-musl`, `#![no_std]`, `rust-lld`) | Pre-E2E-parity `#![no_std]` baseline |
| `microfat-stub-rs` **v0.3** (`aarch64-musl`) | `140,416 B` (`137.1 KiB`) | `138,500 / 148 / 1,648` | Static (`aarch64-unknown-linux-musl`, `#![no_std]`, `rust-lld`) | Pre-E2E-parity ARM64 baseline |
| **`microfat-stub-rs` v0.4 (`#499`, `x86_64-musl`)** | **`184,448 B`** (`180.1 KiB`) | **`182,394 / 208 / 2,816`** | **Static** (`x86_64-unknown-linux-musl`, `#![no_std]`, `rust-lld`) | Full E2E chaos/telemetry/policy parity (`< 200 KiB` gate); **`15.8×`** smaller than Go `stub-minimal` (`2.92 MB`) |
| **`microfat-stub-rs` v0.4 (`#499`, `aarch64-musl`)** | **`160,040 B`** (`156.3 KiB`) | **`158,104 / 160 / 2,520`** | **Static** (`aarch64-unknown-linux-musl`, `#![no_std]`, `rust-lld`) | Full ARM64 parity (`< 200 KiB` gate); **`17.2×`** smaller than Go `stub-minimal` ARM64 (`2.75 MB`) |

### x86-64 Paired 15-Run End-to-End Latency & Archive Size (`demo-stripped --help`)

Measured side-by-side on idle host CPU across 15 runs per cell (`MICROFAT_CACHE_DIR` isolated per mode); all invocations returned exit code `0` and identical stdout SHA-256 (`48ecd80e6b9b63ed`):

| Stub | Codec / Dispatch Mode | Total Fat Binary Size | v0.2 Median (`#504`) | v0.3 Median (Min–Max) | Delta vs Go `stub-minimal` / v0.2 |
| :--- | :--- | :---: | :---: | :---: | :--- |
| Go `stub-full` (`3.94 MB`) | `none` (cold `memfd`) | `16,937,998 B` | `6.03 ms` | `6.01 ms` (`5.70–6.72`) | Reference |
| Go `stub-full` | `lz4` (cold `memfd`) | `10,646,369 B` | `9.07 ms` | `9.38 ms` (`8.96–9.75`) | Reference |
| Go `stub-full` | `zstd` (cold `memfd`) | `9,250,201 B` | `9.42 ms` | `8.90 ms` (`7.46–13.69`) | Reference |
| Go `stub-full` | `zstd --dict` (cold `memfd`) | `9,234,934 B` | `11.92 ms` | `12.02 ms` (`9.07–15.80`) | Reference |
| Go `stub-full` | `cold-cache` (`zstd`) | `9,250,201 B` | `-` | `11.14 ms` (`10.36–14.20`) | Reference |
| Go `stub-full` | `warm-auto` (`zstd` cache hit) | `9,250,201 B` | `4.98 ms` | `4.90 ms` (`4.68–5.14`) | Reference |
| Go `stub-minimal` (`2.92 MB`) | `none` (cold `memfd`) | `15,913,998 B` | `6.00 ms` | `6.03 ms` (`5.71–6.48`) | Baseline |
| Go `stub-minimal` | `lz4` (cold `memfd`) | `9,622,369 B` | `9.19 ms` | `9.60 ms` (`9.20–16.10`) | Baseline |
| Go `stub-minimal` | `zstd` (cold `memfd`) | `8,226,201 B` | `9.36 ms` | `8.24 ms` (`7.64–12.87`) | Baseline |
| Go `stub-minimal` | `zstd --dict` (cold `memfd`) | `8,210,934 B` | `11.72 ms` | `10.83 ms` (`8.71–16.68`) | Baseline |
| Go `stub-minimal` | `cold-cache` (`zstd`) | `8,226,201 B` | `-` | `11.34 ms` (`10.09–15.95`) | Baseline |
| Go `stub-minimal` | `warm-auto` (`zstd` cache hit) | `8,226,201 B` | `5.08 ms` | `4.77 ms` (`4.55–5.25`) | Baseline |
| **Rust `microfat-stub-rs` v0.3 (`165,360 B`)** | `none` (cold `memfd`) | **`13,162,846 B`** | `5.30 ms` | **`5.77 ms`** (`5.66–5.96`) | **Beats Go `stub-minimal` (`6.03 ms`) & `stub-full` (`6.01 ms`)**; `-17.3%` bytes vs `gom` |
| **Rust `microfat-stub-rs` v0.3** | `lz4` (cold `memfd`) | **`6,871,217 B`** | `15.52 ms` | **`8.01 ms`** (`7.79–8.44`) | **`-16.6%` faster than Go `stub-minimal` (`9.60 ms`)**; **`1.94×` faster than v0.2**; **`-28.6%` bytes vs `gom`** |
| **Rust `microfat-stub-rs` v0.3** | `zstd` (cold `memfd`) | **`5,475,049 B`** | `26.36 ms` | **`12.06 ms`** (`11.82–12.57`) | **`2.19×` faster than v0.2**; **`-33.4%` bytes vs `gom`** (`+3.82 ms` vs Go `zstd` asm) |
| **Rust `microfat-stub-rs` v0.3** | `zstd --dict` (cold `memfd`) | **`5,459,782 B`** | `26.24 ms` | **`12.12 ms`** (`11.89–12.54`) | **`2.17×` faster than v0.2**; **`-33.5%` bytes vs `gom`**, **`-40.9%` vs `gof`** |
| **Rust `microfat-stub-rs` v0.3** | `cold-cache` (`zstd`) | `5,475,049 B` | `-` | **`14.82 ms`** (`14.66–15.23`) | Atomic `0700` materialization (`fsync` + `renameat`) |
| **Rust `microfat-stub-rs` v0.3** | `warm-auto` (`zstd` cache hit) | `5,475,049 B` | `4.02 ms` | **`4.07 ms`** (`3.90–4.22`) | **`-14.7%` faster than Go `stub-minimal` (`4.77 ms`)**; **`-16.9%` vs `stub-full` (`4.90 ms`)** |

### v0.4 Paired 15-Run Latency Sanity Check (`#499`, Same-Session Host Control)

Measured side-by-side (`15` runs per cell on `demo-stripped --help`, `bin/microfat-stub-minimal` at `2,928,800 B` vs `microfat-stub-rs` v0.4 at `184,448 B`); all invocations exited `0` with identical stdout SHA-256 (`48ecd80e6b9b63ed`):

| Codec / Dispatch Mode | Go `stub-minimal` Size & Median (Min–Max) | `microfat-stub-rs` v0.4 Size & Median (Min–Max) | Delta vs Go `stub-minimal` |
| :--- | :---: | :---: | :--- |
| `none` (cold `memfd`) | `15,926,286 B` \| `6.67 ms` (`6.33–7.50`) | **`13,181,934 B`** \| **`6.22 ms`** (`6.05–6.79`) | **`-0.45 ms`** (`-6.7%` faster); `-17.2%` fat bytes |
| `lz4` (cold `memfd`) | `10,499,476 B` \| `9.57 ms` (`9.13–10.57`) | **`7,755,124 B`** \| **`8.23 ms`** (`7.83–12.26`) | **`-1.34 ms`** (`-14.0%` faster); `-26.1%` fat bytes |
| `zstd` (cold `memfd`) | `8,238,489 B` \| `8.54 ms` (`7.98–11.92`) | **`5,494,137 B`** \| **`12.76 ms`** (`12.26–13.71`) | `+4.22 ms` vs Go asm; **`-33.3%` fat bytes** |
| `zstd --dict` (cold `memfd`) | `8,223,227 B` \| `9.84 ms` (`8.57–16.08`) | **`5,478,875 B`** \| **`12.70 ms`** (`12.47–15.61`) | `+2.86 ms` vs Go asm; **`-33.4%` fat bytes** |
| `cold-cache` (`zstd`) | `8,238,489 B` \| `11.47 ms` (`10.46–15.24`) | **`5,494,137 B`** \| **`15.00 ms`** (`14.83–15.84`) | Atomic `0700` materialization (`fsync` + `renameat`) |
| `warm-auto` (`zstd` hit) | `8,238,489 B` \| `5.20 ms` (`4.87–5.91`) | **`5,494,137 B`** \| **`4.18 ms`** (`4.02–4.54`) | **`-1.02 ms`** (`-19.6%` faster) |

### Size/Speed Pareto Study (`zrip-decode` Configuration Sweep -> `#![no_std]` Breakthrough)

To hit both `< 500 KiB` static stub size and Go-competitive Zstd speed with zero C cross-compilers, we swept `zrip-decode` profiles on `x86_64-unknown-linux-musl` before isolating the `libstd` bottleneck via `nm`:

| Variant (`x86_64-unknown-linux-musl`) | Stub Size | `zstd` Cold Median | `zstd --dict` Median | Diagnosis / Outcome |
| :--- | :---: | :---: | :---: | :--- |
| `std` + `zrip-decode` (`simd`, `opt-level = 3`) | `676,096 B` | `11.41 ms` | `11.50 ms` | Fast, exceeds `500 KiB` gate |
| `std` + `zrip-decode` (no-simd, `opt-level = 3`) | `573,664 B` | `11.84 ms` | `11.96 ms` | Still `> 500 KiB` |
| `std` + `zrip-decode` (no-simd, `opt-level = 2`, `lld --icf=safe`) | `565,248 B` (`569,568 B` default ld) | `11.80 ms` | `11.94 ms` | Minimal speed cost, still `> 500 KiB` |
| `std` + `zrip-decode` (no-simd, `opt-level = "s"`, `lld --icf=safe`) | `553,664 B` (`561,376 B` default ld) | `12.24 ms` | `12.30 ms` | `nm` showed `223,906 B` from `std`/`core`/`alloc` (`backtrace_rs`, `gimli`, `addr2line`, `miniz_oxide`, `libunwind`) |
| `std` + `zrip-decode` (no-simd, `opt-level = "z"`) | `565,472 B` | `25.49 ms` | `25.27 ms` | Binary `opt-level = "z"` de-optimizes codec hot loops unless overridden |
| **`#![no_std]` + `opt-level = "z"` stub + `opt-level = 3` codecs (`v0.3`)** | **`165,360 B`** | **`12.06 ms`** | **`12.12 ms`** | **Both gates beaten (`161.5 KiB` stub, `2.19×` faster than `ruzstd`)** |

### Live Chaos/Policy Matrix & Emulated ARM64 Verification

- **Unit, Chaos & Static Analysis (`x86_64` + QEMU `aarch64`)**: On v0.4 (`#499`), `cargo test` (host `x86_64`) and `cargo test --target aarch64-unknown-linux-musl` (QEMU `aarch64`) pass **`16/16`** `microfat_stub_core` tests + **`8/8`** `microfat-stub-rs` binary/chaos tests with **`0` warnings** under `cargo clippy --all-targets -- -D warnings` and `cargo clippy --target aarch64-unknown-linux-musl -- -D warnings`.
- **Full Black-Box Go E2E Parity Suite (`#499`, [`tests/e2e/rust_stub_parity_test.go`](../../tests/e2e/rust_stub_parity_test.go), `task test-rust-stub-e2e`)**:
  - **Red baseline on v0.3**: **`8` failed, `2` passed** (`TestRustStubProcessFidelityAndWhitespacePaths` and `TestRustStubRunningImageSurvivesDeploymentChange` passed; 8 failed on missing minimal-profile `--microfat:*` refusal, `auto`-mode insecure-directory behavior, missing `MICROFAT_DEBUG`/`MICROFAT_LOG=json` telemetry, `[microfat] error:` formatting, hostile internal `MICROFAT_*` env scrubbing, and `> 128 MiB` Zstd window capping without cgroups).
  - **Green on v0.4**: **`10/10` suites passed** (`go test -race -v ./tests/e2e -run '^TestRustStub' -count=1` with `MICROFAT_TEST_LEAKS=1` + `MICROFAT_RUST_STUB_TESTS=required`), covering minimal meta-command refusal, variant selection + `safe_avx512`/`FORCE_LEVEL`/`MAX_LEVEL`/`DISABLE_VARIANTS` policy, corruption/security boundaries + 256 MiB Zstd window rejection, `memfd` + real BPF seccomp cache fallback, `auto` read-only hit / cold-miss / all 7 unsafe cache entry attacks / 10-way concurrent race, codec handoff + SHA-256 hex casing matrices, process fidelity + whitespace paths + `MICROFAT_ORIGINAL_EXE`, `ptrace`-paused deployment survival (`unchanged`/`unlink`/`replace`), `unshare`/`setpriv`/`setcap` privileged rejection (`ordinary-root` pass, `setuid`/`setgid`/`capability` fail closed), and 11-mode telemetry + `runtimeinfo` parity.
- **ARM64 Static `musl` under `/usr/bin/qemu-aarch64-static` (`binfmt_misc`, Emulated — Non-Native)**:
  > **Environment Note**: Executed on x86-64 Linux via `/usr/bin/qemu-aarch64-static`; native ARM64 hardware benchmarking was explicitly skipped (no ARM64 hardware available locally).
  - **Multi-Variant Packaging & Verification**: Packed 4 ARM64 `demo-stripped` fat binaries with `microfat-stub-rs` v0.3 (`140,416 B` stub; all 4 passed `./bin/microfat verify`): `arm64-rs-none` (`12,330,996 B`), `arm64-rs-lz4` (`6,179,460 B`), `arm64-rs-zstd` (`4,831,493 B`), `arm64-rs-zstd-dict` (`4,821,762 B`).
  - **Dispatch & Cold-Cache Verification under QEMU**: Cold `MICROFAT_EXEC_MODE=cache` materialized a verified `0700` `4,063,392 B` cached ELF whose direct QEMU execution produced matching stdout SHA-256 `48ecd80e6b9b63ed`. `memfd`, `cold-cache`, and `warm-auto` modes completed verification/decompression and reached `/proc/self/fd/{11,12}` execution handoff at QEMU's documented `O_CLOEXEC`/`binfmt_misc` boundary ([`tests/e2e/qemu_matrix_test.go`](../../tests/e2e/qemu_matrix_test.go)); symlink cache entries failed closed (`exit 1`).

---

## 5. ADR-0001 Promotion Gate Assessment

Per [ADR-0001 §3](../../docs/design/adr-0001-formal-verification-and-v3-lab.md#3-promotion--rollback-criteria), **production Format v2 and Go stubs remain the default in this PR**:

| Promotion Criterion | Format v3 (`#500` / `#505`) Status | Rust Stub (`#497` / `#498` / `#499`) Status | Open Work to Promote |
| :--- | :---: | :---: | :--- |
| Complete-cost archive size win | **PASS** (landed in `#505`; `-4.05%` to `-9.85%` AMD64, `-33.06%` to `-35.09%` ARM64 vs best v2) | **PASS** (**`184,448 B` (`180.1 KiB`)** x86-64 & **`160,040 B` (`156.3 KiB`)** ARM64 static musl v0.4; `-33.3%`/`-33.4%` archive bytes vs `gom` on `zstd`/`zstd --dict`) | Production wiring decision |
| Cold `memfd` & warm-cache latency | **PASS** (landed in `#505`; fused parallel unfiltering beats v2 controls) | **Mostly PASS** (`none` **`6.22 ms`**, `lz4` **`8.23 ms`** (`-14.0%` vs `gom`), `warm-auto` **`4.18 ms`** (`-19.6%` vs `gom`) all beat Go `stub-minimal`; `zstd` improved `2.19×` over v0.2 to `12.76 ms` / `12.70 ms` vs Go `8.54 ms` / `9.84 ms`) | Optional pure-Rust/no-C BMI2/Huffman optimization if the remaining raw-`zstd` delta vs Go asm blocks promotion; native ARM64 timing skipped without hardware |
| Cross-arch & static ABI parity | **PASS** (`x86_64` & `aarch64` corpora packed/verified; native ARM64 timing skipped) | **PASS** (Zero-C `rust-lld` static `musl` for both `x86_64` & `aarch64`; Kani **17/17** harnesses across format, DAG, policy, and cgroup/Zstd math; QEMU AArch64 unit + chaos checks pass) | Optional native ARM64 timing if hardware/CI runner becomes available |
| Security & chaos parity | **PASS** (SHA-256 index/block/full-image checks + 4-seal `memfd`) | **PASS** (Full [`tests/e2e/rust_stub_parity_test.go`](../../tests/e2e/rust_stub_parity_test.go) passes **10/10** suites under `-race` + `MICROFAT_TEST_LEAKS=1`; wired into `task test-rust-stub-e2e` and `.github/workflows/formal-lab.yml`) | Explicit promotion / default-swap review decision |
| Repository CI gate (`NO_COLOR=1 task all`) | **PASS** | **PASS** ([`.github/workflows/formal-lab.yml`](../../.github/workflows/formal-lab.yml) live on `main`: Z3, Lean 4, Rust stub `x86_64`/`aarch64` + Go `TestRustStub` E2E, and Kani **17/17**; `NO_COLOR=1 task all` + `task test-dx` green locally) | None (promotion PR when approved) |
