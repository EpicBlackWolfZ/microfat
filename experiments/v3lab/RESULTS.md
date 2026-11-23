# Post-`v0.4.1` Formal Verification & Compression Research Results (`v3lab`)

- **Topic Branches**: Phase 1 (`#502`) & Phase 2 Track 1 (`#504`) merged onto `origin/main` (`f7660bb`); Phase 2 Track 2 (`perf/v3-parallel-unfilter-dict-arm64`) advances Format v3 + `#500` on top of `f7660bb`
- **Design Decision & Promotion Gates**: [ADR-0001](../../docs/design/adr-0001-formal-verification-and-v3-lab.md)
- **Related Issues**: [#378](https://github.com/EpicBlackWolfZ/microfat/issues/378), [#497](https://github.com/EpicBlackWolfZ/microfat/issues/497), [#498](https://github.com/EpicBlackWolfZ/microfat/issues/498), [#499](https://github.com/EpicBlackWolfZ/microfat/issues/499), [#500](https://github.com/EpicBlackWolfZ/microfat/issues/500), [#501](https://github.com/EpicBlackWolfZ/microfat/issues/501)

This document summarizes all observed verification results, benchmarks, and promotion-gate assessments from the post-`v0.4.1` research tracks. Every figure below comes directly from artifacts and command runs on this machine; unverified production claims or absent/emulated hardware backends (no CUDA Toolkit / `nvrtc.h` for Bend GPU codegen; QEMU user-mode emulation and x86-64 host decoding rather than native ARM64 hardware timing) are explicitly noted.

---

## 1. Track A — Multi-Toolchain Formal Verification Scorecard

| Toolchain | Version & Target Scope | Status | Verified Targets / Properties | Claim Boundaries & Assumptions |
| :--- | :--- | :--- | :--- | :--- |
| **Lean 4** | `v4.34.0` on mathematical specs & filter algebra ([`formal/lean/Microfat.lean`](../../formal/lean/Microfat.lean)) | **PASS** (`lake build`, 7 jobs, 0 warnings) | **31 theorems** across `FormatV2` (marshal/unmarshal round-trip, exact consumption, trailing-byte rejection, segment bounds/disjointness), `Microarch` (AMD64 rank iff, monotonicity, ARM64 DAG acyclicity, SIGILL freedom, unforced optimality), `CgroupMath` (overflow guard, retained limit, `GOMEMLIMIT` ceiling & monotonicity, worker admission sum), and `ElfFilter` (ARM64 BL/B/ADRP and x86-64 BCJ+RIP word/byte bijections plus `x86_chunk_local_reassembly`, `arm64_chunk_local_reassembly`, `x86_dedup_multi_offset_sound`, `arm64_dedup_multi_offset_sound`, `arm64_aligned_split_commutes`). Uses **only** `{propext, Classical.choice, Quot.sound}`; zero `sorry`, `sorryAx`, `native_decide`, or custom axioms. | Proves properties of the Lean model rather than directly compiling Go/Rust binaries; linked to shipped Go (`experiments/v3lab/compress/elffilter_test.go`) and Rust (`experiments/v3lab/rust-stub/src/elffilter.rs`) via shared `#eval` conformance vectors and `cargo test` / `go test -race`. |
| **Z3 (`QF_BV`)** | `v5.3.3` on 64-bit bitvector SMT-LIB2 ([`formal/smt/`](../../formal/smt/README.md)) | **PASS** | `format_v2_bounds.smt2`: `sat` (premise satisfiability) + 3× `unsat` (negated overflow, bounds, and overlap checks). `cgroup_memory.smt2`: 3× `sat` + 3× `unsat` (overflow guard, `RetainedMemoryLimit`, `GOMEMLIMIT` headroom/ceiling bounds across all `2^64` words). | Models exact two's-complement 64-bit signed/unsigned arithmetic of the guards; independent of allocator/sysfs I/O. |
| **Gobra** | `0.1.0-SNAPSHOT` (`viperproject/gobra-ide v3.1.2` local `--overflow`) + canonical CI gate (`v26.09`, [#479](https://github.com/EpicBlackWolfZ/microfat/pull/479)) directly on **production Go** [`internal/cgroup/memory.go`](../../internal/cgroup/memory.go) | **PASS** (`--overflow`, `Gobra found 0 errors.`) | Directly proves signed 64-bit overflow freedom and exact functional pre/postconditions on production `ExtractionMemory` and `RetainedMemoryLimit`, matching the canonical `scripts/verify-cgroup-memory.sh` / [`cgroup-memory-verification.md`](../../docs/design/cgroup-memory-verification.md) contract. | Scoped via `-i internal/cgroup/memory.go` with [`verification/gobra/stubs/math/limits.gobra`](../../verification/gobra/stubs/math/limits.gobra) (`MaxInt64`); remaining sysfs/float helpers and Format v2 bounds (`#501`) remain covered by Go tests, Lean 4, and Z3. |
| **Bend 2** | `v2.0.40` (`--verdict`) on [`experiments/v3lab/bend/filter_proof.bend`](bend/filter_proof.bend) | **PASS** (`ALL PROOFS CHECK`, zero `@unsafe`) | Machine-checks pure bidirectional step & wraparound round-trip properties for normalized x86/ARM64 offset transforms inside pure Bend code. | Model-level check in Bend (`filter_proof.bend`); does not directly inspect Go ELF parsing. |
| **Kani** | `v0.68.0` standalone (`CBMC 6.11.0`, `nightly-2026-08-21`) directly on **Rust stub core** [`experiments/v3lab/rust-stub/src/lib.rs`](rust-stub/src/lib.rs) | **PASS** (`11/11 harnesses`, 0 failures) | Verifies compiled `#![no_std]` `#![forbid(unsafe_code)]` Rust code directly: `verify_arm64_step_word_bijection`, `verify_x86_step_bijection`, `verify_x86_transform_roundtrip_8`, `verify_x86_chunk_local_split_roundtrip` (`144.94s`), `verify_arm64_chunk_local_split_roundtrip` (`1.24s`), `verify_evaluate_amd64_exhaustive` (`2^21` masks), `verify_evaluate_arm64_dag_exhaustive` (`2^24` masks), `verify_trailer_never_panics_and_enforces_exact_layout`, `verify_cursor_primitives_safe`, `verify_decode_hex_digest_no_panic`, and `verify_segment_bounds_disjointness_and_selection` (`1..=4` variants across AMD64 + ARM64 DAG). | Checks Rust core under explicit symbolic bounds (`len <= 8` for multi-step x86 filter, `1..=4` variants for segment layout, `<= 64B` cursor slice); full arbitrarily-long x86 stream & chunk-local bijections are proven unbounded in Lean 4. Hardware SHA-NI intrinsics ([`sha256_hw.rs`](rust-stub/src/sha256_hw.rs)), host probes ([`cpuid.rs`](rust-stub/src/cpuid.rs)), and Linux syscalls ([`main.rs`](rust-stub/src/main.rs)) are verified via unit/differential tests and E2E/QEMU checks rather than Kani models. |

### Alignment with `origin/main` Hardening (`#479`, `#502`, `#504`)

`origin/main` already hardened [`internal/format/format.go`](../../internal/format/format.go) and merged direct production Gobra verification of [`internal/cgroup/memory.go`](../../internal/cgroup/memory.go) via [#479](https://github.com/EpicBlackWolfZ/microfat/pull/479). Accordingly, `perf/v3-parallel-unfilter-dict-arm64` introduces zero edits to production Format v2 or Go stub behavior; all code stays isolated under `formal/` and `experiments/v3lab/`.

Follow-up issues track the research roadmap: [#499](https://github.com/EpicBlackWolfZ/microfat/issues/499) (Rust stub SHA-NI, `opt-level=3`, static `musl`, warm cache, chaos parity — advanced in `#504`), [#500](https://github.com/EpicBlackWolfZ/microfat/issues/500) (Format v3 fused per-extent parallel unfilter, seeded Zstd block dictionary, ARM64 corpus), and [#501](https://github.com/EpicBlackWolfZ/microfat/issues/501) (window-accurate Zstd extraction budget in production + expanded Gobra/CI checks).

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

In **v0.2 (`perf/rust-stub-shani-musl`)**, `microfat-stub-rs` upgrades from the dynamic glibc v0.1 prototype (`#502`) to:
- Static **`x86_64-unknown-linux-musl`** and **`aarch64-unknown-linux-musl`** builds (`+crt-static`, `force-unwind-tables=no`, `aarch64` linked via `rust-lld`).
- Runtime-dispatched x86-64 hardware **SHA-NI** ([`sha256_hw.rs`](rust-stub/src/sha256_hw.rs)) with pure-Rust `microfat_stub_core::sha256` fallback.
- Generalized Kani-verified **ARM64 non-linear DAG** (`v8.0..v8.9`, `v9.0..v9.5`) + Linux `getauxval` / `/proc/self/auxv` `AT_HWCAP`/`AT_HWCAP2` capability probing ([`microarch.rs`](rust-stub/src/microarch.rs), [`cpuid.rs`](rust-stub/src/cpuid.rs)).
- Zero-copy read-only `mmap` self-mapping (with `FileSectionReader` fallback), `ftruncate` pre-sized `memfd`, `256 KiB` copy buffer, `[profile.release.package.*] opt-level = 3` on `ruzstd` / `lz4_flex` / `twox-hash`, and read-only warm-cache fast path (`MICROFAT_EXEC_MODE` / `MICROFAT_DISPATCH_MODE` / `MICROFAT_CACHE_DIR`) enforcing Go's `O_NOFOLLOW`/uid/mode/SHA-256 fail-closed contract ([`main.rs`](rust-stub/src/main.rs)).

### Binary Footprint & Linkage

| Launcher Stub | File Size (Bytes) | `.text` / `.data` / `.bss` (`size`) | Linkage | Notes |
| :--- | :---: | :---: | :--- | :--- |
| **`bin/microfat-stub`** (Go full x86-64) | `3,940,512 B` (`3.76 MiB`) | `3,839,067 / 92,339 / 33,799,016` | Static (`CGO_ENABLED=0`) | Production baseline (`#502` `size`) |
| **`bin/microfat-stub-minimal`** (Go minimal x86-64) | `2,916,512 B` (`2.78 MiB`) | `2,835,037 / 71,251 / 33,798,240` | Static (`CGO_ENABLED=0`) | Production minimal baseline (`#502` `size`) |
| Go `stub-minimal` (cross `arm64`) | `2,752,672 B` (`2.63 MiB`) | `-` | Static (`CGO_ENABLED=0 GOOS=linux GOARCH=arm64`) | ARM64 QEMU control |
| `microfat-stub-rs` **v0.1** (`#502`, glibc x86-64) | `436,400 B` (`426.2 KiB`) | `414,480 / 17,864 / 3,882` | Dynamic (`libc.so.6`, `libgcc_s.so.1`, `ld-linux`) | Prior baseline (`opt-level="z"` scalar SHA) |
| **`microfat-stub-rs` v0.2 (`x86_64-musl`)** | **`540,864 B`** (`528.2 KiB`) | **`524,105 / 3,472 / 4,184`** | **Static** (`x86_64-unknown-linux-musl`, stripped) | **5.39×** smaller than Go `stub-minimal`; **7.29×** smaller than `stub-full` |
| **`microfat-stub-rs` v0.2 (`aarch64-musl`)** | **`478,512 B`** (`467.3 KiB`) | **`461,094 / 15,368 / 5,017`** | **Static** (`aarch64-unknown-linux-musl`, `rust-lld`) | **5.75×** smaller than Go `stub-minimal` ARM64 (`< 500 KiB`) |

### x86-64 End-to-End Cold `memfd` & Warm-Cache (`auto`) Latency (`15` runs, `demo-stripped --help`)

All fat binaries and warm-cache invocations produced exit code `0` and identical stdout SHA-256 (`48ecd80e6b9b63ed`):

| Stub | Codec / Dispatch Mode | Total Fat Binary Size | v0.1 Median (`#502`) | v0.2 Median (Min–Max) | Delta vs v0.1 / Go `stub-minimal` |
| :--- | :--- | :---: | :---: | :---: | :--- |
| Go `stub-full` (`3.94 MB`) | `none` (cold `memfd`) | `16,937,998 B` | `6.30 ms` | `6.03 ms` (`5.66–7.34`) | Reference |
| Go `stub-full` | `lz4` (cold `memfd`) | `11,511,190 B` | `9.60 ms` | `9.07 ms` (`8.74–9.37`) | Reference |
| Go `stub-full` | `zstd` (cold `memfd`) | `9,539,968 B` | `8.87 ms` | `9.42 ms` (`7.63–12.74`) | Reference |
| Go `stub-full` | `zstd --dict` (cold `memfd`) | `9,515,796 B` | `10.66 ms` | `11.92 ms` (`10.43–16.56`) | Reference |
| Go `stub-full` | `warm-auto` (`zstd` cache hit) | `9,539,968 B` | `-` | `4.98 ms` (`4.74–5.38`) | Reference |
| Go `stub-minimal` (`2.92 MB`) | `none` (cold `memfd`) | `15,913,998 B` | `6.22 ms` | `6.00 ms` (`5.75–6.48`) | Baseline |
| Go `stub-minimal` | `lz4` (cold `memfd`) | `10,487,190 B` | `9.19 ms` | `9.19 ms` (`8.66–16.16`) | Baseline |
| Go `stub-minimal` | `zstd` (cold `memfd`) | `8,515,968 B` | `9.42 ms` | `9.36 ms` (`7.92–18.80`) | Baseline |
| Go `stub-minimal` | `zstd --dict` (cold `memfd`) | `8,491,796 B` | `13.33 ms` | `11.72 ms` (`9.89–13.12`) | Baseline |
| Go `stub-minimal` | `warm-auto` (`zstd` cache hit) | `8,515,968 B` | `-` | `5.08 ms` (`4.67–5.48`) | Baseline |
| **Rust `microfat-stub-rs` (`540.9 KB` static)** | `none` (cold `memfd`) | **`13,538,350 B`** | `20.34 ms` | **`5.30 ms`** (`5.08–5.48`) | **`3.84×` faster than v0.1**; **`-11.7%` vs `gom`** |
| **Rust `microfat-stub-rs`** | `lz4` (cold `memfd`) | **`8,111,542 B`** | `28.61 ms` | **`15.52 ms`** (`15.22–15.78`) | **`1.84×` faster than v0.1** (`+6.33 ms` vs `gom`) |
| **Rust `microfat-stub-rs`** | `zstd` (cold `memfd`) | **`6,140,320 B`** | `49.53 ms` | **`26.36 ms`** (`25.93–26.51`) | **`1.88×` faster than v0.1** (`ruzstd @ opt-level=3`) |
| **Rust `microfat-stub-rs`** | `zstd --dict` (cold `memfd`) | **`6,116,148 B`** | `49.82 ms` | **`26.24 ms`** (`25.69–26.69`) | **`-28.0%` bytes vs `gom`** (`-35.7%` vs `gof`) |
| **Rust `microfat-stub-rs`** | `warm-auto` (`zstd` cache hit) | `6,140,320 B` | `-` | **`4.02 ms`** (`3.84–4.30`) | **`-20.9%` latency vs `gom`** (`-19.3%` vs `gof`) |

### Zstd Backend Shootout (`ruzstd @ opt-level=3` vs `libzstd-rs-sys 0.0.1-prerelease.2`)

To test closing the remaining cold `zstd` decompression gap (`26.36 ms` vs Go's `klauspost/compress/zstd` assembly at `9.36 ms`), we benchmarked `libzstd-rs-sys 0.0.1-prerelease.2` (`--features libzstd-rs` temporarily):
- **Speed (`x86_64-unknown-linux-musl`, `CC_x86_64_unknown_linux_musl=gcc`)**: Cold `memfd` median dropped to **`10.83 ms`** (`10.31–13.19`) on `zstd` and **`11.08 ms`** (`10.63–11.63`) on `zstd --dict` (`none`: `5.37 ms`, `lz4`: `17.33 ms`, `warm-auto`: `4.13 ms`), matching Go `zstd --dict` (`11.72–12.29 ms`).
- **Disqualifying Blockers**:
  1. **Binary Size Bloat**: Static `x86_64` stub size grew to **`688,752 B` (`672.6 KiB`, `> 500 KiB`)** even under `lto = "fat"` and `strip = true`.
  2. **Broken Zero-C Cross-Compilation**: Upstream `libzstd-rs-sys` `build.rs` gates `huf_decompress_amd64.S` on host `#[cfg(target_arch = "x86_64")]` instead of `CARGO_CFG_TARGET_ARCH`, causing `aarch64-unknown-linux-musl` builds on x86-64 hosts to fail (`ToolNotFound: failed to find tool "aarch64-linux-musl-gcc"`).
- **Decision**: Kept pure-Rust **`ruzstd @ opt-level=3`** cleanly in `Cargo.toml`/`Cargo.lock` with zero `cc` build scripts so `x86_64` and `aarch64` static `musl` builds work out of the box via `rust-lld` alone. Note also `x86_64-unknown-linux-musl` (`540,864 B`) is slightly above `500 KiB` (`512,000 B`) due to `ruzstd @ opt-level=3` unrolled tables (whereas `aarch64-unknown-linux-musl` is `478,512 B`); finding a zero-C, SIMD/BMI2-capable Zstd decoder under `500 KiB` remains open under `#498`/`#499`.

### ARM64 Static `musl` Verification under QEMU (Emulated, Non-Native)

> **Environment Note**: Executed on x86-64 Linux via `/usr/bin/qemu-aarch64-static` + `binfmt_misc` (`flags: F`). Native ARM64 hardware performance was not measured here.

- **On-Target AArch64 Unit Tests (`cargo test --target aarch64-unknown-linux-musl`)**: **8/8** `microfat_stub_core` tests + **4/4** `microfat-stub-rs` binary tests passed (`cargo clippy --target aarch64-unknown-linux-musl -- -D warnings`: `0` warnings).
- **Multi-Variant Packaging & Verification**: Built `./examples/demo` at `GOARM64=v8.0`, `v8.1`, `v8.2` (`4,063,392 B` each; direct QEMU `--help` stdout SHA-256 `48ecd80e6b9b63ed...`) and packed 4 ARM64 fat binaries with `microfat-stub-rs` (`478,512 B`): `arm64-rs-none` (`12,669,092 B`), `arm64-rs-lz4` (`7,310,603 B`), `arm64-rs-zstd` (`5,491,762 B`), `arm64-rs-zstd-dict` (`5,481,246 B`). All 4 passed `./bin/microfat verify`.
- **`memfd` & Warm-Cache Security Checks under `/usr/bin/qemu-aarch64-static`**:
  - All 4 codecs in `MICROFAT_EXEC_MODE=memfd` completed `AT_SECURE`, trailer/index SHA-256 verification, ARM64 `getauxval` DAG selection, decompression, SHA-256 verification, 4-seal `memfd` setup, and `execve("/proc/self/fd/11")`, stopping strictly at QEMU's documented user-mode `O_CLOEXEC`/`binfmt_misc` boundary (`Error while loading /proc/self/fd/11: No such file or directory`, matching Go `stub-minimal` ARM64 control parity `2,752,672 B` -> `/proc/self/fd/21` and [`tests/e2e/qemu_matrix_test.go`](../../tests/e2e/qemu_matrix_test.go)).
  - Read-only `cache` mode miss (empty `0700` dir): exited `1` (`microfat-stub-rs: no valid warm cache entry found (read-only cache mode)`).
  - Pre-populated `0700` warm-cache hit: validated metadata/SHA-256 and reached `execve("/proc/self/fd/12")` handoff (`stub_stderr` empty; same QEMU `/proc/self/fd/12` `O_CLOEXEC` boundary).
  - Symlink cache entry in `auto` mode: failed closed (`exit 1`, `microfat-stub-rs: unsafe warm cache state: unsafe cache entry symlink`).

---

## 5. ADR-0001 Promotion Gate Assessment

Per [ADR-0001 §3](../../docs/design/adr-0001-formal-verification-and-v3-lab.md#3-promotion--rollback-criteria), **production Format v2 and Go stubs remain the default in this PR**:

| Promotion Criterion | Format v3 Status | Rust Stub (`#497` / `#499`) Status | Open Work to Promote |
| :--- | :---: | :---: | :--- |
| Complete-cost archive size win | **PASS** (`-4.05%` to `-9.85%` AMD64; `-33.06%` to `-35.09%` ARM64 vs best v2) | **PASS** (`540.9 KiB` x86_64 static, `478.5 KiB` ARM64 static; `-28.0%` vs `gom`, `-35.7%` vs `gof`) | Trim x86_64 musl below `500 KiB` (`512,000 B`) without sacrificing decompression speed |
| Cold `memfd` & warm-cache latency | **PASS** (Fused parallel unfiltering beats all v2 controls: `6.48 ms` `demo-stripped`, `17.35 ms` `microfat-stripped`) | **Partial** (`warm-auto` **`4.02 ms`** and `none` **`5.30 ms`** beat Go `stub-minimal`; `zstd` `26.36 ms` vs Go `9.36 ms`, `lz4` `15.52 ms` vs Go `9.19 ms`) | Measure native ARM64 v3 decode latency; replace `ruzstd` with a zero-C-cross-tool `< 500 KiB` fast Zstd decoder or upstream fix to `libzstd-rs-sys` `build.rs` (`#498`) |
| Cross-arch & static ABI parity | **Partial** (AMD64 + ARM64 ELF corpora packed/verified; ARM64 decode timed on x86-64 host only) | **Partial** (Static `x86_64-unknown-linux-musl` + `aarch64-unknown-linux-musl` builds; ARM64 DAG + `getauxval` verified via Kani 11/11 & QEMU) | Native ARM64 hardware benchmark for both Format v3 decode and Rust stub; cgroup runtime tuning (`GOMEMLIMIT`/`GOMAXPROCS`) |
| Security & chaos parity | **PASS in prototype** (SHA-256 index/block/full-image checks + 4-seal `memfd`) | **Partial** (`AT_SECURE`/uid/gid + SHA-NI + 4-seal `memfd` + read-only `O_NOFOLLOW`/uid/mode/SHA-256 warm cache verified) | Read-write cache materialization (`MICROFAT_EXEC_MODE=cache` cold path) & full `tests/e2e` chaos port |
| Repository CI gate (`NO_COLOR=1 task all`) | **PASS** | **PASS** (Rust crate isolated under `experiments/v3lab/`) | Wire Rust/Kani/Lean/Gobra checks into optional/CI lab workflows if promoted |
