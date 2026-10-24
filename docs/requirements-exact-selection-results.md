# Requirements-exact selection: retain the simpler selector

The #153 evaluation retains the existing requirements-based selector. The
test-only exact-mask candidate misses both the frozen useful-work gate and the
representative nonregression gate. Two independently reproduced architecture
alias defects are fixed: `x86-64` now matches an `amd64` host, and safe AVX-512
policy applies consistently to `amd64`, `x86_64` and `x86-64` architecture names.
Forced selection retains its existing precedence over caps/disabled/safe policy.

**Local contention disclosure:** after collection, the user reported gaming on
this workstation during the same session. Exact overlap was not logged, so
contention during timing cannot be excluded. Treat this local cohort as
exploratory: its observed gate failures do not establish mask-caused regressions
or idle-host performance. The original archive and all observations are preserved.
The [separate hosted native characterizations](requirements-exact-selection-hosted-results.md)
also miss both fixed gates. Their [prospective protocol](requirements-exact-selection-hosted.md)
keeps each architecture independent; later results cannot rescue the failed local
promotion decision.

The [prospective protocol](requirements-exact-selection.md), candidate, independent
reference and process-context benchmark were committed at measured source
`83ed54f4605a36bda222046bf0a611263b3824ed` before collection. Both source-state
snapshots were clean and all recorded source hashes rechecked successfully.
The baseline is `7adeba76cbc20507c196d230db961513235edb76`; its alias failures
are retained separately from the measurements, which include the correctness fixes.

## Cohort and decision

Native Linux amd64, AMD Ryzen 9 3950X, Go 1.27.3; parent `GOMAXPROCS=1`,
`-cpu=1`, 100 ms benchmark budget, eight repetitions per fixture/arm.
All **304 selection rows** (19 fixtures × two implementations × eight) completed.
The independent raw-data audit agreed with the retained replay script. There
were no missing repetitions or replaced cohorts. Error fixtures below are
intentional error-return measurements, not unexpected launch/test failures.

The table reports median ns/op, with minimum–maximum in brackets, rounded to
whole nanoseconds for readability. The archive retains exact values, iteration
counts, B/op and allocations for every repetition. ARM feature fixtures are
synthetic inputs executed on this amd64 host, not native ARM timing evidence.

| Fixture | Current ns/op [range] | Exact mask ns/op [range] | Current → mask allocs/op |
| --- | ---: | ---: | ---: |
| AMD sparse | 700 [666–716] | 802 [782–817] | 5 → 5 |
| AMD dense | 1,332 [1,307–1,401] | 1,510 [1,482–1,528] | 5 → 5 |
| AMD cap | 1,241 [1,227–1,309] | 1,420 [1,370–1,448] | 7 → 7 |
| AMD disabled | 1,455 [1,430–1,474] | 1,630 [1,596–1,688] | 8 → 8 |
| AMD forced | 839 [832–847] | 966 [957–974] | 3 → 3 |
| AMD normalization | 2,092 [1,972–2,166] | 2,338 [2,323–2,406] | 21 → 22 |
| AMD unknown (expected error) | 152 [151–155] | 236 [233–239] | 1 → 1 |
| AMD unavailable (expected error) | 178 [169–192] | 246 [235–260] | 1 → 1 |
| ARM sparse v8.0 | 807 [773–836] | 645 [618–682] | 3 → 3 |
| ARM sparse v8.2 | 2,422 [2,389–2,643] | 1,050 [990–1,148] | 8 → 5 |
| ARM sparse v9.0 without v8.9 | 4,706 [4,633–4,914] | 1,216 [1,085–1,236] | 13 → 5 |
| ARM dense, all 16 compatible | 34,964 [34,243–36,185] | 5,195 [4,960–5,432] | 60 → 5 |
| ARM cap | 4,800 [4,661–4,975] | 1,191 [1,180–1,199] | 13 → 5 |
| ARM disabled | 4,765 [4,633–5,021] | 1,212 [1,159–1,244] | 13 → 5 |
| ARM forced | 3,041 [2,985–3,078] | 972 [952–993] | 6 → 3 |
| ARM forced unavailable (expected error) | 5,661 [5,543–5,843] | 1,494 [1,445–1,609] | 15 → 7 |
| ARM forced incompatible (expected error) | 6,286 [6,213–6,540] | 659 [648–688] | 14 → 1 |
| ARM unknown (expected error) | 158 [156–163] | 395 [376–401] | 1 → 1 |
| ARM unavailable (expected error) | 2,301 [2,259–2,340] | 435 [374–451] | 6 → 1 |

Observed sparse ARM median savings are **0.162, 1.372 and 3.490 µs**: all miss the
preselected 100 µs saving threshold. The v8.0 case also misses the 25% reduction
and fewer-allocation requirements. All six successful AMD fixtures have slower timings by
**11.7–15.2%** in these medians; normalization grows from 456 to 472 B/op and
21 to 22 allocations. The dense ARM improvement cannot rescue the failed gates.
Unknown/error paths also include regressions, retained in the table. These are
descriptive comparisons, with no population confidence interval or causal
whole-process performance claim.

## Separate complete-process context

All **32 process benchmark rows** (four modes × eight repetitions) completed
with exact payload marker output, no stderr and successful exit. The existing
golden fixture uses the full stub, default balanced Zstd, no dictionary and the
default format. All variants in the fixture are built for the actual native host.
Every timed invocation is a fresh process; warm-cache preparation and cold-cache
removal are outside timing. The native control executes the baseline-tier payload.

| Current-only process mode | Median ms | Minimum–maximum ms |
| --- | ---: | ---: |
| Direct native | 1.180 | 1.146–1.207 |
| Explicit memfd | 6.917 | 6.655–7.398 |
| Explicit cold cache | 8.219 | 8.087–8.550 |
| Explicit warm cache | 3.727 | 3.580–3.930 |

These complete `exec.Cmd.Output` lifetimes include process creation, launcher
work where applicable and the fixture's exit. They do not measure service p99,
native ARM behavior or a candidate launcher. Parent allocations are not child
allocations/RSS. Launcher children retain production autotuning; direct children
use runtime defaults. OS page cache, CPU affinity (0–31) and shared-host pressure
are uncontrolled; there is no dedicated-hardware or imposed-memory-cap claim.

The original cgroup snapshot read the root only and missed process-relative
quota/memory files. A timestamped process/ancestor snapshot is retained explicitly
as **post-cohort**, not evidence of applied limits during timing. This limitation
does not become a claim of controlled resources or erase the failed gates.

## Correctness, evidence and reproduction

The independent flattened reference covers raw AMD64/ARM64 detection composition,
every required-feature removal, sparse/dense randomized sets, AMD available
subsets/order, aliases, unknowns, LAHF/SAHF negatives, both ARM branch directions,
conservative level-only helpers and policy/error precedence. The mask candidate
also passes exact metadata/error differential tests, including 500 fixed-seed
random cases. The production change consists only of the two alias fixes; masks
remain test-only. Native amd64/arm64 CI explicitly runs the reference and candidate
tests alongside existing runtime/policy compatibility qualification.

The [retained archive](benchmarks/selection-153/local-83ed54f.tar.gz) contains the
frozen protocol, all raw measurements, replay script/JSON summary, toolchain/source
hashes, before/after state, initial/post-cohort host observations, original failing
alias test output and independent audit. Its adjacent
[SHA-256](benchmarks/selection-153/SHA256SUMS) checks archive integrity, not
producer identity or statistical assumptions. Extract it and run `node analyze.cjs` to
recompute the summary without new measurements. Full repository qualification
and final-head hosted checks are recorded in the associated PR; this bounded
evaluation does not authorize merge or release.
