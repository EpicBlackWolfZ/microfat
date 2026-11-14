# Disabled launcher diagnostics: first candidate evaluation

**Historical C1 decision: reject the first conditional-clock implementation.**
The conditional-clock candidate removed a small amount of isolated work but did not
meet the frozen practical startup-benefit rule in any disabled-logging cell. Two
CPU-resource intervals failed qualification. Those failures do not prove a causal
regression; they do prevent qualifying this candidate from this study. The
initial disposition restored the baseline launcher sources and retained the
evaluation, test-only probes and behavioral regressions. A distinct
[single-snapshot candidate](disabled-telemetry-snapshot-results.md) addresses C1's
duplicate enabled-path environment checks. Its evaluation is separate; these
original observations and decisions remain unchanged.

## Source and retained evidence

- Baseline: `6eb862b914e8b3dfdf820dcbd1bfa079e4275dd6`.
- Rejected experimental candidate: `c1d47eaa9935fd481290617d49f9aa0a4502324d`.
- One native Linux amd64 workstation cohort on 2026-10-24; Go 1.27.3,
  AMD Ryzen 9 3950X, 32 logical CPUs, kernel `7.2.11-200.nobara.fc44.x86_64`.
  Shared desktop, no hardware isolation or imposed CPU/memory cap. The launcher
  reported cgroup v2 with no finite resolved memory or CPU limit.
- [Fixed protocol](disabled-telemetry.md). The original prospective version,
  schedule and source/toolchain identities were frozen before timing.
- [Complete observations and evaluation archive](benchmarks/telemetry-41/local-c1d47ea.tar.gz)
  retains all 3,040 attempts, the original protocol/schedule, per-cell decisions,
  normalized payload/index comparisons, binary identities, before/after source
  checks, Go/compiler/linker/harness hashes, both allocation-probe logs, a host
  summary and the candidate production patch. Raw host captures remain in the
  local evidence directory; their hashes are included in the summary.
- Local full evidence: `.work/telemetry-review/local-study/`. There was one
  collection, no replacement attempts, no trimming and no rescue rerun.

Archive SHA-256:

`fd637337121beb3a6e626d241a866827f9d3dc46d180b146ee30e75a3df71b4b`

Uncompressed `observations.jsonl` SHA-256:

`76f280eabb5bd7d2a1031ab8615ee13352cc5b7b3e4b664ee437d18ce03bdedc`

## Attributable clock and allocation probes

The same test binary reconstructs unconditional timing and the conditional-clock
candidate around the same logger. Each profile/mode runs both arm orders with
`go test -run '^$' -bench '^BenchmarkDispatchDiagnostics$' -benchmem -count=5`.
The following are descriptive medians of ten benchmark outputs per arm, not
confidence intervals or measurements of complete-launcher allocation. Probe
output goes to `/dev/null`; it includes the supplied GC/environment fields.

| Profile / mode | Unconditional ns/op | Conditional ns/op | B/op, both arms | Allocs/op, both arms |
| --- | ---: | ---: | ---: | ---: |
| full / disabled | 202.30 | 63.82 | 0 | 0 |
| full / json | 2180.50 | 2269.00 | 1586 | 33 |
| full / debug | 1237.50 | 1317.00 | 232 | 12 |
| minimal / disabled | 204.00 | 70.21 | 0 | 0 |
| minimal / json | 2180.50 | 2266.00 | 1586 | 33 |
| minimal / debug | 1222.50 | 1311.50 | 232 | 12 |

Disabled timing saved approximately 134–138 ns in this probe and saved no
allocations: the existing disabled logger already returns before formatting.
The candidate adds an early environment predicate when logging is enabled;
JSON and debug probes became slower while retaining their allocation counts.
JSON allocations come from record construction, numeric/string formatting and
builder growth; debug includes optional GC/policy strings and formatted output.
These probes exclude environment construction for the payload, manifest parsing,
cgroup inspection, extraction, hashing, filesystem operations and exec. Their
zero disabled allocations do not mean the launcher allocates nothing.

## Complete-process results

All **3,040/3,040 attempts succeeded**, including exact stdout, logging-mode and
cache validation. Every cell has 80 complete adjacent pairs. Seventeen of
nineteen wall intervals satisfy the frozen ±5% equivalence band; the remaining
two are inconclusive. No disabled cell meets both the 1% and 5 microsecond
interval-supported useful-benefit requirements. Neither failed cell is dropped.

The table reports median candidate/baseline wall ratios with cell-wise 95%
paired bootstrap intervals, and the upper CPU-ratio bound. The CPU bound must
be <=1.10; the RSS bound passes in every cell. These are complete fixed-command
process lifetimes, not service request p99, application readiness or an internal
launcher subphase. RSS is Linux wait4 maximum RSS, not a cgroup peak or an imposed
limit. The measured paired median RSS increases and their intervals were zero
in every cell; that observation does not establish zero allocation or heap use.

| Profile / path / logging | Wall ratio [95% interval] | CPU upper bound | Wall outcome | Resource qualified |
| --- | ---: | ---: | --- | --- |
| native / native / off | 1.0335 [0.9921, 1.0499] | 1.0585 | equivalent | yes |
| full / memfd / off | 1.0362 [0.9923, 1.0679] | 1.1690 | inconclusive | **no** |
| full / memfd / json | 1.0164 [0.9707, 1.0342] | 1.0364 | equivalent | yes |
| full / memfd / debug | 0.9903 [0.9526, 1.0471] | 1.0802 | equivalent | yes |
| full / cold-cache / off | 1.0032 [0.9905, 1.0192] | 1.0161 | equivalent | yes |
| full / cold-cache / json | 0.9982 [0.9764, 1.0148] | 1.0146 | equivalent | yes |
| full / cold-cache / debug | 0.9964 [0.9809, 1.0080] | 1.0027 | equivalent | yes |
| full / warm-cache / off | 0.9739 [0.9626, 0.9935] | 0.9908 | equivalent | yes |
| full / warm-cache / json | 0.9930 [0.9836, 1.0064] | 1.0019 | equivalent | yes |
| full / warm-cache / debug | 1.0194 [1.0050, 1.0376] | 1.0393 | equivalent | yes |
| minimal / memfd / off | 0.9876 [0.9519, 1.0123] | 1.0881 | equivalent | yes |
| minimal / memfd / json | 1.0527 [0.9852, 1.0976] | 1.1097 | inconclusive | **no** |
| minimal / memfd / debug | 1.0009 [0.9808, 1.0388] | 1.0901 | equivalent | yes |
| minimal / cold-cache / off | 1.0067 [0.9980, 1.0175] | 1.0113 | equivalent | yes |
| minimal / cold-cache / json | 0.9983 [0.9899, 1.0082] | 1.0227 | equivalent | yes |
| minimal / cold-cache / debug | 0.9976 [0.9875, 1.0037] | 1.0181 | equivalent | yes |
| minimal / warm-cache / off | 0.9961 [0.9849, 1.0114] | 1.0191 | equivalent | yes |
| minimal / warm-cache / json | 1.0016 [0.9860, 1.0181] | 1.0340 | equivalent | yes |
| minimal / warm-cache / debug | 0.9988 [0.9825, 1.0116] | 1.0128 | equivalent | yes |

The full/memfd/off CPU ratio is 1.0942 [1.0226, 1.1690]; the minimal/memfd/JSON
ratio is 1.0587 [1.0059, 1.1097]. Their upper bounds miss the resource gate.
The unchanged native control also has CPU ratio 1.0383 [1.0010, 1.0585]. This
control supports retaining environmental/order uncertainty, not subtracting a
post-hoc correction or dismissing unfavorable observations as noise.
`correctness_pass=true`, `resource_pass=false`, `all_cells_equivalent=false`
are retained verbatim. The harness exited nonzero as specified.

## Original disposition and verification boundary

For C1, the useful effect is unestablished and resource qualification is unresolved.
The original decision was to keep the existing implementation. C1 remains
reproducible through its commit and archived production patch; its helpers live
only in test code. A small microbenchmark saving does not justify promoting C1
over the failed frozen complete-process qualification. This is a completed
bounded C1 evaluation and retention decision, not proof of universal negligible
overhead or regression-free startup. It does not resolve the separate historical
service-p99 investigation in #230.

Functional compatibility is checked separately across both formats, full/minimal
launchers, none/LZ4/Zstandard with and without dictionaries, explicit memfd, cold
and warm cache, automatic warm-cache execution, disabled settings, JSON/debug
output, environment hints and errors. Native amd64/arm64 CI requires that matrix;
ordinary lint/security/race/coverage/runtime checks apply to the final delivery.
The study above is native amd64 evidence only. No native arm64 performance
qualification, complete-process improvement or service-p99 claim is made.
Issue closure requires the reviewed, merged disposition; green CI alone does
not turn C1 into a qualified optimization. See the
[separate snapshot report](disabled-telemetry-snapshot-results.md) for the final
implementation decision.
