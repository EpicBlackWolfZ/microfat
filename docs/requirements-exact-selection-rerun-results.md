# Requested local reassessment: retain the simpler selector

The separate rerun requested after gaming stopped still does not justify
promoting this exact-mask candidate. Both unchanged gates fail. Keep the existing
production selector and the two architecture-alias correctness fixes in PR #400.
This conclusion concerns the tested candidate and the predeclared maintenance
budget; it does not rule out a future ARM-only or less redundant design with its
own prospective evaluation.

The [rerun amendment](requirements-exact-selection-rerun.md) was committed at
measured source `44eac8365494739266f73aae50d117ad66a383a7` before either
command. Only documentation changed from prior PR head `22d3d8b9766d1d36220d084f91c1a72b1d204974`.
At that measured commit, production, reference and benchmark sources are
unchanged from the earlier measured source `83ed54f4605a36bda222046bf0a611263b3824ed`. Before/after source
states are clean, and all nine recorded source hashes recheck successfully.
All previous [local](requirements-exact-selection-results.md) and
[hosted](requirements-exact-selection-hosted-results.md) observations remain
preserved as separate cohorts, without pooling or replacing unfavorable results.

## Collection and limits

Native Linux amd64, AMD Ryzen 9 3950X, Go 1.27.3, 32 logical CPUs and affinity
0–31; parent `GOMAXPROCS=1`, `-cpu=1`, `-benchtime=100ms`, `-count=8`, without
race/coverage instrumentation. Collection ran on 2026-10-24 between
**18:16:03 and 18:17:52 UTC**, with selection and process commands run serially.
No other local tests, builds or benchmarks were deliberately run concurrently.
The maintainer reported gaming had stopped; that report does not establish an
idle host. System CPU pressure snapshots before, between and after collection
reported `some avg10/60/300=0.00`; these are snapshots, not continuous isolation
or absence-of-interference guarantees. Readable process-relative cgroup and
ancestor CPU quotas/memory ceilings were unlimited (`max`); root quota/memory
files were unavailable and retained as such. No affinity or resource cap was
imposed, and OS page cache was uncontrolled.

All **304 selection rows** (19 fixtures × two implementations × eight repetitions)
and **32 process-context rows** (four modes × eight) completed with `PASS`.
Every repetition, iteration count, B/op and allocs/op is retained. The tables
show descriptive medians and minimum–maximum ranges, not confidence intervals
or independent population samples. ARM feature inputs on amd64 are synthetic
selector inputs, not native ARM instruction-execution timings. Error-return
fixtures below are intentional and remain outside the successful-fixture gate.

## Complete selector results

Nanoseconds are rounded to whole units for display; the archived JSON and raw
output retain exact observations. Allocations were constant within each group.

| Fixture | Current ns/op [range] | Exact mask ns/op [range] | Current → mask allocs/op |
| --- | ---: | ---: | ---: |
| AMD sparse | 663 [644–668] | 759 [756–764] | 5 → 5 |
| AMD dense | 1,246 [1,198–1,296] | 1,390 [1,370–1,443] | 5 → 5 |
| AMD cap | 1,198 [1,193–1,211] | 1,342 [1,335–1,420] | 7 → 7 |
| AMD disabled | 1,376 [1,365–1,380] | 1,513 [1,508–1,520] | 8 → 8 |
| AMD forced | 783 [769–786] | 895 [890–903] | 3 → 3 |
| AMD normalization | 1,913 [1,886–1,938] | 2,140 [2,110–2,153] | 21 → 22 |
| AMD unknown (expected error) | 140 [139–143] | 223 [220–227] | 1 → 1 |
| AMD unavailable (expected error) | 164 [163–169] | 230 [226–241] | 1 → 1 |
| ARM sparse v8.0 | 744 [735–750] | 599 [594–618] | 3 → 3 |
| ARM sparse v8.2 | 2,230 [2,222–2,282] | 942 [939–961] | 8 → 5 |
| ARM sparse v9.0 without v8.9 | 4,394 [4,362–4,456] | 1,034 [1,028–1,039] | 13 → 5 |
| ARM dense, all 16 compatible | 32,489 [32,237–32,691] | 4,870 [4,736–5,129] | 60 → 5 |
| ARM cap | 4,508 [4,478–4,524] | 1,122 [1,111–1,144] | 13 → 5 |
| ARM disabled | 4,523 [4,475–4,548] | 1,135 [1,129–1,152] | 13 → 5 |
| ARM forced | 2,920 [2,889–2,989] | 922 [914–928] | 6 → 3 |
| ARM forced unavailable (expected error) | 5,252 [5,168–5,287] | 1,402 [1,396–1,414] | 15 → 7 |
| ARM forced incompatible (expected error) | 5,994 [5,968–6,008] | 632 [630–639] | 14 → 1 |
| ARM unknown (expected error) | 150 [149–151] | 372 [370–376] | 1 → 1 |
| ARM unavailable (expected error) | 2,222 [2,212–2,237] | 369 [367–370] | 6 → 1 |

Sparse ARM median savings are **0.14475, 1.2883 and 3.3605 µs**, respectively;
all miss the unchanged **100 µs** threshold. Relative savings are **19.463%,
57.758% and 76.471%**; v8.0 also misses the 25% threshold and retains three
allocations. The sparse-work gate fails.

All six successful AMD fixtures exceed the 5% nonregression ceiling: sparse
**14.490%**, dense **11.562%**, cap **12.020%**, disabled **9.956%**, forced
**14.332%**, normalization **11.840%** slower in the observed medians.
Normalization also grows from **456 to 472 B/op** and **21 to 22 allocations**.
The successful-representative gate fails. Dense ARM saves **27.6195 µs** and
reduces 60 allocations to five, but cannot override either failed gate.
These comparisons characterize this cohort and candidate, without population
confidence bounds or a causal launch-latency claim.

## Current-only complete-process context

Each timed invocation executed a fresh process and checked exact marker output,
empty stderr and successful exit. The full golden stub uses default balanced
Zstd, no dictionary and the default format; native control uses the baseline
payload. Cache preparation/removal and validation are outside timing. Child
runtime defaults/production autotuning remain as in the original protocol.

| Current-only mode | Median ms | Minimum–maximum ms |
| --- | ---: | ---: |
| native | 0.951 | 0.928–1.006 |
| memfd | 6.208 | 5.943–6.474 |
| cold-cache | 7.070 | 7.015–7.193 |
| warm-cache | 3.121 | 3.039–3.145 |

These measure `exec.Cmd.Output` lifetimes, including process creation, applicable
launcher work and payload exit. Parent allocations do not measure child
allocations or RSS. No candidate launcher ran, so these provide scale only and
establish neither mask-attributable launch savings nor service p99. The lower
process medians than the earlier local cohort do not establish gaming as the
cause of that difference.

## Retention and review

The [new archive](benchmarks/selection-153/local-rerun-44eac83.tar.gz) retains the
frozen amendment, raw outputs, replay script/summary, capture script and host
snapshots, source/toolchain provenance, clean-state records, hash recheck and
independent audit. The adjacent [SHA-256 list](benchmarks/selection-153/SHA256SUMS)
checks archive integrity, not producer identity or statistical assumptions.
Extract and run `node analyze.cjs` to replay analysis without collecting new
measurements. `sha256sum -c MANIFEST.sha256` checks the extracted files; the
separate source manifest names repository files at the measured commit.

Self-review corrected an inaccurate process-benchmark comment: the explicitly
supplied stderr buffer retains child stderr; `exec.Cmd.Output` only populates
`ExitError.Stderr` when no stderr writer is supplied. This comment correction
changes no executable code or measurement. Review of production, reference,
benchmark and native CI behavior found no implementation defects. Pinned full
local qualification at unchanged executable Go/CI code and fresh final-head
hosted checks remain distinct from performance evidence. Merge authorization is conditional on those final checks
and the completed review; no release is part of this evaluation.
