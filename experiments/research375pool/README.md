# Bounded in-process encoder pool POC (#375)

The successor [training and held-out confirmation protocol](CONFIRMATION.md)
freezes a conservative policy before native amd64/arm64 VM confirmation. Its
fixed-sample gates, source pins, fallback diagnostics and retention bounds are
separate from the original exploratory results below.

See [the measured results](RESULTS.md) for the positive high-memory cohort and
the low-budget regressions, retained controls and evidence replay instructions.

This opt-in experiment compares **complete packing**, including input snapshotting,
ELF/ABI validation, compression, assembly, fsync and atomic publication. The normal
CLI still calls the unchanged sequential payload path in `pack.Pack`.
`pack.PackWithEncoderPool` is an experimental internal entry point, not a default
policy or a user-facing flag. No launcher or format change is required.

The pool admits at most `MaxWorkers` jobs and requires
`MaxWorkers * InnerConcurrency <= CPUBudget`. Each active job reserves its raw
snapshot size plus `WorkspaceBytesPerEncoder * InnerConcurrency`; shared stub and
dictionary bytes are charged too. All settings are explicit, positive and bounded.
The reservation is **logical admission accounting**, not an allocation, RSS or
whole-process memory guarantee. ELF/ABI preparation and dictionary training run
before this admission stage. Go allocator retention, runtime overhead and file
cache are excluded. A reservation chosen too low can still cause a hard-cap OOM.

Workers create fresh encoders, write compressed frames to private disk spools,
then assemble them in canonical variant order. All workers are joined before
cleanup or return. Cancellation is cooperative at payload I/O and publication
boundaries; preparation or a codec call can finish before it is observed. Failed,
rejected or canceled calls preserve an existing output. Disk capacity and I/O
remain tradeoffs. This does not implement encoder reuse across calls.

## Reproduce

Use Go **1.27.3**, the repository's pinned Task and a Linux user systemd manager
with delegated cgroup v2 CPU/memory controllers. The corpus is trusted native
AMD64 code built from this repository. Keep the source clean at the recorded
producer revision; the harness records its own executable SHA-256 separately.
The build target requires a clean tree and embeds that revision; a different
`--source-revision` is rejected. Use new output paths for subsequent runs.

```bash
task research-375-pool-prepare

task research-375-pool \
  MANIFEST=.work/research375pool/fixtures/manifest.json \
  STUB=.work/research375pool/fixtures/stub-full \
  OUTPUT=.work/research375pool/campaign

task research-375-pool POC_MODE=matrix POC_EXECUTE=true \
  MANIFEST=.work/research375pool/fixtures/manifest.json \
  STUB=.work/research375pool/fixtures/stub-full \
  MINIMAL_STUB=.work/research375pool/fixtures/stub-minimal \
  OUTPUT=.work/research375pool/matrix

task research-375-pool POC_MODE=limits \
  MANIFEST=.work/research375pool/fixtures/manifest.json \
  STUB=.work/research375pool/fixtures/stub-full \
  OUTPUT=.work/research375pool/limits

.work/research375pool/encoder-pool --mode=summary \
  --request .work/research375pool/campaign/report.json
```

Preparation refuses existing corpus paths. Campaigns refuse existing output
directories. Corpus metadata pins every input's size/hash and toolchain. Inputs
must be finite regular files, with per-file/count/aggregate bounds. The parent
hashes inputs before trials, so this is warm or unspecified page-cache evidence,
not cold-storage performance. Do not run tests or builds during timed campaigns.

## Predeclared comparison

Two families (the demo and CLI), three variants each (`v1/v2/v3`), format v2,
full stub, Zstd/default, no dictionary. Five fixed blocks cover CPU quotas 1/4
and hard memory caps 128/256/512 MiB. Cohort and mode order rotate between blocks.
No discarded warmup trials or success-only reruns. All attempts remain in JSON.

| Mode | Outer jobs | Zstd inner concurrency |
| --- | --- | --- |
| current | Sequential, existing `Pack` | Existing upstream default |
| serial1 (CPU 4 only) | 1 through pool/spool path | 1 |
| pool | At most min(3, CPU budget), further limited by admission | 1 |

The pool's admission budget is 75% of the hard cap, with a declared 64 MiB
workspace reserve per encoder. `GOMAXPROCS` matches CPU quota; `GOMEMLIMIT` is
90% of the hard cap; swap is disabled for each fresh trial. These constants are
experimental parameters, not a proposed autotuning policy.

The prior screening suggested a possible gain from outer concurrency. A useful
follow-up here is at least a **10% descriptive median packing-time reduction**
without failed samples, broken correctness or artifact growth in that cohort.
Paired blocks and the serial1 control help attribute any gain. Five samples on
one desktop cannot qualify population tails, p99, other machines or production.

Each worker has its own transient unit. Actual `memory.max`, `memory.swap.max`
and CPU quota/period are read back, along with raw memory/CPU counters and
`memory.peak`. An `ExecStopPost` sleep keeps the same cgroup alive long enough to
read counters after worker exit; the controller then stops only that owned unit.
The cgroup peak includes runtime and file-cache charges and this tiny keeper.
Worker `ru_maxrss` is a separate whole-worker high-water mark. `pack_ns` and
`pack_cpu_ns` surround the Pack call only; orchestration wall and cgroup CPU also
include startup/control overhead. GOMEMLIMIT is a soft Go-runtime target.

The limits mode retains both current/pool attempts at 32/64 MiB and a 1 MiB
logical-admission rejection under a 512 MiB hard cap. An existing output sentinel
records publication preservation. It reports observed outcomes rather than
assuming every low-cap attempt must OOM. Parent cleanup removes only its own
trial's snapshot/spool/output temporary paths after the unit has stopped. Cleanup
failures abort the campaign; possibly live worker paths are preserved.

## Correctness and evidence boundaries

The untimed native matrix covers format v1/v2, full/minimal stubs, none/LZ4/Zstd/
Zstd-dictionary, both input families, and current/pool packing. Every artifact is
fully verified, with exact input payload hashes/sizes, canonical order, requested
codec/dictionary, stub prefix/boundary and metadata. Complete artifact size and
compressed frame hashes are recorded. Timestamp differences can change complete
hashes; bitstream equality is an observation, not a correctness requirement.

`POC_EXECUTE=true` additionally executes the known trusted fixtures outside the
timing cgroup: native payload reference plus explicit memfd, explicit cache and
auto after private prewarm. Output, actual dispatch mode and selected payload
identity must match. These are native correctness checks, **not** startup latency,
p99, ARM64 or foreign-architecture qualification. Existing hosted runtime and
compatibility suites remain separate evidence layers.

Unit tests also cover corrupted/truncated/changed input, FIFO rejection, mixed
codecs, dictionaries, cancellation, spool failures, admission bounds, CPU bounds,
joining, cleanup and preserving existing output. The resource harness has an
opt-in required live test (`MICROFAT_POOL_RESOURCE_TESTS=required`) with ordinary
failure and an intentional mmap page-touch OOM negative control. A synthetic OOM
is evidence of the hard controller, not a packing workload result.

Validation commands (outside the timing slot):

```bash
MICROFAT_POOL_RESOURCE_TESTS=required go test -race -coverprofile=.work/pool-coverage.out ./experiments/research375pool
NO_COLOR=1 MICROFAT_SBOM_TESTS=required task all
NO_COLOR=1 task benchmark-matrix
```

The standard coverage gate counts production packages; experiment statement
coverage is checked separately. The summary command retains failed denominators,
reports successful-sample medians explicitly, and computes speed ratios within
complete block pairs before taking their median. It also reports missing/failed
pairs, actual memory peaks, logical-bound violations and artifact equality.
