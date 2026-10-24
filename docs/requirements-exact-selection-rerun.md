# Requirements-exact selection: user-requested local reassessment

The maintainer requested a new evaluation after reporting that gaming had
stopped. This prospectively recorded amendment authorizes one separate local
cohort. It supersedes the original protocol's prohibition on rerunning solely
for this requested reassessment; it does not replace, pool or erase either
previous local or hosted cohort. The original observations remain historical.

The candidate, fixtures, benchmark code and production selector are unchanged
from PR #400 head `22d3d8b9766d1d36220d084f91c1a72b1d204974`.
Commit this amendment before collection and record the measured commit, clean
source state, source hashes, Go version, CPU/affinity, process-relative cgroup
ancestors and system CPU pressure before and after the two serial commands.
No other local builds, tests or benchmarks are deliberately run concurrently.
The user's report is not proof of an idle or isolated host.

Use the exact [original protocol](requirements-exact-selection.md): Go 1.27.3,
native Linux, `GOTOOLCHAIN=local`, parent `GOMAXPROCS=1`, `-cpu=1`,
`-benchtime=100ms`, `-count=8`; all 19 selector fixtures with both implementations
and all four current-only process modes. Retain every repetition and failure;
do not tune, extend or repeat this cohort based on its outcome.

```bash
GOTOOLCHAIN=local GOMAXPROCS=1 go test ./internal/microarch -run '^$' \
  -bench '^BenchmarkSelectVariantForHost$' -benchmem -cpu=1 -benchtime=100ms -count=8
GOTOOLCHAIN=local GOMAXPROCS=1 go test ./tests/e2e -run '^$' \
  -bench '^BenchmarkSelectionCompleteProcessContext$' -benchmem -cpu=1 -benchtime=100ms -count=8
```

Apply the unchanged promotion gates: exact correctness/metadata/error
equivalence; **each** sparse ARM input saves at least 100 microseconds and 25%
with fewer allocations; every successful fixture stays within a 5% median time
increase and has no allocation increase. Report all failure fixtures separately.
Passing these gates would warrant a further frozen candidate-launch comparison;
the current-only process modes cannot establish mask-attributable launch gains.
Medians and ranges are descriptive, not confidence bounds. ARM inputs on this
amd64 machine do not establish native ARM timings, and parent allocations do
not measure child allocations or RSS.

Archive raw results, provenance, replay analysis and independent audit under
`docs/benchmarks/selection-153/`, with checksums, and publish a separate results
report. Assess whether the unchanged candidate merits promotion using this
cohort without selectively discarding previous evidence. The user's separate
authorization to review and merge PR #400 remains conditional on a clear review
and green CI at the final head.
