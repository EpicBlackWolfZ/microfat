# Native parallel Pack qualification results

The [frozen qualification protocol](parallel-pack.md) compares the production
Pack default with one outer variant worker on native Linux amd64 and arm64.
The campaign covered four pinned Go application families, two VMs per family
and architecture, two stripping profiles, three Pack profiles, one- and
four-core CPU quotas and memory caps of 128, 256 and 512 MiB or unlimited.
All 24 paired blocks per cell remain in their original VM cohort.

## Campaigns

The [initial dispatch, attempt 1](https://github.com/EpicBlackWolfZ/microfat/actions/runs/43744390450/attempts/1)
at `7f118cf646a78a2ca07c86c4a61e6bba3f5ff29c` stopped in preflight on all 16
VMs. The harness failed to decode the pinned corpus manifest's lowercase
variant fields and reported missing corpus variants. It recorded zero worker
attempts and all 18,432 planned pairs were missing. This dispatch contributes
no timing or resource observations.

The [measurement dispatch, attempt 1](https://github.com/EpicBlackWolfZ/microfat/actions/runs/43745098495/attempts/1)
used Go 1.27.4 and source `831c22d865606b89a5a1b59c8d9e15d7a44e3583`. The
[manifest fix](https://github.com/EpicBlackWolfZ/microfat/pull/458) corrected
harness decoding while preserving the protocol, frozen schedule, and
production Pack code. All 16 archives matched the GitHub-reported SHA-256.
Replay checked the preserved schedule, arm inputs, control readbacks, and
attempt identities, then regenerated each archived summary from its raw
observations.

## Results

All 36,864 attempts in the measured campaign were recorded. Of 18,432 pairs,
15,034 produced successful, byte-identical complete artifacts and 3,398 failed.
No successful pair differed in complete size or SHA-256. All failed pairs
recorded cgroup OOM events or kills in at least one arm. Both arms recorded an
OOM in 2,926 pairs, only the default in 260 pairs, and only the serial arm in
212 pairs. No worker attempts timed out or reported a control error.

The frozen rules classified 337 cells as passing, 205 as inconclusive and 226
as regressions. All 16 shards recorded a regression. Of the passing cells, 201
consistently fell back to one worker and 136 consistently parallelized. In 122
of those parallel cells the upper bound was below 0.90, establishing a packing
time reduction of more than 10% under the protocol assumptions. No cell was
classified as a regression by the timing rule alone. In 203 inconclusive
cells the upper bound did not exclude a timing regression greater than 5%.
The other two inconclusive cells had failed pairs and no usable timing bounds.

At least one `memory.peak` readback exceeded its cap in 2,617 pairs: 832 in
the serial arm alone, 871 in the default arm alone, and 914 in both. The largest
readback above a finite cap was 12 KiB. The frozen protocol treats each of these
observations as a regression and excludes that pair from the timing bounds.
Across both arms, 3,531 individual attempts read back 4–12 KiB above the cap.

| Memory cap | Planned pairs | Failed pairs | Pairs with a peak above the cap |
| --- | ---: | ---: | ---: |
| 128 MiB | 4,608 | 2,571 | 1,592 |
| 256 MiB | 4,608 | 811 | 859 |
| 512 MiB | 4,608 | 16 | 166 |
| Unlimited | 4,608 | 0 | 0 |

At 512 MiB, all failures were on arm64. The peak readbacks exceeded that cap
in 79 amd64 pairs and 87 arm64 pairs. The 4,608 unlimited pairs ran without
recorded OOM events. At 128 MiB, both arms recorded OOM in every `size` pair
across all 64 cells and 1,536 pairs.

There were 139 default-only OOM pairs at 128 MiB, 110 at 256 MiB and 11 at
512 MiB. The only OOMs at 512 MiB occurred in unstripped arm64 Task at four
cores with the `latency` profile on both VMs. Those 512 MiB cells recorded
3 and 2 serial OOMs and 6 and 8 default OOMs.

The CPU quota and memory reservation calculation permit at most one outer
worker in each of the 66 cells with a default-only OOM. To check the memory
bound, sum the two largest worker reservations from the archived ELF sizes
and each codec's workspace. The worker budget is at most 85% of the cgroup
cap less 64 MiB. For the 512 MiB Task cells, two workers require 584.695 MiB
against a 371.2 MiB worker budget. Every successful default in those 66 cells
reported one worker. The killed workers left no worker count.
All successful attempts under finite memory caps that reported at least two
outer workers stayed within their caps. The default-only OOMs still violate
the frozen acceptance rule. See
[production admission](https://github.com/EpicBlackWolfZ/microfat/blob/831c22d865606b89a5a1b59c8d9e15d7a44e3583/internal/pack/parallel.go).

The maintainer accepted the documented limits for v0.4.0. The release retains
production admission with the failures under the tested memory caps recorded
in [#423](https://github.com/EpicBlackWolfZ/microfat/issues/423).

## Results by VM

The complete and failed columns count pairs. The remaining columns count cells,
with pass, regression and inconclusive reported in that order. The last column
counts consistently parallel passing cells with a reduction of more than 10%.

| Shard | Complete | Failed | Pass / regression / inconclusive | Fallback / parallel passes | >10% |
| --- | ---: | ---: | ---: | ---: | ---: |
| amd64-actionlint-0 | 1056 | 96 | 33 / 5 / 10 | 22 / 11 | 11 |
| amd64-actionlint-1 | 1041 | 111 | 9 / 5 / 34 | 3 / 6 | 3 |
| amd64-fortio-0 | 1056 | 96 | 20 / 16 / 12 | 11 / 9 | 6 |
| amd64-fortio-1 | 1056 | 96 | 18 / 15 / 15 | 11 / 7 | 6 |
| amd64-govulncheck-0 | 1056 | 96 | 29 / 10 / 9 | 19 / 10 | 10 |
| amd64-govulncheck-1 | 1056 | 96 | 28 / 9 / 11 | 18 / 10 | 10 |
| amd64-task-0 | 847 | 305 | 15 / 28 / 5 | 9 / 6 | 6 |
| amd64-task-1 | 760 | 392 | 14 / 27 / 7 | 8 / 6 | 6 |
| arm64-actionlint-0 | 1056 | 96 | 30 / 4 / 14 | 19 / 11 | 11 |
| arm64-actionlint-1 | 1056 | 96 | 29 / 4 / 15 | 18 / 11 | 11 |
| arm64-fortio-0 | 891 | 261 | 19 / 15 / 14 | 10 / 9 | 5 |
| arm64-fortio-1 | 898 | 254 | 15 / 14 / 19 | 7 / 8 | 5 |
| arm64-govulncheck-0 | 992 | 160 | 24 / 10 / 14 | 14 / 10 | 10 |
| arm64-govulncheck-1 | 1010 | 142 | 27 / 10 / 11 | 17 / 10 | 10 |
| arm64-task-0 | 611 | 541 | 9 / 27 / 12 | 3 / 6 | 6 |
| arm64-task-1 | 592 | 560 | 18 / 27 / 3 | 12 / 6 | 6 |

## Retained records

The [per-VM records](parallel-pack-43745098495/shards.tsv) identify each archive
by its GitHub artifact ID, size and SHA-256. They include OOM and finite-cap
overshoot counts by arm. The [768 cell records](parallel-pack-43745098495/cells.tsv)
retain every frozen cell, its decision, pair counts, admission counts, timing
statistics and recorded reasons. `memory_mib=0` denotes unlimited memory. Empty
ratio fields indicate unavailable statistics. A dash in the reasons column
indicates an empty reason list. A default failure against a successful serial
run can also appear in the default-only OOM count. These columns overlap.

In the cell records, a complete pair has successful arms and matching artifact
bytes. A complete pair can still exceed a finite cgroup cap. The fallback and
parallel counts use the available default-worker records, and failed workers
may leave them unavailable. Where all 24 eligible ratios are available, the
timing bounds are the third and twenty-second ordered default/serial Pack
wall-time ratios. The Pack clock covers input preparation through atomic output
finalization. Process startup, cgroup setup and artifact hashing are outside
that clock. Hosted results describe these VMs and their recorded cgroup controls.
