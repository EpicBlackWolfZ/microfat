# Native screening result, 2026-10-23

The POC ran successfully: 630 hash-verified observations across 258 batches, with
zero failed attempts. Two program families (demo and microfat
CLI), each with stripped Go 1.27.3 amd64 v1/v2/v3 ELFs, used the same native host,
GOMAXPROCS=4, default compression level, no dictionary, and three blocks per setting.
The inherited cgroup-v2 had **no finite CPU quota or memory ceiling**. This is not
the constrained-resource campaign or independent confirmation required by #375.

The exact runnable-code commit was `c5b18feb` (see the full revision in the JSON).
Corpus source was `ada376b4ffffaa3354becbfb70c1896934caf277`. The report hashes the
actual harness executable and each input ELF. [The summary](evidence/summary.json)
contains commands, per-input compressed hashes, settings and uncompressed/gzip report
hashes. [Demo raw report](evidence/demo.json.gz) and
[CLI raw report](evidence/microfat.json.gz) retain every sample and changing endpoint
PSI/cgroup snapshot. `gzip -dc` exposes the original JSON; gzip metadata has no
filename or timestamp. No environment dump or secret material is captured.

The branch was subsequently rebased onto `94cb9f6` after an unrelated release,
installer and documentation merge. Measurements were not repeated or relabeled.
[Original measured source patch](evidence/measured-source.patch.gz) reconstructs the
experiment on parent `ada376b4`; [source provenance](evidence/source-provenance.json)
pins both compressed and uncompressed patch SHA256 and confirms byte-for-byte
equivalence of all five experimental Go source/test files after the rebase.
For example, use a separate checkout at the recorded measured parent and run
`gzip -dc measured-source.patch.gz | git am`. This reconstructs the measured source
tree; committer metadata can result in a different commit ID. The original harness
executable hash remains in each raw report.

The encoder allocation screen suggests that independent variant work deserves a
single-process worker-pool follow-up. Three-variant bundle median batch wall times:

| Inner encoder threads | Outer workers requested | Demo | CLI |
| --- | ---: | ---: | ---: |
| Upstream default (effective 4) | 1 | 111.6 ms | 266.2 ms |
| 1 | 1 | 129.7 ms | 312.9 ms |
| 1 | 2 | 86.8 ms | 211.7 ms |
| 1 | 4 (three jobs available) | 58.2 ms | 124.1 ms |
| 2 | 2 | 74.2 ms | 176.4 ms |

These intervals include fresh worker process startup and independent roundtrip
verification. They are not production pack-throughput measurements. All settings
produced identical compressed size **and per-ELF SHA256** in this corpus:
5,697,176 bytes for the demo bundle and 14,561,560 for the CLI bundle, before format
metadata/stub. Identical output here is an observation, not a general codec promise.

Lower decoder concurrency does not consistently dominate. The next table uses only
the v1 input from each family, then takes the median of **three burst maxima** of
parent-observed post-release completion latency. This is a temporary-file pipeline
including streaming decode, hash, materialization and exact native child readiness.
It excludes worker prestart/gate time. It is neither p99 nor full microfat startup.

| Decoder threads | Burst | Demo | CLI |
| --- | ---: | ---: | ---: |
| Upstream default (effective 4) | 1 | 12.39 ms | 27.32 ms |
| 1 | 1 | 13.23 ms | 29.95 ms |
| Upstream default (effective 4) | 4 | 18.25 ms | 38.13 ms |
| 1 | 4 | 16.77 ms | 37.83 ms |

The apparent headroom benefit in the demo's four-launch bursts does not reproduce
at a similarly useful size in the larger CLI. Concurrency 1 costs isolated latency
in both examples. Whole-worker RSS highwaters reached about 60 MiB (demo) and 81 MiB
(CLI), including codec setup, verification and other worker phases; these are not
aggregate simultaneous peaks or evidence of a hard memory bound.

**Decision:** retain production defaults. Prioritize a bounded, single-process
encoder worker-pool POC with memory admission and failure/atomic-publication checks.
Before selecting any decoder policy, run held-out confirmations under actual
fractional/1/2/4 CPU quotas and explicit memory ceilings, background pressure,
dispersed arrivals, and independent burst blocks. Test production memfd, cold cache
materialization and verified warm cache separately. Warm-cache codec bypass remains
a negative control. #376's complete-launch costs and #377's uncertainty controls
must precede a practical p99 claim.

Focused race tests passed with **96.1% own-package statement coverage**, and the
strict linter reported zero issues at the runnable-code commit. The repository's
core coverage universe is unchanged; full repository qualification is tracked by
the parent research campaign separately.
