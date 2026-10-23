# Fixed-sample tail-risk POC (#377)

This experiment answers a bounded question: **does a fixed candidate change the
probability that one independent unit exceeds a predeclared latency SLO or fails,
by more than a practical margin?** It adds no runtime policy, format change or new
dependency. The existing paired-block bootstrap in `benchmarks/analysis/paired.go`
is preserved.

Run with the repository's Go 1.27.3 toolchain:

```bash
NO_COLOR=1 task research-377
go run ./experiments/research377/cmd/tailcal --mode=analyze \
  < experiments/research377/example-input.json
GOMAXPROCS=2 GOFLAGS=-p=2 go test -race -coverprofile=/tmp/research377.out ./experiments/research377/...
go tool cover -func=/tmp/research377.out
```

`task research-377 SAMPLES=600 REPLICATIONS=500 SEED=377` runs four seeded
calibration scenarios. The default command has a total work cap and no network,
privilege or kernel-control requirement. `analyze` consumes exactly one strict JSON
document from stdin, capped at 64 MiB, 100,000 observations per arm, and 1,000
predeclared comparisons. Unknown fields, duplicate unit IDs within an arm, omitted
observations, invalid durations and unsupported sampling contracts are rejected.

## Method and decisions

An observation violates the SLO when `duration_ns > slo_ns`, `failed=true` or
`timed_out=true`. A failure and a timeout on the same observation count as one
violation. The reported failure and timeout counts may overlap. Successful units
at exactly the SLO are compliant. A timeout is a known violation, rather than a
success with a short censored latency. No censored quantile is estimated.

For `x` violations in `n` fixed IID units, the POC inverts equal-tailed binomial
tests to obtain a Clopper-Pearson interval. The endpoints solve
`P_L(Bin(n,L) >= x) = alpha/2` and `P_U(Bin(n,U) <= x) = alpha/2`, with endpoints
0 and 1 for zero and all violations. The numerical implementation uses log
probabilities, a smaller-tail sum and 64 bisection steps; there is no normal
approximation or added statistics package. The binomial *construction* has finite
sample coverage in exact arithmetic. The implementation uses float64, with
outward-rounded final brackets; it is not an arbitrary-precision proof of its
roundoff error. Tests check published endpoints, exact small-sample coverage by
enumeration, independent polynomial CDF references and boundary sample costs.

For family error budget `a` and `m` planned candidate comparisons, each arm uses
`a/(2*m)`. The difference interval is
`[candidate.low-baseline.high, candidate.high-baseline.low]`. A union bound gives
simultaneous coverage of at least `1-a` for the `m` difference intervals, assuming
each arm's IID and fixed-sample contract holds. Correlation **between** arms or a
reused baseline does not invalidate this union bound. Dependence **within** an arm
does invalidate its binomial construction. These conservative bounds trade power
for a small, auditable first implementation.

The difference is candidate risk minus baseline risk, in absolute probability
units. With predeclared margin `d`:

| Decision | Required interval |
|---|---|
| Useful improvement | Entire interval below `-d` |
| Material regression | Entire interval above `d` |
| Practical equivalence | Entire interval inside `[-d,d]` |
| Inconclusive | Everything else |

An interval touching a material-change boundary cannot establish that direction.
The margin, threshold, sample counts, family and candidate must be chosen before
confirmation data are inspected. Budget exhaustion is not equivalence.

## Seeded calibration and the rare-state connection

The biological rare-state idea motivates retaining state history and checking
mixtures; it supplies no latency exponent or software probability law. The
calibration instead uses explicitly known binary event generators:

| Scenario | Baseline / candidate risk | Independent units? |
|---|---|---|
| No change | 1% / 1% | Yes |
| Injected benefit | 10% / 0.5% | Yes |
| Rare slow-state mixture | 3% / 3% | Yes |
| Shared latent state across each burst of 25 | 3% / 3% | Deliberately no |

The mixture model is `F=(1-r)F_fast+rF_slow`, where fast durations are below the
SLO and slow durations are above it. Those conditional distributions may be
bounded, heavy-tailed or multimodal: the SLO event depends only on `r`. Simulating
the event directly is sufficient for this estimand. This is not a test of a fitted
latency distribution, a tail quantile interval, or all possible dependence models.
In the correlated scenario, one Bernoulli draw is duplicated across a cluster;
the final partial cluster retains its actual size. Applying the IID formula to
these launch counts is intentionally incorrect and demonstrates the failure.

At seed 377 with 600 observations per arm and 500 replications, the retained
[calibration output](results/calibration-seed377.json) gives:

| Scenario | Difference coverage | False material direction | Useful-improvement rate |
|---|---:|---:|---:|
| IID no change | 100% | 0% | 0% |
| IID injected benefit | 99.6% | 0% | 100% |
| IID rare slow mixture | 100% | 0% | 0% |
| Correlated bursts treated as IID | **52%** | **48%** | 24.8% |

The dependent experiment has no real improvement. It also declares equivalence in
23.4% of replications despite the invalid IID inference. Coverage estimates have Monte Carlo intervals in the JSON; 500
replications do not establish universal calibration. The no-change cases stay
inconclusive at a one percentage point margin: this sample budget is inadequate
to establish that tight equivalence. This is an intended result. These results
are deterministic synthetic inference diagnostics, not microfat speedups or
independent-host evidence.

## Experiment-owner contract and sister POCs

The input preserves declared source revision, Go version, host, measurement kind
and dataset digest. They are experiment-owner metadata, not authenticated claims.
The tool cannot establish IID from a timestamp, host label, unit ID or
`iid_within_each_arm=true`. Attach raw data, environment controls, corpus hashes,
the fixed plan and the actual randomization/isolation protocol to any real report.
The example input is a tiny synthetic fixture and intentionally inconclusive.
Its dataset digest covers the output bytes of
`jq -c '{baseline,candidate}' experiments/research377/example-input.json`, including
the final newline. The CLI retains that digest without recomputing it.

- **#375 decoder concurrency:** requests/decodes within one burst share pressure
  and are dependent. Independent *burst-level* readiness/max SLO units may support
  this method when bursts themselves are independently replicated and stationary.
  Do not substitute their constituent launch count for the number of bursts. If
  the intervention affects failures, count them in the same denominator.
- **#161 dictionaries:** codec microbench timings are not process startup p99.
  Freeze dictionary candidates using training programs, then evaluate separately
  on held-out program families. Retain all tested variants and failed outcomes.
- **#376 lifecycle model:** a weighted empirical cold/warm CDF and its inverse are
  descriptive scenario estimates. Weighting estimated component CDFs does not
  produce independent samples or a confidence bound. Independent complete
  launches under a fixed mixture policy could instead supply SLO events here.

Keep first-ever launch, explicit memfd, auto cache miss, cold materialization and
verified warm cache as declared cohorts. History-sensitive state comparisons are
useful diagnostics, but do not condition away slow or failed outcomes from the
complete deployment population. The SLO risk here and a population p99 answer
different questions; for example, `P(latency > SLO) <= .01` is an SLO compliance
statement, not a precise estimate of the p99 latency in nanoseconds.

## Selection, holdouts and unsupported extensions

This POC supports fixed-sample confirmation only. Include all candidates and SLOs
whose confirmation results influence the choice in `comparison_family_size`,
including unreported losers. A candidate selected solely on independent training
data can have one fresh confirmation comparison; repeatedly checking the holdout
and choosing among candidates spends that independence. Multiple populations,
SLOs, conditions and later confirmations also need an error allocation. Merely
entering a confirmation description is not an enforced data split.

It intentionally does not implement optional-stopping confidence sequences,
block-bootstrap coverage claims, request-level load-generator correction,
automatic candidate selection or a time-uniform guarantee. Offered and observed
request rates and coordinated omission require a load protocol, not a numerical
patch to these intervals. Ordinary independent burst replications are preferable
to an unjustified effective sample size.

The existing paired bootstrap estimates the **median paired difference of trial
summaries**, including differences between per-trial p99s. That is a separate
estimand. This POC cannot fairly rank those intervals against binomial SLO-risk
intervals by comparing their widths. A future method comparison must first align
the estimand, independent unit and valid data model.

For an IID continuous population p99 interval from sorted raw launches, exact
order-statistic bounds are a possible next step: take `B~Bin(n,.99)` and indices
`L,U` satisfying `P(B<L)<=a/2` and `P(B>=U)<=a/2`, then use `[X_(L),X_(U)]` with
infinite sentinel bounds outside the sample. An upper bound needs at least 368
observations to be finite for a two-sided 95% construction; 299 only suffices for
a one-sided 95% bound. Neither number is a recommendation for useful precision.
This POC retains the simpler SLO-event method until measured sample costs justify
sequential or direct-quantile inference.

## Sources and promotion gate

- [NIST Engineering Statistics Handbook, exact binomial confidence intervals](https://www.itl.nist.gov/div898/handbook/prc/section2/prc241.htm)
  supplies the inversion construction and the 4/20 reference endpoint fixture.
- [Howard and Ramdas, Bernoulli 2022, revised manuscript](https://arxiv.org/abs/1906.09712v5)
  gives time-uniform quantile methods for IID data. It motivates the explicit
  fixed-sample limitation here; no sequential theorem is claimed for this POC.

Promote a method only after independent confirmation has useful precision at the
available sample budget, with failure retention and a defensible sampling unit.
The severe cluster failure is a stop signal for launch-level inference under
shared burst state. No production default is justified by this calibration.
Production integration would also require the relevant native matrix, race
tests and the repository's exact >=95% coverage gate; the experimental packages
are separately coverage-checked because the existing core coverage package list
does not automatically include `experiments`.
