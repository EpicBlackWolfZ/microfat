# Compression profiles: complete-cost evaluation and disposition

Retain the existing deterministic `latency`, `balanced` and `size` policies.
This is the bounded evaluation/disposition for
[#42](https://github.com/EpicBlackWolfZ/microfat/issues/42). No replacement
threshold, predictor, automatic dictionary search or new decoder is justified
by this collection. Explicit overrides and forced-dictionary semantics remain
unchanged. Retention does not establish that the defaults are optimal.

The original cohort failed application compatibility checks. A separately
frozen telemetry-off diagnostic completed successfully, but codec-specific peak
memory remains unresolved: the observed `wait4` RSS includes a pre-exec parent
high-water floor. Neither cohort establishes a general performance or memory
qualification. All attempts, failures and inconclusive comparisons are retained;
there is no third timing cohort or pooling of results.

## Frozen work and identities

| Cohort | Frozen source | Protocol | Result |
| --- | --- | --- | --- |
| C1 original | `df579d1dd804143b3efaddad1bc7b8f48f12f61f` | [Original](codec-policy.md) | 640 failed govulncheck memfd output checks |
| C2 isolated | `adaea0905e99518216ccf2191077cfc3f097cf51` | [Private telemetry-off diagnostic](codec-policy-isolated.md) | Every planned attempt passes collection checks; memory attribution unresolved |

Each cohort contains 264 timed full-stub Pack workers, 88 untimed minimal-stub
preparations and 11,880 timed launches: 10,560 fat and 1,320 native controls.
There are eleven groups, eight settings, three Pack rounds and twenty launch
rounds across full/minimal stubs and memfd/cold-cache/warm-cache paths. Both
cohorts completed every one of the 12,144 scheduled timed attempts.

The [source lock and corpus recipe](../experiments/codecpolicy/README.md) pin
Fortio 1.76.0, Task 3.54.0, actionlint 1.7.12 and govulncheck 1.9.0 by module,
module/go.mod checksums and source commit. Each has stripped/unstripped builds
at amd64 v1/v2/v3. Three additional groups are synthetic boundary ELFs. These
remain four application families, not eight independent applications. Source
lock SHA-256: `872a8fe7b2a481ff89edcf71f5945c8b98d79dc82d161066f986ab48511ffcfd`.

Collection used Go 1.27.3, CGO off, GOMAXPROCS 4, baseline-tier execution,
autotuning off, creation epoch zero and Format v2 on one shared native amd64
Ryzen 9 3950X workstation (32 logical CPUs, Linux 7.2.11-200.nobara.fc44).
There were no imposed CPU/memory caps or isolation guarantees. Raw host,
ancestor-cgroup, source/configuration, compiler/linker/collector, CLI, full/minimal
stub and input identities are archived. Production packer, codec and launcher
sources are unchanged from main `67624ef9644242be74556f28c7ed134292a33aab`.
The later report, CI-contract and owned-process cleanup commits are distinct
from the measured sources.

## Original failure and diagnostic boundary

All C1 Pack attempts and minimal preparations passed. Of its 11,880 launches,
640 failed exact output checks: 320 each with full and minimal stubs,
exclusively govulncheck's explicit memfd path. The process exited successfully,
but stderr reported a telemetry self-reexecution error. Nothing was filtered,
replaced or dropped. Both `cohort_pass` and `qualification_pass` remain false;
the collector returned nonzero. The final source was clean and all 57 frozen
identities matched.

The pinned govulncheck calls its linked `golang.org/x/telemetry` revision
`f71dc92da45e37d94eaf7a9e6de4b2cb8f88274a` before parsing `-version`. Its crash
sidecar uses `os.Executable()` and cannot reopen `/memfd:microfat_payload`.
Native/cache launches can start the sidecar in a separate session, outside the
collector's ordinary owned process group. This is different payload work, so
suppressing stderr would conceal the failure. The exact source hashes, module
checksums and source locations are retained in C2's `telemetry-rootcause.json`.
The ambient mode file was observed absent during C1; this was not an initial
configuration snapshot. General memfd self-reexecution support remains outside
this codec disposition.

C2 creates private bootstrap/evidence configurations with exact mode contents
`off 1970-01-01\n`, using absolute `XDG_CONFIG_HOME` for the collection wrapper,
its compiler/collector subprocesses, preparation, Pack workers, native
references/controls and fat launches. The earlier corpus build is outside this
isolation scope. Final checks
verify contents, ownership, permissions and identities. Parent Task's earlier
ambient `go env` metadata reads remain outside that guarantee. User telemetry
settings are untouched. Govulncheck uses the same fixed local version database
in both cohorts. C2 passed all 12,144 attempts and 88 preparations, including
640/640 govulncheck memfd launches; its source was clean and all 62 frozen
identities matched. Its green summary flags mean collection integrity and the
observed budgets passed, not that a replacement policy qualified.

No timing difference between C1 and C2 is attributed to telemetry. Their host
conditions, collection times and configurations differ. C2 does not rescue C1
or establish telemetry-enabled compatibility.

Later review identified a collector cleanup gap: when a command's leader exited
early, a child in its process group could outlive the completed command, even
with redirected output and a successful exit. The current collector observes
leader exit without reaping it, disables and drains group-cancellation callbacks,
cleans that owned group while its identity is still reserved, then reaps the
leader. Regressions cover inherited and redirected child output and cancellation
during leader exit. This fixes future collection; both frozen cohorts predate it and
did not verify the absence of such survivors. No historical contamination or
isolation is inferred, no timings are replaced, and C1 remains failed. Descendants
that leave the owned process group, including by creating a separate session,
remain outside this mechanism.

## Complete artifact bytes

These are full-stub Format v2 bytes, including stub, dictionary, frames, index
and trailer. Each of the 88 cells reproduced the same complete SHA-256 in all
three Pack rounds. All 352 timed/preparation component sums passed in each
cohort. All 88 full-artifact byte counts and hashes match between C1 and C2;
only deterministic artifact identities are compared across cohorts.

| Family / build | Balanced | Latency | Size (auto dict) | Best / no dict | Auto dict extra |
| --- | ---: | ---: | ---: | ---: | ---: |
| actionlint / stripped | 11,284,651 | 14,262,386 | 10,913,129 | 10,907,142 | +5,987 |
| actionlint / unstripped | 18,174,110 | 21,463,915 | 17,770,424 | 17,759,518 | +10,906 |
| Fortio / stripped | 26,133,547 | 35,269,028 | 24,972,518 | 24,931,916 | +40,602 |
| Fortio / unstripped | 47,016,805 | 56,923,069 | 45,807,384 | 45,747,870 | +59,514 |
| govulncheck / stripped | 17,532,112 | 22,499,507 | 16,779,119 | 16,802,260 | −23,141 |
| govulncheck / unstripped | 30,628,645 | 36,296,161 | 29,877,749 | 29,852,164 | +25,585 |
| Task / stripped | 49,798,459 | 71,975,832 | 47,062,109 | 46,982,460 | +79,649 |
| Task / unstripped | 95,628,756 | 120,206,291 | 92,735,955 | 92,652,961 | +82,994 |

The automatic best-level dictionary adds 5,987–82,994 complete bytes in seven
of eight groups (about 0.05–0.17%); it saves 23,141 bytes in the remaining group.
Forced balanced dictionaries help two groups and add bytes in the other six.
All settings preserve the empty raw `CompressionLevel` trainer input. These
observations support neither disabling all dictionaries nor an automatic
encode-both-and-choose feature. Historical artifact totals differ with their
source-built stub; the dictionary deltas reproduce the earlier characterization.

The valid syscall-only synthetic ELFs are exactly 524287, 524288 and 524289
bytes, with deterministic pseudorandom, approximately incompressible padding and identical bytes at all
three tier entries within each group. Actual Pack frames and native execution
confirm `latency` chooses none strictly below 512 KiB and LZ4 at/above it.
They establish that contract, not representative small-Go performance or an
optimal threshold. The proposed 256-KiB/4-MiB boundaries remain unsupported.

## Packing cost and unresolved memory attribution

C2 only: each entry is **median wall ms / median user+system CPU ms / maximum
observed wait4 RSS MiB**, over three fresh workers. The RSS column includes
pre-exec accounting and must not be interpreted as isolated encoder memory.
The [full Pack table](benchmarks/codec-42/c2-pack.md) lists all eight settings;
all individual samples are retained in the archive.

| Family / build | Balanced | Latency | Size |
| --- | ---: | ---: | ---: |
| actionlint / stripped | 228.784 / 268.683 / 80.926 | 131.690 / 139.128 / 55.945 | 1209.892 / 1252.531 / 229.492 |
| actionlint / unstripped | 282.804 / 331.694 / 81.266 | 152.197 / 161.245 / 55.945 | 1336.507 / 1363.522 / 240.129 |
| Fortio / stripped | 683.109 / 805.837 / 111.348 | 353.125 / 371.737 / 81.523 | 3885.934 / 4026.538 / 215.629 |
| Fortio / unstripped | 1021.024 / 1191.757 / 100.359 | 421.835 / 456.001 / 120.574 | 4400.004 / 4568.150 / 140.863 |
| govulncheck / stripped | 387.368 / 461.514 / 84.246 | 206.389 / 215.973 / 55.945 | 2272.081 / 2330.841 / 243.930 |
| govulncheck / unstripped | 435.875 / 519.049 / 64.668 | 251.856 / 258.917 / 74.148 | 2417.474 / 2514.144 / 253.047 |
| Task / stripped | 1943.735 / 2229.259 / 213.379 | 828.841 / 869.271 / 167.891 | 8424.608 / 8730.957 / 213.852 |
| Task / unstripped | 2000.274 / 2356.112 / 260.145 | 1035.834 / 1113.283 / 259.812 | 9629.846 / 9966.725 / 336.562 |

Pack includes child bootstrap, input reading, compression/training and synced
atomic publication; complete decompression/hash verification is outside timing.
C2's maximum Pack observation was 10.004 seconds and 344.715 MiB wait4 RSS;
C1's was 10.068 seconds and 368.574 MiB. These are separate maxima, not a paired
cross-cohort comparison. Observed budgets (120 seconds/2 GiB Pack,
10 seconds/512 MiB launch) are acceptance checks, not enforced memory caps.

The RSS audit found a parent-memory floor: even C2's tiny native boundary ELFs
reported large RSS. Across the 66 native cells, median RSS ranged from 233.684
to 243.680 MiB. The maximum launch RSS, 251836 KiB (245.934 MiB), exactly equals
the collector's final `/proc/self/status` VmHWM. Go 1.27.3 normally uses
[`CLONE_VFORK | CLONE_VM`](https://github.com/golang/go/blob/go1.27.3/src/syscall/exec_linux.go)
for these children. Upstream Linux 7.2 preserves the old address-space high-water
mark during [exec](https://github.com/torvalds/linux/blob/v7.2/fs/exec.c) and
includes it in [resource accounting](https://github.com/torvalds/linux/blob/v7.2/kernel/sys.c).
That mechanism explains the observed floor; the distribution kernel itself was
not separately audited. The retained values are real wait4 observations, but
cannot isolate encoder, decoder or payload allocation. Cgroup peak and unique
physical memory are unknown. Do not subtract a guessed floor or interpret an
observed RSS ratio as a codec-caused memory regression. Memory qualification
remains unresolved; a separately designed study would be required to resolve it.

## Complete process completion

C2 only: ratios compare each existing profile with existing `balanced` in the
same family/build, stub, path and round. Each range spans eight real groups;
each cell retains twenty pairs. Counts classify cell-wise 95% bootstrap ratio
intervals: equivalent wholly within [0.95,1.05], faster wholly below 0.95,
slower wholly above 1.05, otherwise inconclusive. These compare configurations,
not source-version regressions.

| Profile | Stub | Path | Median paired ratio range | Equivalent | Faster | Slower | Inconclusive |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| latency | full | memfd | 0.9398–1.1127 | 0 | 0 | 2 | 6 |
| latency | full | cold cache | 1.0638–1.1063 | 0 | 0 | 6 | 2 |
| latency | full | warm cache | 0.9861–1.0088 | 7 | 0 | 0 | 1 |
| latency | minimal | memfd | 0.8903–1.1065 | 0 | 0 | 2 | 6 |
| latency | minimal | cold cache | 1.0711–1.1101 | 0 | 0 | 7 | 1 |
| latency | minimal | warm cache | 0.9832–1.0064 | 8 | 0 | 0 | 0 |
| size | full | memfd | 1.2625–1.5316 | 0 | 0 | 8 | 0 |
| size | full | cold cache | 1.0679–1.1938 | 0 | 0 | 8 | 0 |
| size | full | warm cache | 0.9490–1.0060 | 7 | 0 | 0 | 1 |
| size | minimal | memfd | 1.2599–1.5096 | 0 | 0 | 8 | 0 |
| size | minimal | cold cache | 1.0767–1.1818 | 0 | 0 | 7 | 1 |
| size | minimal | warm cache | 0.9798–1.0363 | 7 | 0 | 0 | 1 |

An independent resampling implementation reproduced all 96 displayed policy
cells' paired ratio and absolute-saving intervals exactly from raw rows. The
archive also retains every candidate comparison against all three existing
profile anchors, rather than inferring a latency/size replacement from beating
balanced alone. Intervals use 2000 joint paired-round resamples, PCG seed
(42,42), midpoint medians and percentile endpoints floor(p×1999). They assume
approximately independent/exchangeable rounds; shared-host drift can violate
that assumption. They have no multiplicity, fleet or population-tail guarantee.
Three Pack rounds provide descriptive medians/ranges, not precise uncertainty.

The largest C2 launch wall observation was 243.236 ms. Native cell medians range
from 0.335–44.698 ms wall and 0.217–50.914 ms CPU. These short version/marker
commands measure completion including payload work and exit/output capture,
not readiness or service-request p99. Cold/warm refers to extracted payload
cache state. Pre-launch whole-file identity scans deliberately warm source
pages outside timing; OS caches are uncontrolled. Warm verified-cache launches
hash the payload and skip decoding. No disk-cold or network-transfer claim is made.

## Decision and review boundary

The fixed practical margins require benefit in every real family/build, with
bounded bytes, Pack time, resource cost and launch intervals against the
profile being replaced. No candidate has established that complete qualification.
The following C2 screen counts necessary byte/Pack-wall conditions out of eight
real groups and the profile-specific launch conditions out of 48 cells. A
replacement needs every group/cell to pass; memory remains unresolved separately.

| Explicit candidate | Latency Pack | Latency launch | Balanced Pack | Balanced launch | Size Pack | Size launch |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| none | 0/8 | 46/48 | 0/8 | 47/48 | 0/8 | 46/48 |
| LZ4 fastest | 8/8 | 15/48 | 0/8 | 23/48 | 0/8 | 47/48 |
| Zstd fastest | 8/8 | 18/48 | 4/8 | 18/48 | 0/8 | 47/48 |
| Zstd best, no dictionary | 0/8 | 32/48 | 0/8 | 38/48 | 0/8 | 47/48 |
| Balanced, forced dictionary | 0/8 | 17/48 | 1/8 | 16/48 | 0/8 | 46/48 |

Every candidate fails a necessary non-memory condition. Zstd fastest passes
latency's bytes/Pack-wall conditions but fails thirty launch cells, including
all sixteen memfd cells; its median memfd ratios against latency range from
1.0311–1.3214. Its resource readings are therefore not the sole barrier.
In particular, dictionary omission does not reach the 1% size-replacement
margin and loses the positive control; explicit LZ4 duplicates existing latency
artifacts. A smaller artifact or a lower Pack median alone cannot establish a
replacement. The observed memory ratios cannot close the unresolved resource
criterion. Keep the simpler existing policies and all explicit user controls.

Source validation separately exercises actual 512-KiB boundaries, raw-empty
training semantics, forced dictionaries, global/per-variant override precedence,
and unavailable-codec failure before/after partial work with atomic preservation.
Hidden collector/boundary packages are included in pinned lint/race checks and
native amd64/arm64 fixture CI. The existing format/profile/codec/memfd/cache
compatibility gates remain applicable. Native correctness CI is separate from
this one-host amd64 timing collection; there is no native arm64 performance claim.

This is a reviewable bounded evaluation and proposed retention disposition.
Required codec-specific peak-memory acceptance remains inconclusive for
maintainer disposition; collection pass flags do not complete that acceptance.
General memfd self-reexecution also remains explicitly unresolved.
It does not establish a universal no-regression result, resolve
[#230](https://github.com/EpicBlackWolfZ/microfat/issues/230), or authorize merge
or release. Issue #42 stays open until the deliverable is reviewed and merged.

## Retained evidence and reproduction

The [original protocol](codec-policy.md), [isolated protocol](codec-policy-isolated.md),
[pinned corpus recipe](../experiments/codecpolicy/README.md) and executable
collector are tracked. Both archives contain all raw schedules/observations,
untimed preparations, source/configuration checks, identities and metadata.
They retain source snapshots at the measured commits, corpus build information,
worker inputs and logs; large payload/artifact binaries are excluded. C2 also
contains the telemetry source audit, independent Pack/launch audits and separate
bootstrap implementation. No observed record or frozen protocol is rewritten by
this report, the later RSS-scope annotation or the owned-process cleanup fix in
the current collector.

| Archive | SHA-256 |
| --- | --- |
| [C1 failed original](benchmarks/codec-42/local-c1-df579d1.tar.gz) | `5e6ea2371d68451e5d888b71aba10d23e05bc0dfb7f3529b081bc83bd4d36393` |
| [C2 isolated diagnostic](benchmarks/codec-42/local-c2-adaea09.tar.gz) | `6ee129639047ce443b064d98f787c2f11c4a7019ab6e6fcb525ac1360e99f697` |

From the repository root, verify sidecars before extracting into a new directory:

```bash
sha256sum -c docs/benchmarks/codec-42/local-c1-df579d1.tar.gz.sha256
sha256sum -c docs/benchmarks/codec-42/local-c2-adaea09.tar.gz.sha256
```

The prospective protocols give clean-checkout reproduction commands. Reproduction
is a new disclosed cohort, not a replacement sample or authority to pool with
these measurements. Run the full pinned source gate separately with
`NO_COLOR=1 MICROFAT_SBOM_TESTS=required task all`; source CI cannot repair failed
experimental acceptance or resolve the memory attribution limit.
