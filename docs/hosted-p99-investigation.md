# Controlled investigation of the v0.2.4 service-p99 signals

This protocol freezes the bounded hosted investigation for
[#230](https://github.com/EpicBlackWolfZ/microfat/issues/230) before new
measurements. Its endpoint is the median paired difference between **per-trial
service-request p99 estimates**, with a separate median of paired percentage
changes. Requests and observer samples inside a trial are not independent
repetitions. Process-completion clocks, launcher subphases and fixed-SLO
binomial bounds do not replace this endpoint.

The [results and retained evidence](hosted-p99-investigation-results.md) record
the disposition of the frozen collection; this protocol's measurement source
remains pinned in that report.

## Immutable historical comparison

[Run 35549234214, attempt 1](https://github.com/EpicBlackWolfZ/microfat/actions/runs/35549234214)
compared packer/stub base `3d8496b6f01f97a2fae1b27ff04215f9b8fad257` with
head `9607f3a0bb5954f37e8775351d76cee3398574a8`. Both packed the same native
payloads from the historical head. The checksummed
[v0.2.4 evidence archive](https://github.com/EpicBlackWolfZ/microfat/releases/download/v0.2.4/benchmark-evidence-9607f3a0bb5954f37e8775351d76cee3398574a8-35549234214-1.tar.gz)
has SHA-256
`20a0d4d61a27754ec3b4f410d37b76a447b38ae4b6916412e802814d1cfd41a6`.

| Scenario | Original comparison | Specialized level / hosted CPU | Median paired p99 change | Median difference, original paired 95% interval |
| --- | --- | --- | ---: | ---: |
| `memory32`, `bundles/02` | base → head, cold cache | v3 / AMD EPYC 7763 | +5.0689% | +237.981 µs [24.141, 382.607] |
| `mixed4`, `bundles/05` | base → head, memfd | v4 / AMD EPYC 9V74 | +8.7306% | +13.463 µs [1.617, 59.160] |

The original Go toolchain was 1.27.1 and Fortio was 1.75.2 at source
`630bfb3db6cf663b4e01524f9e9be945e44f8a20`; its amd64 executable digest was
`b9a14d7829d5a829d95f0147a78b7d55d96c5b671f25e2871d651e02cff5d776`.
The original VM image was `ubuntu24`, version `20260907.300.1`, with kernel
`6.17.0-1022-azure`. The archive retains 12 shards and 2,400 trials; these
two bundles each contain 200 trials. Their checksum-manifest digests are
`cb68f542e1bec71dffe6aa5cdad4895972397180de0e5e4e81cf61a4a59eaea3`
and `8aba6bb108a1d999b49c137da69889f818d1d4b74d08f7994c493f23e215a1a5`.

## Fixed collection and resource budget

Dispatch the `Benchmarks` workflow at the reviewed investigation commit with
tier `historical-p99`. The fixed matrix has four independent `ubuntu-24.04`
VM jobs: two scenarios × cohorts 0 and 1. Each job has a 180-minute wall-clock
limit; measurement has a 165-minute bound, leaving time for retained summaries
and teardown. There are no automatic retries, replacement cohorts, adaptive sample
increases or decisions to stop after a favorable result. Preserve a failed or
time-limited job as part of this cohort; a later protocol requires a separately
identified investigation.

Each job runs all ten original arms: base/head native generic, base/head native
specialized, base/head fat memfd, base/head fat cold cache and base/head fat
warm cache. Native base/head controls execute identical bytes. All payloads
come from the historical head; only the packer/stub source differs between fat
arms. Use full launchers, format 2, zstd without a dictionary, and generic v1
plus specialized **v3 for memory32** or **v4 for mixed4**. A host without the
required specialized level fails explicitly; do not substitute another level
or drop that outcome.

| Setting | Frozen value |
| --- | --- |
| Independent block unit | One complete block containing all ten arms |
| Blocks / scenario repetition | 20 |
| Workload / iterations | memory / 32; mixed / 4 |
| Native payload size / workload seed | 4,096 bytes / 1 |
| Per-trial warmup / load | 10 s / 30 s |
| Observer sampling interval | 250 ms |
| Fortio QPS / concurrency / histogram resolution | 1,000 / 16 / 0.00001 s |
| Schedule seed | `23001 + cohort` |
| Runtime controls | GOMAXPROCS=2, GOMEMLIMIT=1 GiB, GOGC=100, MICROFAT_AUTOTUNE=0 |
| Target / generator affinity | Original disjoint logical CPUs 0 / 3; fail if unavailable |
| Each target / generator cgroup | v2, CPU quota 100000 µs / period 100000 µs, memory 2 GiB |

Counterbalance arm positions within each repetition using the recorded seeded
schedule. Preserve the complete schedule before collection and every trial in
its scheduled position. Four jobs × 20 blocks × ten arms gives **800 trials**;
warmup plus load totals approximately **8.89 runner hours**, excluding builds,
startup, diagnostics and teardown. The job timeout is the final resource bound.
Do not run concurrent tests or other benchmark workloads deliberately on a VM.

The standard delegation controls select the first and last allowed logical CPUs
and record requested and effective limits. This does not guarantee separate
physical cores; retain sibling topology. The investigation additionally requires
the original 0/3 mapping and fails if the delegation selects other CPU identifiers.
Retain host model, kernel, image,
topology, frequency/governor availability, load, control application, order and
timing telemetry. Cold cache means the extracted payload cache is absent;
filesystem page-cache state remains uncontrolled.

## Explicit reproduction limits

Build the historical sources with the required current **Go 1.27.3** and use
the currently pinned **Fortio 1.76.0**. Record module sums, source revisions,
build settings, executable digests and running harness identity. This is a
controlled historical-source replication under a refreshed toolchain, not an
original-byte reproduction. The VM image, kernel and processor can also differ;
report those differences alongside the original identities above. No new
current-version-only comparison replaces either historical source arm.

Keep Fortio's uncorrected service-time histogram and the original warmup/load
windows. The observer remains enabled; retain its telemetry and exec-diagnostic
availability. The original exec diagnostic sometimes missed the payload exec
entry, so an absent diagnostic cannot establish an extraction-only duration.
Retain startup metrics as context, separately from the request endpoint.

## Frozen analysis and decisions

Analyze each scenario and VM cohort separately; do not pool requests, blocks
from different VMs or architectures to obtain a favorable conclusion. For each
of the four jobs, retain three prespecified paired p99 comparisons: its original
fat signal, native generic and native specialized. Other collected fat arms and
other metrics remain descriptive, with all contradictory observations retained.

The practical absolute margins are **250 µs for memory32** and **10 µs for
mixed4**, applied equally to each scenario's signal and its native controls.
The primary estimand is the median paired absolute difference. Report the
median paired percentage change separately; it is not a ratio of independently
aggregated medians and its interval is not inferred from the absolute interval.

Report the original seeded **10,000-resample paired bootstrap 95% interval** as
a diagnostic to aid comparison with history. For decisions, use conservative
exact order-statistic intervals for the median paired difference, allocating
the family error probability `0.05 / 12` across the twelve prespecified
comparisons. The binomial order-statistic construction follows
[NIST Technical Note 2119, quantile confidence intervals](https://nvlpubs.nist.gov/nistpubs/TechnicalNotes/NIST.TN.2119.pdf).
For 20 pairs the selected endpoints are ordered differences 4 and 17, with
individual coverage at least 0.9974231719970703 under the model.
These simultaneous intervals condition on independent,
identically distributed block differences within each VM; shared-host drift
or order effects can invalidate that assumption. They do not certify the
precision of a population request p99 or make requests independent trials.
Report observed temporal/order drift and the applicability of this condition.

A simultaneous interval wholly above the positive practical margin supports
regression; wholly below the negative margin supports improvement; wholly
inside the two margins supports practical equivalence. All other positions
are inconclusive. A missing, failed or time-limited required trial, an invalid
measurement window, unapplied control, or either native control failing to
establish practical equivalence makes that scenario/cohort inconclusive for
launcher attribution. A confidence interval containing zero is not evidence
of equivalence.

Require consistent classifications across both independent VM cohorts before
describing a scenario as repeated or practically equivalent. Contradictory
cohorts remain inconclusive; do not choose a favorable subset. An observed
signal with native-control changes is evidence to investigate host/order or
observer effects, not proof of launcher causality. If a repeat survives the
controls, any proposed production fix requires its own bounded before/after
protocol and retained correctness evidence.

## Evidence and disposition

The workflow runs `prepare`, `measure` and `summarize` through
`go run ./internal/cmd/p99-investigation`. Preparation records historical
worktrees, config, schedule and provenance before measurement. Measurement uses
the owned delegation; cleanup drains it outside that delegation. Summarization
and artifact upload run even after failure. Retain stdout/stderr, tool identity,
controls, schedules, native and fat outcomes, partial bundles, telemetry and
reports. Preserve checksums when retaining artifacts beyond hosted expiry.
An incomplete, unqualified or protocol-invalid cohort writes its summary and
fails the workflow. Complete qualified measurements with an inconclusive
statistical or attribution result retain that result without failing merely
for its direction or uncertainty.

The report must state improvement, regression, practical equivalence or
inconclusive for each scenario, explain the original contradictory cohort, and
list unmet acceptance or required follow-up for maintainer review. A refreshed
toolchain equivalence result cannot establish equivalence on the original VM
and executable bytes. Green CI and hosted publication qualification are
separate from the service-p99 conclusion. No release, dedicated-hardware
certification, merge, or weakened descriptor/hash/owner/mode/seal check follows
from this protocol. Close #230 only through the reviewed merged deliverable
and its explicit disposition.
