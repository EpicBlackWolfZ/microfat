# Launcher diagnostics mode snapshot: frozen C2 protocol

**Disposition:** C2 was not adopted. All eighteen launcher wall comparisons and
all resource gates passed, but the unchanged native control was inconclusive.
The [completed C2 evaluation and full retained cohort](disabled-telemetry-snapshot-results.md)
preserve that failed adoption outcome. The exact prospective protocol remains in
the evidence archive and at frozen candidate
`813d168661f5ad4e182011ee1dfcde451695ee39`; the thresholds below are unchanged.

This is a new, source-specific experiment for #41. Freeze this protocol, fixture,
harness and exact candidate commit before collecting C2 timing. The rejected
[C1 evaluation](disabled-telemetry-results.md), its original protocol and complete
raw cohort remain unchanged. C1 did not qualify: two resource intervals failed
and two wall intervals were inconclusive. C2 is a different implementation that
reads the dispatch logging mode once per launch and passes that snapshot to the
timers and logger. It removes C1's duplicate enabled-path environment lookups.
C1 and C2 must never be pooled or presented as retries of the same source.

## Candidate, source and fixture

Use clean baseline `6eb862b914e8b3dfdf820dcbd1bfa079e4275dd6` and the exact clean
committed C2 source recorded in `freeze.json`. Preserve both commits and all
frozen source hashes. Recheck exact heads and clean status immediately after
build preparation, before timing, and again after collection. Source changes
invalidate attribution and abort with retained evidence.

The candidate snapshots dispatch mode locally, without package-global environment
caching. Preserve existing dispatch semantics: nonempty `MICROFAT_LOG` enables
logging, mixed-case `json` selects JSON, and other nonempty log values select
text. Debug-only dispatch is disabled for empty, `0` and case-insensitive `false`;
other nonempty debug values select text. Existing stricter error-hint and cgroup
debug semantics remain separate. Disabled dispatch skips its timing clocks and
formatting. Enabled logging consumes the captured mode without reading those
controls again.

The enabled launcher timer starts **after** the initial mode snapshot, still after
early privilege and executable-path work. It excludes mode parsing, Go runtime
bootstrap, diagnostic error output, dispatch formatting/emission, exec handoff
and payload execution. Memfd extraction timing includes decode and integrity
hashing; cache extraction timing includes materialization/publication and the
validated descriptor. A warm cache retains zero extraction duration while
hashing the executed descriptor. These internal timings are separate from the
complete-process lifetime measured below.

Use native Linux amd64 or arm64 and Go 1.27.3, `CGO_ENABLED=0`, `-trimpath`,
`-buildvcs=false`, stripped binaries and an empty build ID. Build the fixed
`tests/e2e/testdata/telemetry/fixture` once at `GOAMD64=v1` or `GOARM64=v8.0`.
Its only invocation is `--startup-only`, printing the exact marker
`MICROFAT_TELEMETRY_STARTUP_READY_v1` and exiting. The same baseline packer builds
both arms as Format v2, Zstandard better, no dictionary, `SOURCE_DATE_EPOCH=0`.
Require identical compressed payload bytes and index metadata after normalizing
only payload offsets. Keep native/packer/stub/bundle hashes, complete sizes and
each profile's size deltas. Format/codec/security compatibility remains a
separate behavioral matrix.

Clear inherited `GO*`, `MICROFAT_*`, `CGO_ENABLED`, `SOURCE_DATE_EPOCH` and
`XDG_CACHE_HOME`; explicitly set `GOENV=off`, `GOTOOLCHAIN=local`, `GOWORK=off`,
`GOOS=linux`, native `GOARCH` and `CGO_ENABLED=0`. Runtime defaults and ordinary
launcher cgroup autotuning apply equally to both arms. Verify external compiler
version/settings and machine; hash resolved PATH Go, GOROOT Go, compiler, linker
and the executing harness. Capture CPU/kernel/pressure observations, current
unified cgroup and all ancestors' quota/stat and memory max/high/current/events
within `/sys/fs/cgroup`, and the launcher's resolved limit interpretation.
Mark unavailable observations unknown. No imposed cgroup cap or isolated-hardware
claim follows from these observations.

## Fixed C2 schedule and sampling units

Use eighteen launcher cells: full/minimal × explicit memfd/empty explicit cache/
warm explicit cache × off/JSON/debug. Off omits both logging controls; JSON sets
only `MICROFAT_LOG=json`; debug sets only `MICROFAT_DEBUG=1`. Add one unchanged
native/off control using the identical native binary for both arms.

Collect exactly **320 adjacent pairs per cell**, totaling **12,160 fresh process
launches**: 11,520 launcher launches and 640 native controls. This larger fixed
design is chosen before C2 collection because C1 exposed weak CPU precision; it
does not change C1's sample, thresholds or failed disposition. Every round visits
all nineteen cells. Alternate baseline/candidate order and reverse cell visit
order on alternate rounds. Save the entire exact schedule before timing.

Each attempt has a new private cache directory. Cold begins empty. Warm is seeded
outside the timer with the exact selected payload under its SHA-256 name, mode
0700. Memfd/native must leave the directory empty. Validate exact fixture stdout,
mode-appropriate stderr, final cache bytes/mode and cleanup outside the timer.
Off emits no stderr; JSON emits one dispatch record with the expected variant,
hash and mode; text debug retains its dispatch fields.

The sampling unit is a paired fresh-process launch within a round. Per-cell paired
rounds are treated as approximately independent/exchangeable for resampling;
temporal dependence or shared-host activity can invalidate that approximation.
Page cache is uncontrolled. Cold/warm describes extracted payload state, not cold
storage. No timed warmups, pilots, outlier removal, trimming, adaptive stopping,
precision extensions, replacement attempts or same-source rescue reruns.

## Frozen observations and qualification gates

Preserve every planned attempt's wall nanoseconds for complete `exec.Cmd.Run`,
child user+system CPU nanoseconds, Linux wait4 maximum RSS in KiB, output, errors,
cache and cleanup outcomes. The lifetime includes bootstrap, launcher, decode/
materialization where applicable, handoff, fixed program exit and output capture.
It does not measure readiness, requests, fleet startup or population p99.

Each launch has a ten-second timeout and an observed RSS ceiling of 512 MiB.
Preparation, launch, timeout, output/cache validation or cleanup failures stay in
the denominator; continue all remaining attempts without replacement. Global
source/toolchain/fixture preparation failure aborts as incomplete. An interrupted
or truncated cohort remains incomplete. Keep raw JSONL as written and retain all
failed or inconclusive controls. No subsequent cohort can erase this one.

For each cell report median paired candidate/baseline wall and CPU ratios, median
paired baseline-minus-candidate wall saving, and paired RSS increase. Use the same
2,000 paired bootstrap resamples, seed 41, and percentile 95% intervals as C1.
These are cell-wise intervals, not simultaneous family-wise confidence bounds.

The entire wall interval must be within **[0.95, 1.05] for every cell**, including
unchanged native and enabled-logging controls. An interval overlapping/outside the
band is inconclusive or out of equivalence, not a pass; an interval wholly above
1.05 is a regression and one wholly below 0.95 is an improvement outside the band.
Neither an unexpected control improvement nor a wide interval may be discarded.
Require every CPU-ratio upper bound <=1.10 and every RSS-increase upper bound
<=10% of baseline median RSS +2 MiB. Missing precision fails these gates. Resource
statistics using complete successful pairs when any attempt fails are conditional
and cannot override that failure.

C2 production adoption requires **all correctness, resource and wall/control
equivalence gates**, plus focused evidence that the snapshot removes disabled
diagnostic work and preserves zero disabled diagnostic allocations. Run
`BenchmarkLaunchDiagnosticsSnapshot` in both profiles, cold and warm timer shapes,
all three modes and both arm orders, retaining every output. Include mode snapshot
cost in the candidate shape and existing environment lookup cost in the baseline
shape; use the same fields and `/dev/null` sink. Report all ten outputs per arm
and their descriptive medians for each profile/path/mode. Attribution requires
source review confirming absent disabled clock/formatting paths and zero B/op
and allocations/op in both disabled arms; probe medians are descriptive, not an
extra startup-benefit threshold.
Enabled results are retained; complete-process gates must still pass for them.
These probes exclude payload environment construction, parsing, cgroups, codecs,
I/O, hashing and exec; they do not establish an allocation-free launcher.

A useful complete-process startup claim still requires an off cell's wall-ratio
upper bound <=0.99 **and** median-saving lower bound >=5,000 ns. This threshold is
unchanged; adoption for attributable diagnostic-work reduction does not imply
that such a startup improvement occurred. Report all cells and architectures
separately, without selecting favorable subsets or pooling them to rescue a gate.

The harness labels this study `snapshot` and separately reports correctness,
resource, all-cell equivalence and complete-process qualification. Snapshot mode
exits nonzero on any incomplete, failed or inconclusive gate. Its zero exit covers
complete-process nonregression only; focused attribution/allocation evidence and
ordinary exact-head functional/security qualification are additional requirements.
If any acceptance requirement is unmet, retain baseline production behavior and
preserve this cohort as negative/inconclusive evidence. Do not relax gates after
seeing results.

## Execution and retention

After freezing clean source, run focused harness tests, then collect once into a
new directory. The original 80-pair design remains available with
`-study original` (the default); use the C1 checkout to reproduce its historical
source and protocol, never the C2 source as a replacement for C1.

```bash
go test -race ./tests/e2e/testdata/telemetry
go run ./tests/e2e/testdata/telemetry -study snapshot \
  -base /absolute/path/to/6eb862b-checkout \
  -candidate /absolute/path/to/frozen-c2-checkout \
  -output "$PWD/.work/disabled-telemetry-snapshot-evidence"
go test ./cmd/microfat-stub -run '^$' -bench '^BenchmarkLaunchDiagnosticsSnapshot$' \
  -benchmem -benchtime=200ms -count=5
go test -tags minimal ./cmd/microfat-stub -run '^$' -bench '^BenchmarkLaunchDiagnosticsSnapshot$' \
  -benchmem -benchtime=200ms -count=5
```

The output directory must not exist. Keep protocol/schedule, source/compiler/
artifact identities, matching payload/index evidence, raw attempts, source
rechecks, host observations, all decisions and any failure text. Preserve focused
probe logs alongside this evidence. Routine CI must not silently rerun timing or
replace a failed study with later green code checks. Exact-head lint, race/leak,
coverage and native full compatibility/runtime qualification remain separate.

## Recorded disposition

The single completed C2 cohort recorded 12,160 correct attempts with
`resource_pass=true`, `all_cells_equivalent=false` and
`complete_process_qualified=false`. Its unchanged native wall interval exceeded
the equivalence band, so the frozen contract requires baseline production behavior
to remain. C1 and C2 stay separate, retained experiments. Reproduction uses the
exact rejected source at `813d168`, never the final retention head, and any later
collection cannot replace this failed control. See the
[C2 results and archive](disabled-telemetry-snapshot-results.md) for every cell,
focused probe, source identity and qualification boundary.
