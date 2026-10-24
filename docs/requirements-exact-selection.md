# Requirements-exact selection: frozen evaluation

Issue #153 qualifies the existing selector and an exact-mask alternative. The
production baseline is `7adeba76cbc20507c196d230db961513235edb76`. Architecture
alias fixes are correctness changes and are assessed separately from timing.
This protocol must be committed before any selection benchmark is collected.

## Correctness and candidate boundary

An independent test oracle spells out flattened AMD64 and ARM64 requirements;
it does not consume the production requirement graph, rank or normalization
functions. Differential checks include sparse and dense features, missing
LAHF/SAHF, ARM v8/v9 branch negatives, unknown features/levels, architecture and
variant aliases, available subsets/order, forced levels, caps, disabled variants
and safe AVX-512 policies. Force retains its existing precedence over the other
policy filters. Errors remain distinguishable by their existing sentinel.

The experimental mask implementation remains in a `_test.go` file. Its static
requirement masks may be prepared once, but every timed call must build the host
feature mask and compatible available set, normalize inputs and apply the
existing policy engine. No precomputed host/available-set shortcut is timed as a
complete selection. ARM compatibility follows exact requirements, never every
rank below the host. The reference tests independently check production behavior;
candidate differential tests check the same selected original string, metadata
and error category.

## Fixed measurements and decision

Use Go 1.27.3, native Linux, `GOTOOLCHAIN=local`, `GOMAXPROCS=1`, `-cpu=1`,
`-benchtime=100ms`, `-count=8`, without race/coverage instrumentation. Record the
source commit, diff/clean state, protocol and source SHA-256, compiler version,
`uname`, CPU model, affinity, and readable cgroup quota/memory observations.
Host limits are observations, not imposed or isolated-resource guarantees.

Run `BenchmarkSelectVariantForHost` with current/exact-mask arms for these fixed
19 fixtures: AMD64 sparse/dense, cap, disabled, force, normalization, unknown and unavailable;
ARM64 sparse v8.0, sparse v8.2, sparse v9.0 without v8.9 requirements, dense all
16 levels, cap, disabled, force, force-unavailable, force-incompatible, unknown
and unavailable. Retain all eight
repetitions, errors, ns/op, B/op and allocs/op. Successful fixtures must check
results and errors outside the timer. Report median and range across repetitions;
these are descriptive repeated microbenchmarks, not independent population
samples or confidence bounds. Do not rerun, trim or extend the cohort to improve
its result. Preparation failure is incomplete and must be retained separately.

The conservative complexity gate for promoting masks requires all of:

- Exact correctness and metadata/error equivalence on every differential case.
- On **each** sparse ARM fixture, median savings of at least **100 microseconds
  per selection** and **25%** relative to current, with fewer allocations.
- No successful representative fixture's median time increases by more than 5%
  and no allocation count increases. Failure fixtures remain reported separately.

This is a preselected engineering threshold for adding a second representation
and its initialization/maintenance cost to a once-per-process operation, not a
universal useful-latency definition. Missing a gate retains the simpler selector
with the tests, evidence and justified decision. Passing isolated gates would
still require a separate frozen full-process comparison before claiming a launch
improvement. There is no mandatory sub-50-ns target.

Separately run `BenchmarkSelectionCompleteProcessContext` for direct native,
full-stub memfd, cold cache and warm cache using the existing golden fixture.
Each invocation starts a fresh process and must return the exact marker with no
stderr. The clock includes `exec.Cmd.Output`, process creation, actual launcher
work and payload exit. Cache preparation/removal and output checks are outside
the clock; cold/warm refers to the extracted-payload cache, with uncontrolled OS
page cache. No candidate launcher is executed: these measurements provide scale
only and establish neither mask-attributable savings nor service-request p99.
Parent benchmark allocations are not child RSS or child allocation measurements.
The e2e environment helper removes inherited runtime/launcher overrides;
`GOMAXPROCS=1` controls the benchmark parent. Direct children use Go runtime
defaults and launcher children use ordinary production cgroup autotuning.

The local native cohort is sufficient for the bounded implementation decision;
synthetic ARM feature inputs on amd64 are correctness/algorithm measurements,
not native ARM instruction timings. Hosted native amd64/arm64 correctness and
dispatch qualification remain separate required checks. Any later native timing
cohort must be retained separately and cannot rescue a failed local decision.

## Reproduction and retention

```bash
GOTOOLCHAIN=local GOMAXPROCS=1 go test ./internal/microarch -run '^$' \
  -bench '^BenchmarkSelectVariantForHost$' -benchmem -cpu=1 -benchtime=100ms -count=8
GOTOOLCHAIN=local GOMAXPROCS=1 go test ./tests/e2e -run '^$' \
  -bench '^BenchmarkSelectionCompleteProcessContext$' -benchmem -cpu=1 -benchtime=100ms -count=8
```

Keep raw output and provenance, including failures, in a checksummed archive
under `docs/benchmarks/selection-153/`. Record final decisions in a separate
results document. Full pinned Task qualification, >=95% overall coverage,
race/leak checks, compatibility and exact-head hosted CI are required before
review readiness. An evaluation or green CI does not authorize merge/release.
