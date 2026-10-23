# Verified cache descriptor: independent native confirmation

This is the prospective confirmation for #392. Freeze this file and its harness
before collecting timing. The earlier actionlint/Fortio/Task desktop screen is
exploratory evidence and does not satisfy this protocol. Preserve this protocol,
the exact candidate commit and all raw results, including failed and inconclusive
controls. A second run is a separate experiment; it cannot replace a failed run.

## Fixed design

The baseline is `c28bbe73fb4a43ddab85443865ba52c6f3ef4e01`. The candidate is the
exact PR head, never GitHub's synthetic merge commit. Run each complete experiment
once on a native Linux amd64 runner and once on a native Linux arm64 runner. Both
arms use the same Go 1.27.3 compiler, `CGO_ENABLED=0`, `-buildvcs=false`, stripped
full-profile stubs, Format v2, Zstandard better and no dictionary. Both pack
invocations set `SOURCE_DATE_EPOCH=0`. The baseline packer constructs both
arms from the same corpus variants. amd64 includes v1/v2 and forces v1; arm64
includes v8.0/v8.1 and forces v8.0. Higher variants are packaged but not forced on
potentially unsupported hardware. This confirmation is distinct from the complete
format/profile/codec/dictionary/security matrix in native runtime qualification.

Clear inherited `GO*` variables, including runtime tuning such as `GOMEMLIMIT`,
`GOMAXPROCS`, `GOGC` and `GODEBUG`, so both arms start from matched defaults.
Set `GOENV=off`, `GOTOOLCHAIN=local`, `GOOS=linux` and the native `GOARCH`
explicitly. Before compiling, preserve and verify the external `go version`,
`go env` settings and `uname -m`; they must agree with Go 1.27.3 and the native
amd64/x86_64 or arm64/aarch64 runner. This agreement does not establish hardware
isolation; the workflow must use the declared native runner class.

Use three fresh, fixed Go program families. Obtain them through the Go module
proxy with checksum-database authentication and require these exact module sums:

| Program | Module version | Module sum | Invocation |
| --- | --- | --- | --- |
| shfmt | `mvdan.cc/sh/v3@v3.14.1` | `h1:bXkhQWNHCs0KZEChF8hYS6FC+T2N9mUZLbQv9blditI=` | `--version` |
| yamlfmt | `github.com/google/yamlfmt@v0.21.0` | `h1:9FKApQkDpMKgBjwLFytBHUCgqnQgxaQnci0uiESfbzs=` | `-version` |
| gitleaks | `github.com/zricethezav/gitleaks/v8@v8.30.0` | `h1:5heLlxRQkHfXgTJgdQsJhi/evX1oj6i+xBanDu2XUM8=` | `version` |

Build each corpus variant once. Record downloaded module metadata and origin,
uncompressed variant identities, both stubs and complete bundles. Before timing,
require equal compressed payload bytes and equal index metadata after normalizing
only absolute offsets. Report both complete sizes and the stub-size delta; do not
claim equal complete sizes when the compiled implementation changes their size.

For each family and each of cold cache, warm cache and explicit memfd, collect
exactly **160 pairs**, with the two arms alternating order on successive rounds.
Each round visits all three families and all three paths; reverse that visit order
on alternate rounds. The saved schedule lists all **2,880 measured launches per
architecture** before the first launch. No pilot timing, adaptive stopping,
trimming, outlier removal, data-dependent retiming or precision-based extensions.

Every launch has a new private cache directory. Cold cache starts empty and uses
explicit cache execution. Warm cache starts with the exact selected native payload
already present under its SHA-256 filename with mode 0700. Explicit memfd starts
empty and must leave it empty. Check exact stdout/stderr and successful exit
against the direct native invocation. Verify the exact final cache bytes and mode
after cache paths. These are complete version-command lifetimes, including ELF
bootstrap, decompression/materialization where applicable, handoff and program
exit; they are not service readiness or request latency. Filesystem page cache is
uncontrolled and no hardware isolation or cold-storage claim is made.

## Measurements and decisions fixed before timing

Record each attempt's wall nanoseconds, user+system CPU nanoseconds, Linux
`wait4` maximum resident set size in KiB, output, errors, cache outcome and cleanup
outcome. The timeout is 15 seconds per measured launch; maximum RSS must never
exceed 512 MiB. This is an observed RSS gate, not an imposed 512 MiB cgroup limit.
Every planned attempt stays in the denominator. Failure to prepare an attempt's
cache, launch, validate or clean that attempt is a correctness/resource failure.
Continue the remaining schedule and retain the failure; never replace an attempt.
Global source, toolchain or fixture preparation failure aborts the study as
incomplete before timing; retain the frozen schedule and error, and do not count
unexecuted attempts as successes.

For each family/path compute the median paired candidate/baseline wall ratio and
a percentile 95% confidence interval from 2,000 paired bootstrap resamples, with
fixed seed 392. A useful cold-cache effect requires a median ratio <=0.95 and an
upper confidence bound <1.00 in at least two of three families on **each** native
architecture. Every cold-cache family additionally needs an upper bound <=1.05.
Every warm-cache and memfd family needs its entire confidence interval inside
[0.95, 1.05]. A control outside this equivalence interval, or an interval too wide
to fit, is an unresolved nonregression outcome and blocks qualification. Do not
attribute a memfd difference to descriptor handoff or dismiss it as noise.

The bootstrap treats paired rounds as approximately independent and exchangeable.
Temporal dependence or shared-runner interference can invalidate its uncertainty
estimate; retain the host and pressure observations and report that limitation.

For each family/path require the upper 95% bootstrap bound for the median paired
CPU ratio <=1.10. Require the upper 95% bootstrap bound for median paired RSS
increase <=10% of baseline median RSS +2 MiB. Preserve every CPU/RSS sample,
including failures; resource statistics computed from complete successful pairs
are explicitly conditional when any failure exists, and cannot override it.

Qualification passes only if every attempt succeeds, every resource and unchanged
path precision gate passes, and the cold useful-effect rule passes on both native
architectures. An unmet precision/effect gate is inconclusive or negative evidence,
never a pass. Report family-specific outcomes; do not select a favorable cohort,
combine architectures to rescue a failure, or estimate population p99 from this
sample. This protocol does not promise fleet-wide or service-p99 improvement.

## Execution and retention

The PR workflow `verified-cache-handoff.yml` executes this fixed study on the
`perf/verified-cache-descriptor` delivery branch and retains
the entire evidence directory with `if: always()`, including preparation errors,
the prospective protocol, schedule, source identities, raw JSONL observations,
compiled artifacts and per-family decisions. A workflow cancellation or truncated
schedule remains incomplete evidence. Workflow reruns remain visible as separate
run/attempt artifacts and must be disclosed; successful reruns do not erase earlier
failures. Collect new timing only after reviewing and freezing this protocol.

For an equivalent local native run after committing the frozen candidate:

```bash
go test ./tests/e2e/testdata/fd_handoff
task qualify-cache-handoff HANDOFF_BASE=/absolute/path/to/c28bbe73-checkout
```

The harness refuses an existing output directory. Preserve it after any outcome.
Production integration additionally requires the full security/compatibility
matrix and normal exact-head CI, race/leak, lint, vulnerability and coverage gates.
