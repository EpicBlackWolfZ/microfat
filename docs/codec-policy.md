# Compression profiles: prospective complete-cost evaluation

Issue [#42](https://github.com/EpicBlackWolfZ/microfat/issues/42) evaluates the
existing deterministic policy. Production source starts at
`67624ef9644242be74556f28c7ed134292a33aab` (requirements-exact selection, #400). Commit this protocol, corpus
recipe and collection harness before collecting measurements. Record their
complete commit and SHA-256 identities in each cohort. No production codec,
dictionary, integrity or dispatch change is part of this experiment.

## Corpus and fixed alternatives

The primary cohort uses this local shared native Linux amd64 workstation,
without imposed CPU/memory isolation, and Go 1.27.3. Record its CPU, affinity,
ancestor cgroup quotas and readable memory observations before collection.
Later native arm64 collection is a separate disclosed cohort, never pooled.
The source lock under
`experiments/codecpolicy/` pins Fortio, Task, actionlint and govulncheck by module,
version, module/go.mod checksums and source commit. Build each at three tiers
(amd64 v1/v2/v3 or arm64 v8.0/v8.1/v8.2), stripped and unstripped, with CGO off,
trimpath, no VCS stamping and empty build ID. These are four application families,
not eight independent applications or a randomly sampled population.

Additionally build a tiny native static Linux ELF that writes a fixed marker and
exits. Pad it deterministically to exactly 524287, 524288 and 524289 bytes. These
three synthetic groups exercise the current latency boundary: strictly positive
sizes below 512 KiB choose none; the boundary and above choose LZ4. They contain
no Go runtime and cannot establish representative sub-512-KiB Go application
performance. Contract tests also check explicit overrides and actual frames.

Every group has these eight alternatives, with otherwise identical inputs:

| Alternative | Profile | Explicit compression | Forced dictionary |
| --- | --- | --- | --- |
| latency | latency | absent | false |
| balanced | balanced | absent | false |
| size | size | absent | false (production automatic behavior) |
| explicit-none | balanced | none | false |
| explicit-lz4 | balanced | lz4:fastest | false |
| explicit-zstd-fastest | balanced | zstd:fastest | false |
| explicit-zstd-best | balanced | zstd:best | false |
| balanced-dict | balanced | absent | true |

Start from `pack.DefaultOptions()`, keep raw `CompressionLevel` empty in every
alternative, and use creation epoch zero. In particular, resolving an effective
best level must not change the dictionary trainer's raw input. Compare size's
automatic best/dictionary behavior to explicit best/no-dictionary, and balanced
to balanced/forced dictionary. Retain warnings and actual dictionary presence;
automatic/forced intent does not prove training succeeded. The three boundary
variants may share exact bytes; disclose this synthetic repetition.

## Frozen work and scheduling

Prepare inputs, source-built full/minimal stubs, CLI and collector outside timing.
Refuse an existing output directory. Save the entire intended schedule before
the first timed attempt; preparation failures retain the schedule and failure.
No pilots, adaptive stopping, replacement attempts, trimming, outlier removal,
cohort pooling or favorable subsets are permitted.

For each of eleven groups and eight alternatives, measure **three** fresh
full-stub Format v2 Pack workers: **264 Pack attempts**. Rotate alternatives and
reverse order across rounds to reduce fixed-order bias. Execute serially with
`GOMAXPROCS=4`; a fresh worker includes its Go bootstrap, input reading,
compression/training, atomic publication and sync. Parent verification is outside
that clock. Keep all three hashes and require deterministic complete bytes.
Retain the first successful artifact for launches; a missing artifact is a failed
planned launch, never grounds for silently dropping a cell. Build its matched
minimal artifact outside timing and independently verify every variant/hash.

Measure **20** complete fresh-process rounds for every group/alternative across
full/minimal stubs and explicit memfd, cold cache and warm cache: **10560 fat
launch attempts**. Add **1320** unchanged native selected-payload controls, one
for every group/path/profile round, paired with that round's alternatives. Rotate/reverse
visitation and alternative order. Each attempt gets a fresh private directory;
cold starts empty, warm is seeded with verified payload bytes outside timing.
Force the baseline tier so each alternative and its direct control executes
identical payload bytes on hosts with different ISA levels. Use only the pinned
version command (Fortio `version`, Task `--version`, actionlint/govulncheck
`-version`) or the boundary fixture's marker invocation. Check exit status,
complete stdout/stderr and cache contents outside timing against direct controls.
Version-command output is not service readiness or request latency. The pinned
govulncheck version command normally queries a remote database even with
`-version`; pass `-db file://<evidence>/version-db -version` with fixed local
`index/modules.json` (`[]`) and `index/db.json` (epoch-zero modification time).
Record both files and effective arguments. This prevents a network round trip
or changing remote metadata from dominating the codec comparison.

Each Pack worker has a 120-second timeout and an observed maximum-RSS budget of
2 GiB. Each launch has a ten-second timeout and an observed maximum-RSS budget of
512 MiB. Each compiler/download preparation command is bounded to ten minutes,
other collector metadata/version commands to ten seconds, and each untimed minimal Pack to
120 seconds. These are collection/acceptance
budgets, not imposed cgroup memory limits. A timeout, excessive observed RSS,
verification/output/cache/cleanup failure or incomplete schedule fails correctness
or resource acceptance and remains in the denominator. Continue other planned
attempts where possible. Write raw observations promptly; preserve an interrupted
cohort as incomplete, including any orphaned worker diagnostics.

## Measurements and interpretation

Record complete artifact and component sizes: stub, stored dictionary,
compressed frames, metadata index and 56-byte trailer. Check that components sum
to complete bytes. Record complete SHA-256 and variant codecs/digests/byte sizes.
For Pack and launch processes retain parent-clock wall nanoseconds, child
user+system CPU nanoseconds and Linux wait4 maximum RSS in KiB. Wait4 RSS is an
actual process high-water observation; it is neither Go TotalAlloc nor an entire
cgroup peak. Mark cgroup peak unknown unless measured for that exact attempt;
never substitute a configured cap or post-unload counter for actual telemetry.

Cold/warm describes extracted-payload cache state; OS page cache is uncontrolled.
The collector hashes each complete native/fat executable immediately before its
timed launch, outside the clock, to check identity. This scans and warms source
file pages; these measurements cannot establish disk-cold launch or transfer
costs. Cache seeding and verification also affect filesystem caches.
Warm verified-cache execution still hashes the payload but does not decompress.
The process clock includes bootstrap, launcher work, payload handoff and command
exit/output capture. It is distinct from internal launcher/decode clocks and
from #230's median paired differences between per-trial service-p99 estimates.

Pack's three rounds provide descriptive median/range and matched ratios, with
limited precision. For descriptive launch comparisons pair each alternative with balanced
within the same group/profile/path/round. For any profile replacement decision,
also pair candidates with that profile's existing alternative (latency against
latency, size against size, balanced against balanced); beating balanced alone
cannot justify replacing latency or size. Report median ratios, median absolute
wall savings and cell-wise percentile 95% intervals from 2000 paired bootstrap
resamples, seed 42. Resample paired rounds jointly for ratios and differences;
use the midpoint median for even counts and sorted bootstrap order statistics
at floor(0.025*(N-1)) and floor(0.975*(N-1)) for endpoints. Treat
rounds as approximately independent/exchangeable; shared-host drift can violate
that assumption. Repeated variants/profiles/paths do not create independent
application families. These are exploratory cell-wise intervals, without a
family-wise or population-tail guarantee. Report unchanged controls and all
inconclusive intervals. Statistics conditional on successful pairs cannot
override failures.

Practical launch equivalence is an interval wholly within [0.95, 1.05]. An
interval wholly below 0.95 is improvement, wholly above 1.05 regression, otherwise
inconclusive. A prospective profile replacement must demonstrate a useful effect
in every relevant real family/build group, preserve all correctness/resources,
and meet the following conservative engineering criteria:

- Latency: at least 5% and 50 microseconds complete-process saving with interval
  support on memfd and cold cache, warm-cache ratio upper bound <=1.05, without
  increasing complete bytes, Pack median time or peak RSS by more than 25%.
- Size: at least 1% fewer complete bytes in every real group, Pack median time
  ratio <=1.25 and observed maximum RSS increase <=10% plus 16 MiB; launch ratio
  upper bounds <=1.05 on all paths.
- Balanced: at least 5% complete-byte or Pack median-time improvement in every
  real group, no >5% other median-cost regression, launch ratio upper bounds
  <=1.05, and maximum RSS increase <=10% plus 16 MiB.

These intentionally conservative margins justify the maintenance cost of a new
general rule; they are not universal SLOs. The tiny boundary fixture alone cannot
justify changing a threshold. If no alternative qualifies, retain current defaults
and publish the completed bounded evaluation with limitations. An apparent
cross-group winner is a candidate for independent held-out confirmation before
a production rule, not permission to promote an automatically tuned policy.
No dual-encoding dictionary search, predictor or new decoder is required.

## Provenance, reproduction and separate checks

Remove inherited Go/compiler and `MICROFAT_*` controls; explicitly set GOENV off,
GOWORK off, GOTOOLCHAIN local, native GOOS/GOARCH, CGO off and GOMAXPROCS 4.
Every direct/fat launch uses GOMAXPROCS 4 with GOGC and GOMEMLIMIT unset,
and `MICROFAT_AUTOTUNE=0`. Tuning interactions are outside
this codec comparison. Baseline-tier forcing and execution mode are explicit;
diagnostic logging is disabled.
Record source clean state before/after collection, external Go version/env,
compiler/linker/collector/CLI/stub/input hashes, uname/CPU/affinity and readable
cgroup quota/memory/pressure observations. Observed shared-host limits do not
establish isolation or a verified imposed resource cap. Abort on changed source
or input identities, retaining the failure.

```bash
task codec-policy-corpus CODEC_CORPUS="$PWD/.work/codec-policy-corpus" CODEC_ARCH=amd64
task test-codec-policy-harness
task qualify-codec-policy \
  CODEC_CORPUS="$PWD/.work/codec-policy-corpus" \
  CODEC_OUTPUT="$PWD/.work/codec-policy-cohort"
```

Archive raw observations, schedules, identities, host snapshots, verification,
decisions and failures under `docs/benchmarks/codec-42/` with a SHA-256 sidecar.
Large reconstructed binaries may remain local; their exact build recipe, hashes,
sizes and verification results must be retained. Keep the results/disposition in
a separate document without rewriting this prospective protocol.

Run applicable Format v1/v2, codec, dictionary, full/minimal and memfd/cache
compatibility tests plus pinned Task race/coverage (>=95%), lint/security and
exact-head hosted native qualification. These gates establish source correctness,
not performance on another architecture. Leave #230 and native architecture
timing boundaries explicit. A green CI result alone does not complete #42 or
authorize merge/release.
