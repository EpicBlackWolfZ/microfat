# Benchmark measurement contract

## Comparability

Build every target ISA from identical source, Go 1.27.1, modules, tags, and optimization settings. Reuse the exact
native payload bytes inside each fat artifact. Capture hashes, full source revisions, dirty-tree state, build info,
the load tool identity, suite configuration, and environment. Verify the running target's ISA, execution mode,
selected payload digest, and effective runtime settings before sustained measurement.

Matched experiments set explicit runtime values and disable microfat autotuning. ISA specialization, launch mode,
and tuning changes are separate comparison dimensions. Tuning on/off runs use the same startup hook in every
payload; no workload handler branches on whether microfat launched it. Go runtime defaults remain active when
microfat autotuning is disabled. Higher ISA levels do not imply a measured speedup.

Each block contains all scheduled configurations. A seeded PCG permutation and rotating positions counterbalance
their order across blocks. Every trial starts a fresh target and has an explicit cache preparation and warmup.
Cache-cold means the microfat materialization is absent; it does not mean the operating-system page cache is cold.
Warmup results are retained separately from the measurement window. Failed attempts and missing metrics remain
visible; they cannot become zero latency or disappear from the pair set.

## HTTP load and latency

Fortio 1.75.2 runs externally with explicit QPS, concurrency, uniform pacing, disabled catch-up, percentile list,
and histogram resolution. Its raw JSON is preserved. The adapter records its variable-width histogram and
uncorrected service-time observations, not fabricated per-request samples or a claimed HDR correction.
These semantics follow the pinned [Fortio source](https://github.com/fortio/fortio/tree/v1.75.2), especially
`periodic/periodic.go` and `stats/stats.go`.

Request completion rate and requested rate are distinct. Rate shortfall, generator saturation, errors, and
insufficient p99.9 observations qualify interpretation. A service-time histogram may understate latency from
missed scheduled arrivals during overload. Fixed-rate pacing alone does not remove coordinated omission.
Non-200 and transport error counts retain Fortio's original classifications; unsupported finer error categories
must not be inferred from them. Percentiles retain the source histogram's resolution.

## Time and memory scope

Primary startup is helper-spawn-to-server-readiness wall time. Protocol `helper-spawn-to-ready-line-v2`
timestamps the first complete stdout readiness line in the bounded collector, before later parsing or observer
setup. It removes the former 5 ms polling quantization; startup comparisons across protocol versions are invalid. It includes helper bootstrap, launcher extraction,
kernel execution, and application initialization; it is not an isolated dispatch microbenchmark. Native and fat
arms use the same helper/control path. Primary throughput and latency come from the subsequent measurement window.

The observer runs in a separate process. Target and generator have separate process identity, resource accounting,
and optional cgroups. Process RSS differs from cgroup memory, and memfd/shmem and file-cache pages overlap with
other counters. Do not add overlapping counters into a purported total or interpret GOMEMLIMIT as an OOM guarantee.

Resource samples retain phase, source, units, and sampling interval. Sampled maxima are lower bounds on transient
peaks. Kernel high-water counters span their stated lifetime and cannot be split by subtracting peaks. The report
keeps startup, warmup, and steady-state sampled maxima separate from terminal process resource usage.

`exec_diagnostics` adds a distinct ptrace pass with executable-transition events and samples grouped by exec stage.
Tracing perturbs timing; those durations never enter primary performance comparisons. Trace permission failures
are recorded as unavailable. The tracer follows the initial thread; Go can execute the payload from another thread.
If the helper, launcher, and payload entries are not all captured (helper and payload for native execution),
the diagnostic is explicitly partial and its sample phases become unknown. Complete exec-stage observations and
cumulative high-water counters help separate launcher and post-exec behavior without claiming exact allocation
attribution. Missing extraction-only metrics remain unavailable. Complete thread-group tracing remains further work.

## Paired statistics and CI policy

The independent unit is a matched trial block. Compute candidate-minus-baseline differences, then bootstrap the
median difference with 10,000 resamples of those blocks. The recorded seed, PCG algorithm, and R7 quantile rule
make regeneration deterministic. Relative effects are median paired percentage changes and are unavailable for
zero baselines. Intervals are per-comparison 95% percentile-bootstrap intervals, not family-wide guarantees.

An interval touching zero is not significant. Fewer than five complete pairs produce descriptive results only.
An incomplete pair set is inconclusive. A nonsignificant result does not prove equivalence. Statistical significance
is separate from operational importance and the coarse regression thresholds used on noisy hosted PR runners.
No automatic outlier trimming or one-sided retry changes the inclusion rule after results are observed.

CI startup gates require repeated no-change calibration on the intended runner class; store the calibration
bundles and chosen absolute floor. Size gates are deterministic coarse comparisons. Sustained throughput, memory,
and latency remain separately reported until an appropriate noise model and gating policy are established.

## Evidence verification

Benchmark schema v2 carries typed observations, failures, histograms, pair IDs, controls, and provenance. Existing
v1 sample/R7 semantics and evidence digests remain unchanged. The schema version is independent of executable
format version. A bundle contains the raw experiment, schedule, environment, artifacts, generator output, telemetry,
analysis, Markdown/JSON reports, and `SHA256SUMS`. Verification rejects missing, altered, unlisted, and non-regular files.

Measured, derived, and model-assumption labels identify the basis of each metric. Missing values need an explicit
reason. Checksums detect modification against a supplied digest; they neither authenticate a publisher nor make
storage physically immutable. Cite the full source revision, baseline, tuning/cache state, trial count, hardware,
toolchain, uncertainty, and durable raw bundle for every published performance claim. Fixtures are never evidence
of performance. This documentation makes no numerical speedup claim.

Interrupted runs keep a `.partial-<experiment-id>` journal under the output directory:
`raw.json` contains the initial schedule and completed trials, and `trials/` retains their
raw outputs. The journal is removed after a verified bundle is published. It is diagnostic
input, not a checksummed completed bundle. SIGINT/SIGTERM cancel children and publish the
available trials; a hard kill leaves the journal for inspection.

For observer overhead calibration, repeat the same suite with `disable_observer: true`
and `false`, counterbalancing run order across repetitions. Disabled-observer results are
never release eligible. Compare fresh process trial medians; differences on a shared host
include host noise and cannot certify dedicated-runner overhead. The exec diagnostic is a
separate ptrace pass with a fresh cache directory matching the declared cold/warm state.
Its event RSS is cumulative, and its sampled phase maxima can miss peaks. Its timings are
excluded from all primary performance comparisons.

`packaging_ratio` is the sum of the uniquely built native payload file sizes divided by the
whole fat executable size, including stub and metadata. Throughput per quota vCPU and per
memory-limit GiB require the corresponding finite target limit to have been applied.
They never substitute host CPU count or physical RAM. Phase counter deltas use the first
and last available samples; a reset or single sample yields an unavailable value.

Resource samples are stored once in each trial's checksummed `telemetry.json`;
`raw.json` records its path, count, interval, and derived phase summaries. Readers verify
both the referenced sample count and sample schema. In-memory raw trial files have a
256 MiB budget; exceeding it stops the experiment and preserves its incomplete journal.
Shard large suites or increase the sampling interval. Release suites initially use 250 ms
sampling; the separate exec diagnostic keeps its finer interval and explicit limitations.

Both the current and base packer CLIs are built from their declared checkouts. Packaging
never substitutes the packer linked into a potentially older harness executable. The
harness's own VCS revision and dirty state come from its build information; an unknown
or dirty harness prevents release eligibility. A dirty base packer/stub also marks the
experiment dirty.


## Hosted publication and calibration

Evidence schema v2 optionally records runner provider, image/build, run/attempt/job/repetition and startup protocol.
Legacy bundles without this block remain readable and retain their original rendering. The metadata describes
reported VM identity rather than independently attesting physical hardware. Hosted runs always retain
`release_eligible=false`; a separate hosted publication policy verifies successful measurement and evidence
completeness. Its verdict does not mean absence of regression or dedicated-hardware certification.

Same-revision calibration uses independent hosted jobs, with repetitions 0-19 assigned to training and 20-29 to
holdout before measurements are observed. Group by architecture, CPU model, kernel, image build, Go version,
startup protocol and measurement settings; exclude only revision-independent names and transient cgroup paths.
Choose twice the largest absolute training median startup difference as the absolute floor. Require 20 training
and 10 holdout jobs in a class, no duplicate job identities, and no holdout coarse-gate failures. Other classes
remain report-only. Do not automatically loosen floors, trim outliers, or rerun only unfavorable observations.

Process lifetime CPU/fault/context-switch totals come from terminal wait4 accounting. Sampled /proc/status context
switch counters are explicitly labelled as thread-leader counters. V1 CPU-accounting time uses nanoseconds;
memory failure counts and throttled periods are counters. These remain distinct from sampled memory maxima,
process RSS and cgroup memory charges. Functional OOM probes run in separate small groups with the harness outside.

Cgroup memory amounts use byte units, including v1 hierarchical `total_*` fields and v2 slab/THP/cache fields.
Event and page-fault counters remain counts, following the [kernel v2 memory.stat reference](https://docs.kernel.org/admin-guide/cgroup-v2.html#memory)
and [v1 stat reference](https://docs.kernel.org/admin-guide/cgroup-v1/memory.html#stat-file).
