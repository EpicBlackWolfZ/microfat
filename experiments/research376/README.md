# Lifecycle cost POC (#376)

This isolated analyzer makes codec/deployment tradeoffs explicit. It executes no
workloads, changes no dispatch defaults and adds no binary format. Give it named,
matched conditional cold/warm observations plus explicit deployment scenarios:

```sh
NO_COLOR=1 task research-376
NO_COLOR=1 task research-376 RESEARCH_INPUT=/absolute/path/observations.json > report.json
go test -race -cover ./experiments/research376
```

The checked-in `testdata/example.json` is **synthetic**, not measured performance.
Replace its provenance and samples before drawing any operational conclusion.
JSON output remains machine readable; Task emits no progress to stdout.

## Model and interpretation

For warm probability `h`, the model inverts the discrete empirical CDF
`F(t) = h F_warm(t) + (1-h) F_cold(t)` for p50/p99. It does not average branch
quantiles or interpolate between atoms. In particular, a separated warm/cold
example can move to a different branch near 1% cold probability. This threshold
depends on both measured distributions; it is not a universal cache-hit target.

Weights and quantile thresholds are parsed as binary64 probabilities. CDF
comparisons then use their exact rational values, avoiding rounding across a
quantile atom; no broad epsilon merges distinct neighboring probabilities.

For **means only**, with complete artifact bytes `S`, transfer count `D`, declared
effective bandwidth `B` and launch count `N`, the scenario cost is

`D S/B + optional_pack_time + N [h mean(warm) + (1-h) mean(cold)]`.

Both directions of break-even against the first candidate are reported: a smaller
but slower candidate can be cheaper *below* the crossing launch count; a larger
but faster one can become cheaper *above* it. A crossing is a real-valued model
boundary, not an instruction to round down, switch defaults or assume equal cost
at the next integer. Equal/tied linear cases are explicit.

Transfer and launches are assumed additive. Concurrent transfer/startup,
multi-host deployments, background prewarming and cache materialization need
their own explicit scenarios. Whole artifact transfer bytes are distinct from
the selected payload read at launch. No stage-duration sum, causal speedup,
memory Pareto frontier or automatic policy is inferred.

## Input contract

Input is bounded to 16 MiB, 32 candidates, 128 scenarios and 100,000 observations
per candidate/state. Durations are positive integer **nanoseconds**, at most one
hour. Pack time can be zero. Artifact bytes, launch counts and transfer counts
are bounded. Scenario warm probability must be in [0,1], and effective bandwidth
must be finite and at least one byte/second. Unknown fields are rejected.
The total successful-observation count times scenario count is capped at four
million, keeping the input-size and exact-arithmetic work budgets independent.

Provenance requires a source label/revision, the corpus SHA-256, Go 1.27.3, host
class, evidence scope and measurement kind. It is user-supplied attribution,
not a cryptographic attestation or independently established IID guarantee.

- Scope is `synthetic`, `local-native-exploratory` or `imported-measurements`.
- Measurement kind is `full-process-exit`, `application-ready` or
  `codec-pipeline`. Only one comparable kind belongs in a single input.
- `full-process-exit` includes parent process launch, the payload's initialization,
  its requested work and exit. It must not be renamed pure exec/startup time.
- Each candidate records successful samples and failed observation counts for
  **both** states. Any failures produce an explicit incomplete result with null
  predictions and no break-even comparison. Failed runs cannot become winners
  by disappearing from the denominator. Fully failed states are representable.

Successful auto-mode memfd execution does **not** populate microfat's cache.
The warm fraction must come from a declared explicit prewarm/deployment policy,
not an assumption that repeated launches become cache hits. Existing warm entries
still receive mandatory descriptor-bound SHA-256 verification on each launch.

## Evidence and connections

Small conditional samples yield descriptive empirical scenarios, not qualified
population p99. Issue #377 supplies exact independent-unit/SLO methods; neither
its IID assumptions nor existing median paired-block bootstrap uncertainty can
be silently applied to reused/correlated mixture samples. Keep host and workload
classes distinct and validate predictions on held-out programs and runs.

Issue #161 supplies complete dictionary-inclusive artifact costs. Issue #375
provides codec/pipeline and concurrency observations, which must keep their own
measurement kind instead of being pooled with full-process timings. Issue #380
can later replay sequential prewarm decisions using these conditional costs.

The first POC tests the lifecycle/quantile model, not the entire research issue.
Intervention sensitivity, uncertainty in deployment weights, native ARM64,
dedicated hardware and held-out predictive qualification remain follow-ups.

## Retained local campaign

`evidence/` retains both applications' observation inputs, canonical analyzer
reports, every timed observation, artifact identities, a hash-pinned corpus
manifest and untimed dispatch diagnostics. Replay the analyses with:

```sh
task research-376 RESEARCH_INPUT=experiments/research376/evidence/demo-input.json
task research-376 RESEARCH_INPUT=experiments/research376/evidence/microfat-input.json
```

The campaign used three real amd64 variants (v1/v2/v3) per application and the
production full stub, codecs none/LZ4/Zstd, 60 observations per codec/state and
zero failed runs: **720 process executions**, plus separate untimed checks.
Source/corpus baseline is `ada376b4`; rebasing the research code onto the later
`94cb9f6` release-preparation commit does not change these historical identities.
The fixed campaign collector is retained as a text method record, not a supported
general-purpose command. Its source, build flags, binaries and manifest have
recorded hashes. Timings naturally cannot be reproduced byte-for-byte; analysis
of the retained inputs can.

The labels matter:

- Cold observations forced **memfd extraction**, after pack/verify/prewarm had
  already touched the files. They are not cold-disk/page-cache measurements and
  do not include auto's cache-miss lookup. Cache entries already existed.
- Warm observations requested auto with the explicitly prewarmed private cache.
  Untimed JSON dispatch probes confirm actual `cache` execution, v3 selection
  and GOMAXPROCS=4 for every artifact; cold probes confirm `memfd`. Instrumented
  probes are excluded from the timing arrays.
- The collector ran with GOMAXPROCS=4/-p=4; pack and prewarm inherited that
  environment and launches explicitly set GOMAXPROCS=4. No affinity, CPU quota,
  memory-pressure or dedicated-machine isolation was imposed.
- Cold/warm order alternates inside each codec; codec order itself is fixed.
  Drift and desktop background activity prevent a causal cross-codec ranking.
- Pack duration has one observation per codec. Scenario bandwidth (10 MB/s),
  transfer/launch counts and warm fractions are hypothetical assumptions.
- Prewarm setup/check time and retention cost are **not charged**. High warm
  fractions therefore illustrate conditional launch costs, not net prewarm ROI.
  Issue #380 must add those costs before policy recommendations.
- With 60 IID continuous observations, the sample maximum exceeds true p99 with
  probability only `1-.99^60`, about 45.3%. The finite crude upper-bound minima
  are 299 for one-sided 95% and 368 for two-sided 95%; neither ensures useful
  precision, and these conditional observations are not IID mixture launches.

The defensible result is a replayable separation of complete-byte transfer cost,
conditional mean launch cost and empirical mixture tails. It does not establish
a production codec winner or qualified p99 improvement.

Metabolic/catalytic control motivates comparing feasible interventions rather
than assuming a conspicuous stage controls the outcome; no chemical steady-state
law is asserted for transient startup quantiles. See the source ledger in issue
#378. Before production promotion, require independent confirmation, practical
margins and all relevant repository/runtime qualification.
