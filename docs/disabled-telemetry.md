# Disabled launcher diagnostics: measurement protocol

**Historical C1 disposition:** retain production's unconditional timing. The completed
[evaluation and retained evidence](disabled-telemetry-results.md) did not qualify
the conditional-clock candidate. The original prospective protocol is preserved
in the evidence archive and at candidate commit
`c1d47eaa9935fd481290617d49f9aa0a4502324d`; the thresholds below are unchanged.

This is the original fixed protocol for #41. The distinct
[single-snapshot candidate](disabled-telemetry-snapshot.md) has its own prospective
protocol and cohort; its results cannot replace the observations below.
Commit this protocol, its fixture and harness
before collecting candidate timing. The existing demo startup matrix always
enables JSON diagnostics and is exploratory evidence for a different question.
The historical descriptor-handoff study retains its own frozen design and results.

## Source, fixture and scope

The recorded comparison uses clean baseline
`6eb862b914e8b3dfdf820dcbd1bfa079e4275dd6` and clean experimental candidate
`c1d47eaa9935fd481290617d49f9aa0a4502324d`. Preserve both identities and all
source/toolchain hashes. The initial retention commit restored baseline production
code; it must not be substituted for the rejected candidate when reproducing the study.
Recheck both exact source heads and clean status after build preparation immediately
before timing and again after collection; abort with retained evidence if changed.
Use native Linux amd64 or arm64 with Go 1.27.3, `CGO_ENABLED=0`, `-trimpath`,
`-buildvcs=false` and stripped binaries with an empty build ID. A run is evidence
only for its recorded architecture and host; it does not establish both native
architectures, hardware isolation or fleet performance.

Build the fixed in-repository `tests/e2e/testdata/telemetry/fixture` once at the
architecture baseline (`GOAMD64=v1` or `GOARM64=v8.0`). Its sole measured invocation
is `--startup-only`, which prints the unique marker
`MICROFAT_TELEMETRY_STARTUP_READY_v1` and exits. Both baseline and candidate
launchers execute these identical payload bytes. The baseline packer creates
Format v2, Zstandard better, dictionary-free bundles with `SOURCE_DATE_EPOCH=0`.
Require equal compressed payload bytes and equal index metadata after normalizing
only payload offsets. Preserve native, packer, stub and complete-bundle identities,
the complete sizes and each profile's stub/bundle size delta.

Clear inherited `GO*`, `MICROFAT_*`, `CGO_ENABLED`, `SOURCE_DATE_EPOCH` and
`XDG_CACHE_HOME` controls. Explicitly set `GOENV=off`, `GOTOOLCHAIN=local`,
`GOWORK=off`, `GOOS=linux`, the native `GOARCH` and `CGO_ENABLED=0`. Verify external
`go version`, `go env` and `uname -m`. Runtime defaults and normal launcher cgroup
autotuning apply equally to both arms; record the host/cgroup/pressure state.
Hash the resolved PATH `go`, `GOROOT/bin/go`, compiler, linker and executing
harness. Preserve the current unified cgroup and every ancestor's CPU quota/stat
and memory max/high/current/events files within `/sys/fs/cgroup`, marking missing
or inaccessible observations unknown. Record the launcher's own resolved cgroup
limit interpretation too. These observations establish no imposed memory cap.

## Frozen schedule and independent units

Measure full/minimal profiles across explicit memfd, explicit cache with an empty
cache, and explicit cache with a warm cache. Cross each with default diagnostics
off (both logging variables absent), `MICROFAT_LOG=json`, and
`MICROFAT_DEBUG=1` with `MICROFAT_LOG` absent. These are eighteen launcher cells.
Add one unchanged direct-native/off cell, executing the same native binary twice
per pair as a drift and precision control.

Each of nineteen cells receives exactly **80 pairs**, giving **3,040 measured
fresh process launches**: 2,880 launcher launches and 160 native controls. Pair
baseline/candidate executions adjacently, alternate their order on successive
rounds, and reverse cell visitation order on alternate rounds. Save the complete
schedule before any timing. Every round visits every cell. There are no pilot
measurements, timed warmups, adaptive stopping, precision extensions, trimming,
outlier removal, replacement attempts or data-dependent reruns.

Every attempt receives a new private cache directory. Cold cache begins empty;
warm cache is seeded outside the timer with the exact selected native payload
under its SHA-256 filename, mode 0700. Explicit memfd and native controls must
leave their directories empty. Verify exact fixture stdout, mode-appropriate
stderr and final cache bytes/mode outside the timer. JSON must contain exactly one
dispatch event with the expected variant/hash/mode; debug must retain its dispatch
fields; off must write no stderr. The format/codec/security compatibility matrix
is separately tested and does not become an extra timing cohort.

The sampling unit is a paired fresh-process launch within a round. Resampling
treats paired rounds for each cell as approximately independent and exchangeable.
Shared-host interference or temporal dependence can violate this approximation;
retain all host observations and this limitation. Page cache is uncontrolled.
Cold/warm refers to the extracted payload cache, not cold storage.

## Measurements and decisions fixed before collection

Measure the complete `exec.Cmd.Run` lifetime: ELF startup, launcher work,
decompression/materialization as applicable, payload handoff, fixed program exit
and output capture. This is a complete-process duration, not internal launcher
time, readiness latency or request latency. Preserve wall nanoseconds, child
user+system CPU nanoseconds, Linux `wait4` maximum RSS in KiB, stdout, stderr,
failure, cache validation and cleanup outcomes for every planned attempt.

Each launch has a ten-second timeout and an observed RSS ceiling of 512 MiB;
this is not an imposed cgroup memory limit. A failed preparation, launch, timeout,
output/cache validation or cleanup remains a failed attempt in the denominator.
Continue the remaining schedule without replacement. Global source/toolchain/
fixture preparation failure aborts as incomplete; retain the schedule and error.
An interrupted or truncated study is incomplete. Always retain raw JSONL as it is
written, including failed attempts. A separate rerun is a new, disclosed cohort;
it cannot erase or be pooled to rescue an earlier failure.

For every cell report the median paired candidate/baseline wall and CPU ratios,
the median paired wall saving (baseline minus candidate), and paired RSS increase.
Use percentile 95% intervals from 2,000 paired bootstrap resamples, seed 41.
The practical wall equivalence interval is **[0.95, 1.05]**: classify equivalent
only when the entire wall interval lies inside it; classify regression if the
entire interval exceeds 1.05, and improvement outside equivalence if it lies below
0.95. An overlapping or excessively wide interval is **inconclusive**, never a
nonregression pass. Preserve every cell, including the unchanged native control
and enabled-logging controls. Unexpected control improvement is also unresolved
against the equivalence criterion and cannot be discarded as noise.

A useful complete-process improvement in an off cell requires both an upper wall
ratio confidence bound <=0.99 and a lower median saving confidence bound >=5,000
nanoseconds: at least 1% and 5 microseconds with interval support. Do not require
this effect to establish removal of a small amount of disabled diagnostic work.
Focused clock/allocation benchmarks establish that attribution separately; they
do not establish a whole-process speedup. Report cells individually without
selecting favorable subsets, pooling architectures or estimating population p99.
These are cell-wise intervals, not simultaneous family-wise confidence bounds.

Require each cell's CPU ratio upper bound <=1.10 and its RSS-increase upper bound
<=10% of baseline median RSS +2 MiB. An unmet resource interval gate fails resource
qualification, including insufficient precision. Statistics using successful pairs
when any attempt fails are marked conditional and cannot override that failure.

The JSON output separates `correctness_pass`, `resource_pass` and
`all_cells_equivalent`. The harness exits nonzero for correctness/resource failure.
A zero exit does not certify wall equivalence: inconclusive or outside-band timing
is printed and retained explicitly. No complete-process performance claim is
permitted unless the relevant cells and unchanged controls satisfy this protocol.

## Commands and retention

After preparing clean checkouts at the fixed baseline and rejected experimental
candidate, run the focused harness tests. Any new collection is a separate cohort:

```bash
go test ./tests/e2e/testdata/telemetry
go run ./tests/e2e/testdata/telemetry \
  -base /absolute/path/to/baseline-checkout \
  -candidate /absolute/path/to/c1d47ea-checkout \
  -output "$PWD/.work/disabled-telemetry-evidence"
```

The output directory must not already exist. Keep it after every outcome. It
contains the frozen protocol/schedule/source identities, external toolchain and
host observations, compiled artifacts, matching payload/index evidence, raw
observations, source rechecks, per-cell decisions and failure text where applicable.
Ordinary exact-head lint, race/leak, coverage, compatibility and native runtime
qualification remain separate evidence layers.

## What the evaluated candidate measured and removed

The rejected candidate skipped the launcher and extraction clock pairs: four clock
reads on cold-cache/memfd paths and two on a warm-cache hit. The diagnostic
predicate still reads its environment controls. Payload environment construction,
metadata parsing, codec and filesystem I/O, integrity hashing, cgroup detection
and execution remain ordinary launcher work. This is a narrow clock-removal
change; it did not make the complete launcher allocation-free.

In that candidate, enabled `total_launcher_us` starts after the initial diagnostics
environment predicate, still after early privilege and executable-path work. It
ends before telemetry formatting/emission and the exec handoff; it excludes Go
runtime bootstrap and payload execution. The memfd extraction duration includes
decompression and the integrity hash. Cache-path extraction duration includes
materialization, publication and acquisition of the validated descriptor. A warm
cache hit retains zero extraction duration while still hashing the selected file
descriptor. Neither internal duration is the complete-process wall duration.

The focused clock/allocation benchmark reconstructs the former unconditional
clock shape around the current semantically equivalent logger. Its allocation and
timing results isolate that shape and the diagnostics predicate; they do not
measure full-launcher allocation, native startup or complete-process improvement.

The original retention implementation kept these candidate helpers in tests only. Ordinary CI
checks the harness/statistics and requires the behavioral telemetry matrix on
native amd64 and arm64. Actual study collection remains opt-in; a failed timing
cohort is not silently retried by routine CI or replaced by a later green code
check. This original study does not qualify a later implementation.
