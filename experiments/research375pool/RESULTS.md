# Local encoder-pool POC results — 2026-10-23

**Decision: retain the opt-in POC; do not change defaults.** The four-CPU,
512 MiB cohort clears the predeclared 10% packing-time margin for both families.
The 256 MiB cohort does not; at 128 MiB the pool is substantially slower than
the current packer. Larger memory use and a conservative admission policy are
material tradeoffs. This is descriptive screening, not independent confirmation.

## Source and protocol

Measured implementation and predeclared protocol:
[`39ab979e28b3adc41386d27260675d6e9cc4dbd8`](https://github.com/EpicBlackWolfZ/microfat/commit/39ab979e28b3adc41386d27260675d6e9cc4dbd8).
Main baseline was `142583b9d98b0a654bc82963fd68d0e527de017b`.
Later evidence/documentation commits do not relabel these measurements.
Research executable SHA-256:
`ed90c9817e625d66bc391820c736fa7b8ba476823439f8915401019566a48355`.
Go 1.27.3, klauspost/compress v1.20.3, native AMD Ryzen 9 3950X (16 cores,
32 threads), Linux `7.2.11-200.nobara.fc44.x86_64`, desktop environment.
No CPU affinity, fixed frequency, cold-page-cache or exclusive-host claim.
Tests/builds were paused throughout the packing campaign.

The [protocol](README.md) specifies five fixed blocks, rotating cohort/mode
order, Zstd/default, three ISA variants per family, no dictionary, format v2,
full stub. Each trial used a fresh systemd cgroup with read-back CPU 1/4,
memory 128/256/512 MiB, swap zero and GOMEMLIMIT at 90%. Pool admission was
75% of the hard cap with a declared 64 MiB workspace per encoder.

There were **150 attempts, 150 verified artifacts, zero failed/OOM trials and
zero logical-bound violations**. All attempts remain in the evidence. These
two families were also used in the earlier screening; they are not held-out
program families. Five samples cannot establish p99, population precision or a
portable production policy.

## Packing speed

Four-CPU results below show successful-sample medians in milliseconds. The last
column is the median of **within-block current/pool ratios**; it is deliberately
not a ratio of the marginal medians. Each comparison has five complete pairs.

| Family | Hard cap | Current Pack | Pool path, 1 worker | Bounded pool | Paired speed ratio |
| --- | ---: | ---: | ---: | ---: | ---: |
| demo | 128 MiB | 120.79 | 143.62 | 145.33 | 0.827× |
| demo | 256 MiB | 95.81 | 118.34 | 89.20 | 1.084× |
| demo | 512 MiB | 97.06 | 120.01 | 62.24 | **1.571×** |
| microfat CLI | 128 MiB | 257.16 | 323.06 | 315.19 | 0.816× |
| microfat CLI | 256 MiB | 260.59 | 323.68 | 252.37 | 1.023× |
| microfat CLI | 512 MiB | 215.44 | 272.20 | 137.35 | **1.589×** |

At 512 MiB, paired reductions are **36.33% / 37.06%** for demo/CLI; all five
blocks individually clear 10%. The pool admits three jobs there, two at 256 MiB,
and only one at 128 MiB. At 256 MiB the paired reductions are 7.74% / 2.20%,
below the threshold. At 128 MiB it is 20.97% / 22.57% slower than current.

At CPU 1 the pool admits one job and is about **0.76–3.17% slower** across the
six family/cap cohorts. Against the serial1 control at CPU 4/512 MiB, outer
pooling gives paired ratios 1.965× / 1.982×. This separates outer parallelism
from the inner-thread setting: serial1 itself is slower than current Pack.
These are complete Pack-call times, not isolated compressor rates or process
startup latency. Parent verification and systemd orchestration are excluded.

## Memory and CPU cost

Successful medians at CPU 4/512 MiB:

| Family / mode | Pack CPU ms | Worker max RSS MiB | Cgroup peak MiB |
| --- | ---: | ---: | ---: |
| demo / current | 121.88 | 51.42 | 79.73 |
| demo / pool | 122.15 | 55.44 | 92.43 |
| CLI / current | 285.71 | 77.75 | 136.22 |
| CLI / pool | 289.56 | 111.67 | 183.00 |

The gain spends parallel resources rather than removing much CPU work. The CLI
pool's largest observed cgroup peak in this cohort was **193.48 MiB**. RSS and
cgroup peak are different quantities: the latter also charges file cache,
startup/runtime and the stop-post keeper. Fresh snapshots and spool files are
part of this real packing path. Full per-cap medians, extrema, throttling,
logical peaks and raw counters are retained in JSON.

Logical admission stayed within its budget, with zero active/admitted counters
on return. This does **not** bound preparation, retained heap, total RSS or kernel
charges. In particular, even the 1 MiB admission-rejection probe reached about
94.81 MiB cgroup peak during the complete worker lifetime before returning with
zero admitted jobs. Treat the 64 MiB reserve as a chosen parameter, not a proven
universal workspace estimate.

## Hard-limit and publication negative controls

These five additional attempts are separate from the 150 timed trials:

| Probe, CLI family | Observed outcome | Prior output preserved |
| --- | --- | --- |
| Current, 32 MiB | Kernel OOM, exit 137 / signal 9, raw oom_kill=1 | Yes |
| Pool, 32 MiB | Logical admission rejected, exit 1, zero jobs started | Yes |
| Current, 64 MiB | **Succeeded**, verified replacement artifact | No, replaced successfully |
| Pool, 64 MiB | Logical admission rejected, exit 1, zero jobs started | Yes |
| Pool, 512 MiB hard cap / 1 MiB admission | Shared-byte admission rejected, zero jobs started | Yes |

The current packer succeeding at 64 MiB while the pool refuses work exposes the
reservation's conservative cost. It must not be described as a pool memory win.
The OOM's precise internal packing phase was not instrumented. Parent cleanup
removed only the stopped worker's private temporary files; no owned transient
units or abandoned snapshot/spool/output temp paths remained.

Two pool negative controls recorded a raw memory.peak one 4096-byte page above
memory.max. Those values are preserved, not clipped. The
[Linux cgroup v2 documentation](https://docs.kernel.org/admin-guide/cgroup-v2.html#memory-interface-files)
explicitly permits temporary usage above memory.max; a hard-limit setting is not
a promise that every recorded charge is numerically below it.

The separately retained `resource-negative-control.log` is a synthetic mmap
page-touch test of the controller: phase marker observed, exit 137, signal 9,
oom_kill=1, memory.max and peak both 64 MiB. It is distinct from the real packer
OOM and from logical admission errors. Ordinary exit-1 and CPU quota readbacks
were also tested.

## Artifact size and correctness

Timed artifacts were always **9,668,121 bytes** (demo) or **18,493,897 bytes**
(CLI), equal across all modes, blocks and caps. All comparable compressed frame
hashes and dictionary identities matched the current packer. Complete file
hashes can differ because the index contains CreatedUnix; equality of complete
hashes is not a requirement across packing times.

The separate native matrix passed **64/64 complete artifacts**: two families ×
format v1/v2 × full/minimal stub × none/LZ4/Zstd/Zstd-dictionary × current/pool.
All 32 current/pool pairs have identical complete sizes, compressed frame hashes
and dictionary hashes. Verification checks exact requested stub bytes and
boundaries, codec/dictionary/metadata, canonical variant order and decoded input
hashes/sizes. Unit matrices also compare exact decoded byte sequences.

The matrix records **256 successful untimed executions**: 64 direct native
references and 192 fat launches, one each in explicit memfd, explicit cache and
prewarmed auto. Every output matched; actual modes and selected payload
identities matched. This qualifies these native correctness cases, not ARM64,
foreign kernels, launch p99 or application service latency.

An independent subagent audit checked the retained files outside the Go packer:
all **214 artifacts / 642 frames** passed manual format/trailer, stub, dictionary
and hash checks, and independent raw/libzstd/liblz4 decoding matched every source
byte. All 32 matrix and 60 timed current/pool pairs also had identical normalized
indexes. Exact whole files matched in 23/32 and 41/60 pairs respectively; all
remaining differences were solely CreatedUnix bytes and the consequent index
digest. Native execution selected **v3 on this host**; all three tiers were
independently decoded. The matrix's dictionary ablation increased complete v2
sizes by 30,303 bytes (demo) / 72,465 bytes (CLI) in both packers; there is no
dictionary or pool size win here.

## Validation and next investment

Combined experiment tests with required live cgroup controls pass under race
detection with **96.5%** experiment statement coverage. New pool implementation
statements have 100% targeted coverage. The repository production coverage gate,
full Task pipeline and existing compatibility matrix are tracked in the PR.

Next: independently repeat the CPU4/high-memory candidate on held-out programs
and native ARM64, measure level/size/workspace sensitivity and bound whole-process
preparation/retention costs before designing a user-facing policy. Keep serial
defaults at low budgets. Encoder reuse across calls is an independent untested
ablation. No decoder, decompression, launch-tail or p99 improvement is established.

## Replay the retained evidence

`evidence/{campaign,matrix,limits}.json.gz` contain complete original reports,
including failed controls, raw counters, requests, source/corpus hashes, actual
dispatch diagnostics and absolute local artifact paths. Paths identify the
original private trial tree; the ELF artifacts themselves are not committed.
`SHA256SUMS` covers compressed files; `RAW_SHA256SUMS` covers decompressed reports.
Readable `*-summary.json` files are produced by the same bounded Go analyzer.

```bash
mkdir -p .work/research375pool/replay
gzip -dc experiments/research375pool/evidence/campaign.json.gz > .work/research375pool/replay/campaign.json
gzip -dc experiments/research375pool/evidence/matrix.json.gz > .work/research375pool/replay/matrix.json
gzip -dc experiments/research375pool/evidence/limits.json.gz > .work/research375pool/replay/limits.json
sha256sum -c experiments/research375pool/evidence/SHA256SUMS
(cd .work/research375pool/replay && sha256sum -c ../../../experiments/research375pool/evidence/RAW_SHA256SUMS)
.work/research375pool/encoder-pool --mode=summary --request .work/research375pool/replay/campaign.json
```
