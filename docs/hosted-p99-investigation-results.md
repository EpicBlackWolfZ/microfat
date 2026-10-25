# Historical service-p99 investigation results

**Both historical signals remain inconclusive.** The frozen investigation
completed 400 successful memory/v3 trials across two independent hosted jobs.
Neither primary interval establishes improvement, regression or practical
equivalence under the prespecified rule. Both mixed/v4 jobs stopped before
timing because their hosts lack v4, providing **zero service observations**.
No runtime fix is justified by this investigation, and
[#230](https://github.com/EpicBlackWolfZ/microfat/issues/230) retains unresolved
acceptance for maintainer review.

## Frozen source and scope

[Run 40139089920, attempt 1](https://github.com/EpicBlackWolfZ/microfat/actions/runs/40139089920)
used harness/protocol source `cedeb50c18901b1c7fcdf8550cb04126c5821def`.
The [prospective protocol](hosted-p99-investigation.md) fixed four cohorts,
all ten arms, 20 blocks per job, windows, margins, multiplicity and failure
handling before collection. There were no retries, replacement cohorts,
sample increases or tier substitutions.

Packer/stub base is `3d8496b6f01f97a2fae1b27ff04215f9b8fad257`; head and
shared native payload source are
`9607f3a0bb5954f37e8775351d76cee3398574a8`. Base/head native controls execute
identical bytes; historical base/head packers and stubs package the same
native payloads. Full launchers, format 2, zstd without a dictionary, workload
seed 1 and schedule seeds 23001/23002 are retained. The memory payload remains
v3 even on a host capable of v4.

Historical sources were rebuilt with required **Go 1.27.3 and Fortio 1.76.0**.
Original Go 1.27.1/Fortio 1.75.2 executable bytes, VM images and some CPU
models differ. This is a historical-source replication under a refreshed
toolchain, not an original-byte reproduction. Source, module, executable
digest and build-setting identities are retained per artifact.

The endpoint remains the median paired difference between **per-trial
service-request p99 estimates**. Requests within a trial, process-startup
clocks and observer samples add no independent blocks. Each complete cohort
has 20 paired blocks for each comparison; cohorts and requests are not pooled.

## Collection and controls

| Scenario / cohort | Required payload tier | Successful / planned trials | Disposition |
| --- | --- | ---: | --- |
| memory32 / 0 | v3 | 200 / 200 | complete, hosted-qualified; inference inconclusive |
| memory32 / 1 | v3 | 200 / 200 | complete, hosted-qualified; inference inconclusive |
| mixed4 / 0 | v4 | 0 / 200 | pretrial host incompatibility |
| mixed4 / 1 | v4 | 0 / 200 | pretrial host incompatibility |

The memory jobs used Ubuntu image `20261023.334.3`; both mixed jobs used
`20261012.334.1`. All four had kernel `6.17.0-1022-azure` and four logical
CPUs. The original image was `20260907.300.1` with the same kernel release.

| Cohort | Hosted processor | Relevant departure |
| --- | --- | --- |
| memory32 / 0 | AMD EPYC 9V45 96-Core Processor | Different from original memory EPYC 7763; fixed v3 payload retained |
| memory32 / 1 | AMD EPYC 7763 64-Core Processor | Same advertised model as original memory, different VM/tool/image |
| mixed4 / 0 and 1 | AMD EPYC 7763 64-Core Processor | No AVX-512F/v4; original mixed host was EPYC 9V74/v4 |

Memory measurement retained all 400 scheduled outcomes, with no failed,
time-limited or missing trials and no HTTP failures. Every successful trial
verified requested/effective target CPU0, generator CPU3, a separate one-CPU
quota (`100000 100000`) and 2 GiB cgroup-v2 memory ceiling per role. Payload
runtime observations verified GOMAXPROCS=2, GOMEMLIMIT=1 GiB, GOGC=100 and
MICROFAT_AUTOTUNE=0. Fat arms additionally verified the selected v3 payload and
its native digest; native generic controls remain v1. Affinity does not
establish exclusive physical cores; sibling topology is retained.

Each trial used 10 s warmup, 30 s load and 250 ms observation sampling at
QPS 1,000/concurrency 16. Cohorts 0/1 retain respectively 5,999,929/5,999,918
load requests, 1,999,962/1,999,919 warmup requests and 32,200 telemetry samples
each. All required observation windows were valid. These counts do not increase
the independent sample size beyond 20 blocks per cohort.

The mixed jobs' compatibility checks failed before the first trial. Control
preparation succeeded, plans retained all 200 scheduled positions, and cleanup
and failure-summary upload succeeded. Effective trial controls, HTTP outcomes,
timing and telemetry windows are unavailable for these jobs. Their absence
cannot establish equivalence. The overall workflow conclusion is failure
because of these retained pretrial failures; both memory jobs succeeded.

## Paired service-p99 results

Differences below are head minus base in microseconds; negative favors head.
The percentage is the median of paired percentage changes, not a ratio of
separate medians. Decision intervals use the frozen exact order-statistic
construction, ranks 4 and 17 of 20 differences, with family allocation
`0.05 / 12`. Their individual achieved coverage is at least
`0.9974231719970703` **conditional on independent stationary block differences**.
Shared-host scheduling does not establish that condition. The memory practical
margin is ±250 µs; both endpoints must be inside it for equivalence.

| Cohort | Comparison | Median difference, µs | Median paired change | Simultaneous interval, µs | Decision |
| --- | --- | ---: | ---: | --- | --- |
| 0 | Native generic | −104.803 | −3.317% | [−377.216, +454.398] | inconclusive |
| 0 | Native specialized | −89.803 | −2.787% | [−445.680, +442.051] | inconclusive |
| 0 | **Cold-cache primary** | **−69.497** | **−2.359%** | **[−592.061, +268.951]** | **inconclusive** |
| 1 | Native generic | −36.029 | −0.828% | [−201.130, +103.112] | practical equivalence |
| 1 | Native specialized | +5.485 | +0.126% | [−223.117, +156.494] | practical equivalence |
| 1 | **Cold-cache primary** | **−124.490** | **−2.795%** | **[−369.315, +163.340]** | **inconclusive** |

Cohort 0 cannot establish native-control equivalence, and its primary interval
extends beyond both practical boundaries. Cohort 1 establishes equivalence for
both native controls, but its primary interval extends below −250 µs and does
not establish primary equivalence or improvement. Neither cohort triggers the
frozen rule for a control-supported repeat requiring a production-fix study.
Selecting cohort 1 alone would not resolve the scenario.

The original seeded 10,000-resample bootstrap 95% intervals remain diagnostics,
separate from the simultaneous decision rule:

| Cohort | Cold-cache primary diagnostic interval, µs |
| --- | --- |
| 0 | [−470.013, +202.333] |
| 1 | [−220.076, +50.863] |

Other fat arms are retained as descriptive observations; they are not extra
decision candidates or substitutes for the original cold-cache comparison:

| Cohort | Arm | Median difference, µs | Median paired change | Diagnostic bootstrap 95% interval, µs |
| --- | --- | ---: | ---: | --- |
| 0 | memfd | −21.072 | −0.752% | [−148.890, +306.428] |
| 0 | Warm cache | +62.773 | +2.208% | [−243.463, +181.440] |
| 1 | memfd | +110.292 | +2.577% | [−111.248, +143.658] |
| 1 | Warm cache | +10.099 | +0.228% | [−81.559, +102.380] |

All five paired comparisons, their raw pairs, diagnostics and other metrics
remain in each complete bundle. No service-p99 estimate exists for mixed/v4.

## Host and order variation

The following strata are descriptive, with no new inferential comparisons.
First/last groups each contain ten planned blocks. Arm positions are
counterbalanced; this does not guarantee equal base-first/head-first counts
for every pair.

| Cohort | Comparison | First 10, µs | Last 10, µs | Base first: count / median µs | Head first: count / median µs |
| --- | --- | ---: | ---: | --- | --- |
| 0 | Native generic | −272.383 | +168.208 | 9 / −314.327 | 11 / +165.720 |
| 0 | Native specialized | +19.743 | −263.778 | 16 / +0.481 | 4 / −214.484 |
| 0 | memfd | −8.692 | −21.072 | 15 / −2.296 | 5 / −39.848 |
| 0 | Cold-cache primary | −115.689 | −69.497 | 5 / −138.448 | 15 / −2.744 |
| 0 | Warm cache | −16.646 | +112.675 | 11 / −108.340 | 9 / +178.822 |
| 1 | Native generic | −36.029 | −84.216 | 12 / −88.298 | 8 / −22.730 |
| 1 | Native specialized | −122.599 | +68.539 | 10 / +35.007 | 10 / −17.175 |
| 1 | memfd | +139.224 | +72.913 | 5 / +160.612 | 15 / +63.899 |
| 1 | Cold-cache primary | −19.951 | −206.316 | 4 / −76.873 | 16 / −125.505 |
| 1 | Warm cache | −7.792 | +10.099 | 5 / +27.787 | 15 / −6.718 |

The native controls' changing directions, unequal pair-order counts and
different primary time strata leave stationarity and causal attribution
unestablished. These observations do not prove a particular host, launcher or
observer cause. CPU governor/frequency telemetry was unavailable; shared VM
hardware stability and filesystem page-cache state remain uncontrolled. Some
exec diagnostics miss the payload exec entry, so their absence cannot identify
an extraction-only duration. All these limitations remain in qualification.

## Relation to the original signals

The immutable [v0.2.4 evidence](hosted-p99-investigation.md#immutable-historical-comparison)
retains these original observations:

| Scenario / original arm | Median difference, µs | Median paired change | Original bootstrap 95% interval, µs |
| --- | ---: | ---: | --- |
| memory32 / Cold cache | +237.981 | +5.0689% | [+24.141, +382.607] |
| mixed4 / memfd | +13.463 | +8.7306% | [+1.617, +59.160] |

The new memory point estimates have the opposite sign, but their simultaneous
intervals remain too wide for the frozen decision. This does not explain away
the original positive cohort or establish precise nonreproduction. Native
control changes, host/order variation, multiplicity in the original screening
and changed executable/VM identities remain plausible contributors, not
demonstrated causes. The mixed comparison has no new observations at its
required v4 tier. No production change or regression-free claim follows.

## Retained evidence and replay

All four original ZIP containers match GitHub's artifact SHA-256 metadata:

| Cohort | Hosted artifact | Original ZIP SHA-256 |
| --- | --- | --- |
| memory32 / 0 | [12581410781](https://github.com/EpicBlackWolfZ/microfat/actions/runs/40139089920/artifacts/12581410781) | `9f5f0be8c3c258f26498f330a593c8f7455ef3e788e21d77a7609942ea9a57e0` |
| memory32 / 1 | [12582460513](https://github.com/EpicBlackWolfZ/microfat/actions/runs/40139089920/artifacts/12582460513) | `cf3897dfb0760f1a8e1b7373a4524020dc7c3fcc3046ae68970a409bcd60c5e5` |
| mixed4 / 0 | [12577093777](https://github.com/EpicBlackWolfZ/microfat/actions/runs/40139089920/artifacts/12577093777) | `0280bd119305fdf42fe07d1f393e7804c69f4c38ed9f07fbc80cf2e38a854638` |
| mixed4 / 1 | [12577393411](https://github.com/EpicBlackWolfZ/microfat/actions/runs/40139089920/artifacts/12577393411) | `7b9d7ed544303987d859aa013ec22d96750e75caad0074de28054f019f28ed10` |

The ZIP files have 14-day hosted retention. Durable, checksummed repository
archives retain every extracted artifact file, tool identities, logs, host
snapshots, plans/schedules, historical source snapshots, complete/failed
summaries and the frozen measurement source:

| Cohort | Retained archive | SHA-256 sidecar |
| --- | --- | --- |
| memory32 / 0 | [Archive](../benchmarks/testdata/p99-230/hosted-40139089920-1-memory32-0.tar.gz) | [Checksum](../benchmarks/testdata/p99-230/hosted-40139089920-1-memory32-0.tar.gz.sha256) |
| memory32 / 1 | [Archive](../benchmarks/testdata/p99-230/hosted-40139089920-1-memory32-1.tar.gz) | [Checksum](../benchmarks/testdata/p99-230/hosted-40139089920-1-memory32-1.tar.gz.sha256) |
| mixed4 / 0 | [Archive](../benchmarks/testdata/p99-230/hosted-40139089920-1-mixed4-0.tar.gz) | [Checksum](../benchmarks/testdata/p99-230/hosted-40139089920-1-mixed4-0.tar.gz.sha256) |
| mixed4 / 1 | [Archive](../benchmarks/testdata/p99-230/hosted-40139089920-1-mixed4-1.tar.gz) | [Checksum](../benchmarks/testdata/p99-230/hosted-40139089920-1-mixed4-1.tar.gz.sha256) |

Follow the [archive verification and offline replay recipe](../benchmarks/testdata/p99-230/README.md).
Each archive has an internal file manifest and audit; repackaging changes the
container digest while preserving every original extracted byte. The frozen
measurement source supports replay on a copy. Raw historical worktree `.git`
pointers are provenance, not portable Git checkouts. Evidence fixtures add to
source checkout size but are outside GoReleaser binary-archive file lists.
The four archives total 49,827,502 bytes (47.5 MiB). The
[final run metadata](../benchmarks/testdata/p99-230/run-40139089920-1.json) and
[its checksum](../benchmarks/testdata/p99-230/run-40139089920-1.json.sha256)
retain the completed overall failure separately from earlier in-progress
metadata snapshots inside the mixed archives.

Independent validation checked every bundle checksum, historical and measurement
source blob, source/tool/payload/runtime/control identity, scheduled outcome,
external telemetry window and all 800 memory warmup/load Fortio outputs.
Both complete bundles replay their JSON/Markdown reports; both pretrial failure
summaries also replay exactly. A separate standard-library arithmetic audit
matches all five comparisons, exact binomial interval ranks/coverage and drift
strata in both memory cohorts without arithmetic, scope or identity mismatches.
No favorable subset was selected and no raw evidence was rewritten.

## Source qualification and disposition

The complete serial local Task pipeline passed at clean validation head
`0b1649306bc0627fbc87d10445a0f622932fb7c7` on Go 1.27.3: pinned lint and
vulnerability tools, mandatory SBOM tests, default/minimal race suites,
format/module checks, runtime metadata recipes and builds. Exact merged
coverage is **18,308 / 19,080 statements = 95.953878%**. Focused investigation
coverage is 96.3%, command coverage 100%.
[Source CI at that head](https://github.com/EpicBlackWolfZ/microfat/actions/runs/40140850298)
passed all 40 checks, including `CI Complete` and every Codecov component.
A tests-only follow-up to the frozen source covers unavailable source checkouts,
corrupt Git indexes and unresolved relative paths after the Benchmark Harness
patch gate identified uncovered error paths. Measured production code and
decision rules remain unchanged. Independent source/evidence review found no
remaining actionable findings.

A separate, unqualified local smoke built both historical revisions, ran all
ten memory/v3 arms once with 200 ms load windows and verified its bundle offline.
It establishes orchestration feasibility and provides no latency inference.
Source CI and hosted evidence qualification do not establish a p99 improvement
or release hardware qualification; `release_eligible=false` remains explicit.

The bounded collection/report deliverable is ready for review with **both
signals unresolved**. Unmet acceptance is precise, independent confirmation
of the memory signal and any new measurement of the original mixed/v4 signal.
Maintainer disposition may accept the explicitly inconclusive bounded report
or request a separately frozen follow-up. A follow-up would need v4-capable
hosts for mixed, stronger precision/control planning for memory, retained
current failures and explicit tool/host identity departures. It cannot silently
replace these cohorts, extend their sample budget or declare equivalence from
intervals containing zero. A production fix requires a control-supported repeat
and its own bounded before/after evidence.

This report does not close #230 or authorize merge or release.
