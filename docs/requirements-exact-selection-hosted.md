# Separate hosted selector characterization

This prospective amendment is committed before hosted timing. The original
[local protocol](requirements-exact-selection.md) and raw cohort remain immutable
in their archive. After the local cohort completed, the user disclosed gaming on
the workstation in the same session; exact overlap is unknown. Local comparisons
are exploratory and do not establish mask-caused performance effects.

Collect **one fixed cohort per native architecture**, amd64 and arm64, in the
existing GitHub native-runtime jobs at this PR's recorded source SHA. These are
separate hosted characterizations, not replacements for the local cohort. The
decision remains to retain the simpler production selector. Do not pool cohorts,
change thresholds, tune fixtures, add samples or promote masks from a favorable
subset. Preserve incomplete/error attempts and any rerun as a new cohort.

Use the same 19 fixtures × current/exact-mask × eight repetitions, and the same
four current-only process-context modes × eight repetitions. Go 1.27.3,
`GOTOOLCHAIN=local`, parent `GOMAXPROCS=1`, `-cpu=1`, `-benchtime=100ms`, no
race/coverage instrumentation. Child runtime/launcher environments and clocks
retain the local protocol's documented behavior. Run the isolated benchmark
first, then process context, after the ordinary runtime qualification steps.
There are no pilots, timed warmups beyond Go's built-in benchmark calibration,
replacement attempts, trimming or adaptive extensions. Log every reported
iteration count, ns/op, B/op, allocs/op and failure.

Before collection require native `GOARCH` and the checked-out SHA to match the
workflow context; retain clean status, protocol/implementation hashes, selected
`go env` values, Go/compiler/linker hashes, kernel/CPU/affinity and readable
process-relative cgroup ancestor quota/memory files. Missing observations stay
unknown. Recheck source/status/hashes after collection. Hosted VMs and readable
limits establish neither dedicated hardware nor isolation from other activity.

Commands on each native host:

```bash
GOTOOLCHAIN=local GOMAXPROCS=1 go test ./internal/microarch -run '^$' \
  -bench '^BenchmarkSelectVariantForHost$' -benchmem -cpu=1 -benchtime=100ms -count=8
GOTOOLCHAIN=local GOMAXPROCS=1 go test ./tests/e2e -run '^$' \
  -bench '^BenchmarkSelectionCompleteProcessContext$' -benchmem -cpu=1 -benchtime=100ms -count=8
```

Apply the original descriptive median/range, 100 µs/25% sparse-work,
allocation/nonregression criteria unchanged to each architecture separately;
report every fixture and all failures. Do not interpret these repeated
microbenchmarks as independent population samples or use the current-only
process context to claim candidate-attributable launch savings or service p99.
Correctness failures make the relevant cohort incomplete, not a performance pass.

Retain raw logs, source/toolchain/host metadata and separately replayed summaries
in the runtime artifacts, including on failure. After collection, preserve
checksummed copies and the per-architecture disposition with the reviewable
report. Remove the temporary branch-specific timing step once evidence is
retained; the native requirements/policy tests remain permanent. Final-head
source CI remains separate from the measured-source identities.
