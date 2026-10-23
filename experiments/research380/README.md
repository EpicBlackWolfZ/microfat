# Explicit selected-variant prewarm experiment (#380)

This isolated Linux research program compares existing **auto dispatch without
prewarm** against **one deployment-time, selected-variant prewarm**. It adds no
resident agent, learning policy, expiry mechanism, production format or dispatch
default. The launcher still verifies the executed descriptor SHA-256. Each timed
launch enables identical `MICROFAT_LOG=json` logging; its overhead is included.

The endpoint is completion of a short, terminating version/readiness command,
checked against the exact native selected variant's stdout digest. It includes
process creation, launcher, native ELF bootstrap, output and exit. It is not
server/network readiness or request-service latency. Demo reports `ready`; the
other fixtures report versions. No vulnerability scanning or arbitrary jobs run.

## Prospective protocol

1. Prepare artifacts outside timing with `--mode prepare --request prepare.json
   --output /absolute/new/artifacts`. The bounded JSON names a pinned corpus
   manifest and its SHA-256, production microfat/full-stub paths and hashes,
   families, profile and selected native level. Preparation verifies all selected
   input hashes, packs zstd/format2 with the full stub, verifies payload integrity,
   and captures exact native stdout. `artifacts.json` retains all commands, source
   module/version/commit/sums, and diagnostic preparation process accounting.
   Schema2 is the held-out corpus; schema1 is accepted only for the explicitly
   named demo/microfat training corpus with `profile=training`.
2. Pilot only demo and microfat, 12 episodes per arm per scenario. Pilot results
   select a useful SLO and expose failures; they cannot confirm an improvement.
3. Before any held-out timing, commit the protocol and confirmation request with
   the pilot report digest, SLO, margin, error family, timeout and fixed N. The
   planned confirmation is actionlint and Fortio, 100 episodes per arm per
   scenario: 1,200 episodes total, six prespecified comparisons. No candidate,
   threshold, scenario or sample-size changes after confirmation starts.
4. Run `--mode run --request frozen.json --output /absolute/new/campaign` using a
   clean, versioned producer built with `-ldflags '-X main.producerRevision=SHA'`.
   The request source revision must match. The runner bounds each command to at
   most one minute and each campaign to at most 20 minutes. The entire pilot plus
   confirmation timing allocation is at most 20 minutes, managed by the study
   owner; a budget stop retains the attempted episodes and marks the report
   incomplete. It does not silently shrink the planned denominator.
   Each attempted episode is appended to `episodes.jsonl` outside the launch
   timer; the final report is written once. This avoids repeatedly rewriting a
   growing report between timed trials. The journal is not an fsync durability
   guarantee. A request is capped at 12,000 process attempts.
5. Analyze with `task research-380 RESEARCH_INPUT=/absolute/report.json`, or
   `--mode analyze --request report.json`. Retain harmful and inconclusive groups.

Each request explicitly supplies one-off, recurring bursts, and disappearing
recurrence schedules as nonnegative ordered offsets. Launches at the same offset
run concurrently. Every episode owns a fresh private cache. Arm order alternates
by complete block; app/scenario order is fixed and recorded. **The independent
unit is the complete deployment/launch episode, never an individual correlated
launch within it.** The outcome is any failed/timed-out operation or any launch's
scheduled-arrival-to-command-completion duration exceeding the SLO. Preparation
or postmeasurement cache-verification failure makes the episode a failure too.
All planned launches are attempted even when preparation fails.

The foreground launch clock starts after the prewarm attempt finishes. Therefore
foreground SLO improvement alone does not establish lifecycle value. Reports also
retain initial preparation delay, deployment-to-first-ready, preparation plus
foreground span, total preparation plus foreground CPU, raw per-command CPU/RSS,
and Linux input/output block counters. Later scheduled launches can overlap; sums
of their wall times are not an episode span. Postmeasurement cache verification
is a research correctness check, recorded separately and excluded from policy ROI.

Cache file bytes times a **fixed logical retention horizon** gives modeled retained
byte-seconds. The runner does not wait out that horizon. It neither measures RSS
residency nor implements eviction; baseline fallback-created cache files receive
the same conservative full-horizon charge. Disappearing recurrence keeps the
unused retention charge visible. Linux `ru_inblock/ru_oublock` count 512-byte block
I/O, not logical bytes decoded, hashed or copied; page-cache hits can report zero.
Do not turn zero block counters into a claim of zero I/O work.

Warm launch telemetry must report cache mode and the pinned selected payload
level/digest/size. Never-prewarm auto may legitimately fall back from memfd to
cache; its actual mode is retained rather than relabeled as a cold memfd launch.
Every launch checks telemetry and native output, and prewarmed cache entries are
verified after the measured episode. The fixed environment is PATH=/usr/bin:/bin,
LANG=C, LC_ALL=C, TZ=UTC, GOMEMLIMIT=off, the requested GOMAXPROCS and explicit
MICROFAT cache/auto/level/log variables. Ambient preload, debug or home settings
are not inherited. Requested process concurrency is not a hard host CPU quota.

## Statistical and cost gates

The analyzer always reports attempted episodes, failures and SLO misses. Latency
and cost medians use successful episodes only and carry their own sample counts;
failed episodes remain in the risk denominator and raw records. No sample p99 is
promoted to a population p99 claim. Exact fixed-sample SLO-risk intervals appear
only for complete confirmation reports with an explicit IID assumption. A source
revision, fresh cache or alternating order does not establish independence or
stationarity on a shared desktop. Such intervals remain conditional; leave IID
false for descriptive evidence when that assumption is unsupported.
The frozen protocol additionally prespecifies `--mode iid-sensitivity` as a
separate hypothetical-IID calculation; it retains the original protocol's IID
value and report digest, and never replaces the primary descriptive analysis.

The practical improvement gate is an entire simultaneous candidate-minus-baseline
risk interval below the predeclared negative margin. Practical harm uses the
positive margin; otherwise retain practical equivalence or inconclusive. Family
error includes all six prespecified comparisons, including unfavorable results.
At N=100 and this allocation, even zero violations cannot certify a 1% risk.
The pilot must freeze a useful SLO; do not choose a flattering threshold after
looking at held-out data.

Prewarm ROI also requires complete preparation cost and storage budgets. In a
simplified otherwise-cold sequential workload, N(C-W)>P is the wall-cost break-even
screen; concurrent episodes require the actual measured span and CPU accounting.
CPU, wall time, block I/O and byte-time are separate budgets, not interchangeable
units. One-off demand can improve foreground readiness while losing overall cost.
If deployment-time prewarm suffices, stop before adaptive retention complexity.

This bounded study does not control kernel page-cache coldness, globally drop
caches, isolate desktop scheduling, or reproduce native arm64. App/profile/codec,
host and quota generalization require later independently qualified cohorts.
An overlapping prewarm/foreground contention study, if run, is a separate
descriptive cohort and cannot alter the primary six-comparison decision family.

## Reused inference provenance

`tailrisk/analysis.go` and `tailrisk/bounds.go` are byte-identical copies from
#377 at `bd6bc28a5cb31bea8e5be27ec1029d7aba2e6d66`:

```
a6d72e209173dec9cb8e71e603a8a420465a9f3f92bd4398ae4f7305f2dd6cf0  analysis.go
c4e149a7051a0d649ba7a3d366e44f0275bbdc63ace54eef4c2957b1f2cda9ec  bounds.go
```

The copied tests use tiny calibration constants/helpers in `reuse_test.go`; the
inference implementation is unchanged. It uses fixed-sample Clopper–Pearson
intervals and a Bonferroni union bound over arms and comparisons. See the original
[NIST binomial interval reference](https://www.itl.nist.gov/div898/handbook/prc/section2/prc241.htm).
It does not replace the existing paired bootstrap or support optional stopping.
