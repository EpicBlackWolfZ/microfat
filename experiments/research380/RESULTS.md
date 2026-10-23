# Local explicit prewarm result

**Explicit selected-variant prewarm is promising for recurring, latency-sensitive
launches, with a visible deployment and storage cost.** It improved foreground
completion on both held-out applications. It made deployment-to-first-ready
slower in every scenario and increased one-off CPU cost by about40%. This supports
opt-in operational guidance and a separately qualified deployment decision. It
does not support changing auto-dispatch defaults or adding an adaptive agent.

The frozen confirmation completed **1,200/1,200 episodes** in94.88 seconds, after
a separate144-episode/9.28-second pilot:104.15 seconds of study campaigns total,
inside the20-minute allocation. There were zero command failures, timeouts or
missing telemetry records. All2,000 never-prewarm launches used memfd; all2,000
prewarmed launches used cache. Each launch matched the selected v3 level, payload
digest/size and exact native stdout. All600 selected-cache verification commands
passed. No per-episode cache directories remain. The raw journal retains one
record per episode; the compressed report retains every one of4,000 launches.

This was a single native amd64 Ryzen3950X desktop during a coordinated quiet slot.
The process setting was GOMAXPROCS=4, with the fixed documented environment; it
was not a dedicated CPU quota or a physically cold-storage experiment. Source
hash verification and preparation read artifacts before timing. Both arms include
identical structured dispatch logging. The readiness endpoint is completion of a
short version command, not network readiness, service latency or a population p99.

## Foreground SLO and preparation cost

Each cell has100 episode attempts per arm. A burst counts once, and a miss means
any launch in that episode completed more than20 ms after its scheduled arrival.
All episodes succeeded, so the cost medians below also have100 samples per arm.
Numbers are milliseconds; B/P means never-prewarm/deployment-prewarm. Medians of
per-episode totals are used, rather than sums of independently computed medians.

| Application / episode |20 ms misses B→P | Max foreground B→P | Prewarm wall | Derived first-ready B→P | Child CPU B→P |
|---|---:|---:|---:|---:|---:|
| actionlint / one-off |2→0 |14.87→6.21 |25.56 |14.87→31.68 |24.14→33.74 |
| actionlint / recurring2×3 |12→0 |17.75→7.19 |24.17 |13.07→30.76 |146.58→67.18 |
| actionlint / disappears1×3 |4→0 |15.33→6.65 |24.81 |12.73→31.27 |71.14→47.65 |
| Fortio / one-off |100→0 |39.65→16.35 |60.01 |39.65→76.38 |67.55→94.61 |
| Fortio / recurring2×3 |100→0 |39.13→17.26 |60.05 |31.23→76.97 |353.48→180.21 |
| Fortio / disappears1×3 |100→0 |35.67→17.06 |57.46 |30.67→74.46 |178.18→128.51 |

Child CPU includes prewarm plus all foreground child CPU, with logging. It
excludes the separately retained postmeasurement verification command. First
ready is the earliest successful completion, including preparation delay, rather
than the first scheduled invocation or a sum of launch times. The maximum
foreground duration and total deployment span are separate recorded metrics.
Both deployment-to-first-ready and deployment-plus-foreground-span are **derived**
as prewarm process wall time plus the foreground clock endpoint. They exclude
private cache-directory setup, control JSON parsing and bookkeeping between the
two clocks; they are not one continuous complete-deployment timer. Child CPU
also excludes parent orchestration CPU. The raw campaign wall includes that
orchestration, correctness checks and reporting, so it is not a per-policy timer.

The one-off CPU increase is about40% for both applications. For recurring2×3,
total CPU fell about54% for actionlint and49% for Fortio; the disappearing1×3
cohort fell about33% and28%. Three launches already amortized CPU preparation here,
but not the initial deployment delay. A deployment can choose to perform this
work before first demand; the work and storage charges still exist.

## Retention and I/O accounting

Every prewarmed episode stored exactly one selected payload:6,512,764 bytes for
actionlint and19,718,268 for Fortio. Never-prewarm episodes stored zero cache
bytes. The fixed modeled retention charge is:

| Application | One-off / disappears,60 s | Recurring,1 s |
|---|---:|---:|
| actionlint |390,765,840 byte·s |6,512,764 byte·s |
| Fortio |1,183,096,080 byte·s |19,718,268 byte·s |

These are logical disk-byte-time scenarios, not measured retention waits or
resident memory. The disappeared recurrence keeps the60-second unused retention
charge. No expiry or cleanup policy was added to production; only exclusively
owned experiment directories were removed after each episode.

Linux input block counters were zero in both arms for all six cells. This is
consistent with the warm/uncontrolled page caches and does not mean decode/hash
work read zero bytes. Per100 prewarmed episodes, output counters were1,272,800
512-byte blocks for actionlint and3,852,000 for Fortio in each scenario; baseline
output counters were zero. These are Linux filesystem block accounting, not a
measurement of completed persistent-device transfers. Raw process counters and
postmeasurement verification costs are retained.

## Primary interpretation and conditional sensitivity

The primary protocol retained **IID=false**, so the observed episode risks above
are descriptive. The quiet slot, fresh caches and alternating arm order do not
establish independent stationary timing on this shared desktop. No population
p99 or rare-failure guarantee follows from100 episodes per arm. Zero observed
misses must not be presented as a proven zero miss probability.

The separate IID sensitivity was prespecified before confirmation with the same
20 ms SLO, N=100, six comparisons, family alpha=.05 and absolute10-percentage-point
margin. It asks what unchanged #377 binomial/Bonferroni calculations would say
under a hypothetical IID/stationarity assumption. Candidate-minus-baseline risk:

| Application / episode | Conditional simultaneous interval | Conditional decision |
|---|---:|---|
| actionlint / one-off |[-.09924,.05921] |Practical equivalence at the10-point margin |
| actionlint / recurring2×3 |[-.24001,.01413] |Inconclusive |
| actionlint / disappears1×3 |[-.13144,.05456] |Inconclusive |
| Fortio / all three |[-1,-.88026] |Clears the hypothetical10-point improvement gate |

This sensitivity does not change the primary IID=false protocol, establish its
hypotheses, or turn the desktop cohort into a statistically qualified promotion.
Actionlint's observed12-point recurring improvement is still inconclusive under
that calculation; the unfavorable and unresolved comparisons remain visible.

## Reproduction and evidence identity

The pilot measurement producer is clean source
`164046036d732004095ad3f568bc2e0bb0bbd126`, binary SHA-256
`78bb473abc4fa17ae24bdfd71a13dc0270a570736ac72edd83f10e10c07a98be`.
Its frozen request is committed at`01d577c`; uncompressed report SHA-256:
`4f8f5b8fd86343abf3436581c1e756d799f1171a4c7a90231e9c9ad9397a5c9b`.

The confirmation producer is clean source
`fbf5947dd3b19e356bc4fa1dee3d478274d09ff8`, binary SHA-256
`5acfc75323cb281e7403ba94052a3bb66786dde803416df994009709c3be3742`.
The only intervening Go change adds the separately prespecified sensitivity
analysis/CLI; the measured runner/process/preparation files are unchanged.
The final request is frozen at`96799be28bd41309bca7f953a5da1453a62de25d`, SHA-256
`e12ecd0fab49f9417976894aa7a84f86ea890c406edee97f63916e4b38050392`.
Uncompressed confirmation report SHA-256:
`6b6a5b88f8ab91a8d99cd0fac59673fba3de0960020fdc6a5c2b34ac102c343f`.

Go1.27.3 built both producers. The shared held-out recipe is
`f87db35fdf9fc7f51d262bd15634a3dd19c01a5e`; production packer/full-stub Go
source is unchanged from main`142583b`. The pinned production CLI, full stub,
corpus manifest, external modules/source commits, input hashes and prepared
artifact identities are retained in`evidence/*-artifacts.json`. Reconstructing
pack artifacts can change creation-time metadata, so preserve and verify the
recorded artifact hashes when replaying the frozen requests.

The raw report gzip, primary summary, separately labeled IID sensitivity,
preparation recipes and their checksums are under`evidence/`. The original
journals, binaries, prepared artifacts and uncompressed reports remain under
`.work/research380/`. Re-run analysis after decompression with the same CLI; it
performs no launches. Root coordinates full repository qualification separately.
The final focused race checks passed95.2% runner coverage and98.9% reused-inference
coverage; pinned golangci-lint2.15.0 reported zero issues before measurement.
An independent read-only jq audit recomputed all1,200 unique episode identities,
12×100 group counts,4,000 dispatch/output identities, SLO maxima, schedule/ready
arithmetic, earliest first-ready, CPU totals and logical byte-time. Its result
and the exact audit query are retained alongside the report. This audits data
consistency; it does not establish IID or authenticate host observations.

No overlap/contention cohort, deployment trace generalization, native arm64,
alternate codec/profile or actual long-gap page-cache decay was measured. Keep
those boundaries when deciding whether a particular deployment should prewarm.
