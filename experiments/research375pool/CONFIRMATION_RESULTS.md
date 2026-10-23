# Frozen encoder-pool confirmation — 2026-10-23

**Decision: the broad production gate is not met; retain the pool as a
research-only POC, with no production option this wave.** All
12 pool-eligible application/architecture/VM cells clear the fixed useful-speed
gate. Task uses the unchanged current packer, but its fallback nonregression
bound is inconclusive on two VMs. Those failures of precision remain failures
of the predeclared broad gate. There was no policy tuning, rerun, sample-size
increase or gate relaxation after inspecting held-out results.

## Source, training and native cohorts

Measured source is
[`8c0ac81118363f8bda802483bd2b6ed2d01e22cc`](https://github.com/EpicBlackWolfZ/microfat/commit/8c0ac81118363f8bda802483bd2b6ed2d01e22cc),
Go 1.27.3 and klauspost/compress v1.20.3. Later evidence/documentation commits
retain the same Go harness and policy. The [protocol](CONFIRMATION.md) was
committed before collection. Its SHA-256 is
`4067972d7dd87d47d9ae895e0d4c6c71d6bf18fd46deb83937da0fb4000d3d4d`.

[Native run39927016072](https://github.com/EpicBlackWolfZ/microfat/actions/runs/39927016072)
completed all four jobs: two native amd64 runners and two native arm64 runners.
Their GitHub runner IDs are distinct and retained in evidence. Both amd64 hosts
report AMD EPYC 7763; both arm64 hosts report Neoverse-N2, on Linux
6.17.0-1022-azure. The hostnames repeat within architecture, so a hostname is not
evidence of separate physical hardware or independent samples. The runner jobs
are separate cohorts; VM blocks are never pooled. Their timing coverage remains
conditional on independent stationary ratios within each cohort.

Each VM trained only on demo/microfat, selected the largest passing declared
workspace reservation (**96 MiB per encoder**), then froze it before measuring
held-out actionlint 1.7.12, Fortio 1.76.0, govulncheck 1.9.0 and Task 3.54.0.
At CPU4/512 MiB, actionlint/Fortio/govulncheck qualify by source size for three
workers with inner concurrency one. Task fails the conservative admission-size
rule and stays on current `Pack`. CPU1 and low-memory controls also use current
`Pack`. Input hashes, source commits, module checksums, build flags, native ISA
tiers and both build profiles are retained. Confirmation timing uses stripped
inputs only; unstripped packing timing was not collected.

Initial [run39926492099](https://github.com/EpicBlackWolfZ/microfat/actions/runs/39926492099)
failed on all four VMs at systemd delegation setup, before corpus building or
any timed Pack attempt. Its original c303d92 logs and bundles are retained
separately. The minimal adapter/setup-documentation repair replaced unsupported
`set-property Delegate` with a runtime drop-in before user-manager startup.
No Go code, policy, sample size or statistical gate changed for the next run.

## Paired complete-Pack results

Each entry is **median current/policy ratio / one-sided lower bound** from 30
complete paired blocks. A larger ratio means less policy Pack time. The lower
bound is the eighth ordered exact ratio, with family error budget 1/20 across
all 16 predeclared cells. Eligible cells require a lower bound strictly above
10/9; current/current fallback cells require strictly above 20/21.

| Native cohort | actionlint, pool | Fortio, pool | govulncheck, pool | Task, current fallback | VM gate |
| --- | ---: | ---: | ---: | ---: | --- |
| amd64 VM0 | 1.3887 / 1.3291 | 1.4253 / 1.3756 | 1.3942 / 1.3565 | 1.0099 / 0.9460 | Inconclusive fallback |
| amd64 VM1 | 1.3686 / 1.3413 | 1.4311 / 1.3891 | 1.3914 / 1.3485 | 0.9879 / 0.952567 | Pass |
| arm64 VM0 | 1.5400 / 1.5142 | 1.3869 / 1.3555 | 1.5607 / 1.5465 | 1.0143 / 0.9685 | Pass |
| arm64 VM1 | 1.6114 / 1.5580 | 1.3608 / 1.3285 | 1.6017 / 1.5729 | 0.9909 / 0.9199 | Inconclusive fallback |

The 12 eligible medians correspond to **26.5–37.9% less Pack wall time**, with
individual lower bounds corresponding to **24.7–36.4% reductions**, conditional
on the protocol assumptions. The two Task misses do not establish a fallback
regression: both arms call the same current path, and their medians are near
one. They do prevent the required all-four-VM broad qualification. AMD64 VM1's
fallback bound only narrowly exceeds 20/21; the exact recorded value, rather
than a rounded table entry, determines its gate. These observations do not
identify a cause for the bound misses or establish that pooling caused harm.

Pack time includes snapshots, validation, compression, assembly, fsync and
atomic publication. Policy selection in the controller, process startup,
verification and systemd orchestration are outside that timer. These results
do not measure launcher startup, service readiness, p99 or selector overhead.

The speed spends resources: median paired policy/current Pack CPU ratios are
1.117–1.129 on amd64 and 0.992–1.026 on arm64 for eligible programs. This is
not a CPU-efficiency improvement claim. The largest eligible pooled primary
cgroup peak is **332.066 MiB**, versus a configured 512 MiB limit. Task's
current path reaches **491.367 MiB**. These are observed maxima, not future
memory guarantees; raw worker RSS and cgroup/file-cache charges stay separate.

## Complete attempts, failures and compatibility

| Phase | Attempts | Successful artifacts | Retained failures |
| --- | ---: | ---: | ---: |
| Training | 432 | 408 | 24 admission rejections at 128 MiB / 96 MiB workspace |
| Confirmation | 1,248 | 1,236 | 12 Task 128 MiB OOM attempts, both arms on both ARM VMs |
| Full compatibility matrix | 128 | 128 | 0 |
| Total | 1,808 | 1,772 | 36 |

All **960 primary attempts / 480 pairs** completed with source-byte hashes,
compressed-frame identity and normalized-index compatibility verified by the
harness. There are zero missing primary attempts, primary OOMs, reported native
gaps or fallback functional/resource regressions. Task at 128 MiB on arm64 has
three paired unsupported-baseline controls per VM: both arms OOM. Those six
pairs remain unsupported, with no speed/equivalence claim. All failed attempts
preserved the previous output. The training admission rejections also remain
in the report; they do not disappear from calibration denominators.

Each VM's actionlint matrix covers both formats, full/minimal stubs,
none/LZ4/Zstd/shared-dictionary and current/pool: 32 artifacts and 128 untimed
native checks. Another 32 checks per VM cover every retained first primary
artifact in both arms for all four families. Across the four VMs, **640 checks**
cover direct native, explicit memfd/cache and prewarmed auto, with exact stdout
and actual dispatcher mode/variant/hash/size matching. Govulncheck uses the
fixed local version-only DB fixture; no vulnerability scan or public DB refresh
participates in that correctness comparison.

There are **32 low-cap observations with memory.peak above configured
memory.max**: 16 by 4 KiB, 13 by 8 KiB, and one each by 20/36/68 KiB. Three are
training observations; 29 are confirmation diagnostics. None is primary.
Raw peaks and actual readbacks are preserved. The configured enforcement
limit is not a strict observed-peak ceiling; admission reservations are also
not an RSS cap. The protocol and gates were not changed to explain these data.

## Retained evidence and replay

[Evidence directory](evidence/confirmation39927016072/) contains four compressed
raw VM bundles, raw GitHub job logs, exact run/job/artifact metadata, per-file
hashes, source and producer pins, policies, corpus manifests, a compact
packing analysis and the independent audit result. Source ZIP hashes were
checked against GitHub's published digests before extraction. Every archived
file was hash-compared with its extracted original. No ELF payloads are
committed; the pinned build recipe reconstructs them. This preserves metric
provenance, not authentication of a supplied report or an independent re-decode
of hosted payloads after pruning.

The source-pinned executable independently replays all four training freezes
and confirmation summaries to exactly the recorded JSON results. For example:

```sh
root="$(git rev-parse --show-toplevel)"
evidence="$root/experiments/research375pool/evidence/confirmation39927016072"
(cd "$evidence" && sha256sum -c SHA256SUMS)
extracted="$(mktemp -d)"
tar -xzf "$evidence/pool-amd64-0-39927016072-1.tar.gz" -C "$extracted"
cohort="$extracted/pool-amd64-0-39927016072-1"
(cd "$cohort" && sha256sum -c SHA256SUMS)
"$root/.work/research375pool/encoder-pool" --mode=confirm-summary \
  --request "$cohort/runs/confirmation/report.json"
```

The independent audit manually reconstructs all planned trial keys and paired
denominators, verifies actual CPU/memory/swap readbacks and worker records,
checks corpus/index/frame identities and frozen size eligibility, recomputes
training-only selection and exact rational median/rank gates, and requires all
native records. It also compares pre-native durable journals with final
reports: post-timing launch records and completed-removal status are the only
allowed differences. Both forms are retained unchanged. The external analysis
script's digest and audit output are recorded; no Python repository helper was
added.

Focused source validation used Go1.27.3 race tests with **95.1% experiment
coverage**, pinned golangci-lint2.15.0 (zero issues), ShellCheck and actionlint.
The full local Task qualification is tracked separately at the PR, with its
exact tested source, complete raw-log digest and final outcome.

## Next investment

Keep the measured policy and its negative broad decision intact. #375 remains
a promising direct packing-speed track, but this wave keeps the opt-in research
harness and makes no production option, extraction or automatic-default change.
A future production proposal remains gated by separately predeclared
confirmation resolving the failed broad decision. Further fallback
nonregression study needs a new cohort and estimand, not extra samples appended
here. Connect
#377 only to separately planned failure/SLO-risk confirmation; connect #376/#380
to actual launch/lifecycle costs. Neither inherits these packing-speed ratios.
