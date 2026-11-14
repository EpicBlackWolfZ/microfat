# Launcher diagnostics mode snapshot: completed C2 evaluation

**Decision for #41: reject C2 and retain baseline production timing/logging.**
The snapshot removed disabled diagnostic clock work without repeating enabled-mode
lookups. All eighteen launcher wall comparisons and all resource gates passed.
The unchanged native control did not fit the frozen ±5% wall equivalence band, so
C2 did not qualify for adoption. This is unresolved measurement/control uncertainty,
not evidence that the candidate caused a startup regression. No gate was waived.

## Source and retained evidence

- Baseline: `6eb862b914e8b3dfdf820dcbd1bfa079e4275dd6`.
- Rejected C2: `813d168661f5ad4e182011ee1dfcde451695ee39`. Reproduction uses this frozen
  source, not the final retention head, which restores baseline production code.
- [Frozen C2 protocol](disabled-telemetry-snapshot.md); the archive contains its
  exact prospective version from C2. The [C1 evaluation](disabled-telemetry-results.md)
  and its separate archive remain unchanged. The cohorts are not pooled.
- One native Linux amd64 workstation cohort on 2026-10-24, Go 1.27.3,
  AMD Ryzen 9 3950X 16-Core Processor, 32 logical CPUs, kernel
  `7.2.11-200.nobara.fc44.x86_64`. Shared desktop; no hardware isolation or imposed
  CPU/memory cap. The launcher resolved cgroup v2 with no finite CPU/memory limit.
- [Complete C2 observations/evaluation archive](benchmarks/telemetry-41/local-813d168.tar.gz)
  retains every observation, schedule, decision, original protocol, source recheck,
  normalized payload/index comparison, binary/toolchain identity, both probe logs,
  exact macro failure/output, host summary, production-only patch, full source patch
  and exact candidate probe source. It contains no executables or private raw host
  captures. Raw host capture hashes are in `host-summary.json`; complete local
  artifacts remain in `.work/telemetry-review/snapshot-study/`.
- One macro collection and one prescribed full/minimal probe collection; no
  replacement attempts, outlier removal, retiming or rescue rerun.

Archive SHA-256:

`268c411ba59258fd4d570e3deb00e6700d5fbd9814bf0bb8b6220d09661a0a24`

Uncompressed `observations.jsonl` SHA-256:

`0e264812b4853e33f5ba626ccb9648bce73f2e1ee07127388e855d3a2f58abf3`

The payload is identical in both arms: 1,511,548 bytes, SHA-256
`6fa2253e0686ab8227d3c4d0df1ec8edaf00e92fb771c6971762a7a3ee880685`. Baseline packing uses Format v2,
Zstandard better and no dictionary. Every normalized index and compressed payload
comparison passed. Complete sizes differ where the compiled stub differs:

| Profile | Stub bytes baseline / C2 | Stub delta | Bundle bytes baseline / C2 | Bundle delta |
| --- | ---: | ---: | ---: | ---: |
| full | 3,993,724 / 3,997,820 | +4,096 | 4,697,096 / 4,701,192 | +4,096 |
| minimal | 3,756,156 / 3,756,156 | +0 | 4,459,528 / 4,459,528 | +0 |

The external compiler, linker and executing harness are bound by recorded hashes;
all stub/bundle/native/packer hashes and sizes are in `identities.json` and the
matching records. Source hashes and exact Go settings are retained in
`freeze.json` and `toolchain.json`:

| Tool | SHA-256 |
| --- | --- |
| Go (resolved PATH and GOROOT) | `5f5eb6d0b2fb6aa0bb5d5f8c5f1c944e0de955c925f0111af92a9d8f79680b5b` |
| Compiler | `c331d8fc6c4ae9dbe20e8198435db0455f2a5ca55d2d02fc6d7337c906be8283` |
| Linker | `c9990b939ed48a77ca9a29b33d408d44b56fc4950a28e3d3533025a8dc08b2fe` |
| Executing harness | `69943bca37f2e6868b1f19c068069fe3be6fcbe87c77dc5bddf8df138c99c494` |

Both source rechecks retained clean baseline/C2 heads before and after timing.
Host observations span 2026-10-24T15:23:05.227241133Z to
2026-10-24T15:26:02.695881192Z; raw captures remain local and their hashes
bind the archived public summary.

## Focused diagnostics probes

The fixed command used `BenchmarkLaunchDiagnosticsSnapshot`, `-benchmem`,
`-benchtime=200ms`, `-count=5`, both arm orders and full/minimal profiles. Each
profile/path/mode/arm has ten outputs; all 240 outputs are retained. Cold/warm are
clock-call shapes in this microbenchmark, not actual extraction/cache workloads.
The snapshot arm includes its one mode read. Both arms use the same logger fields
and `/dev/null` sink. Values below are descriptive medians, not confidence bounds;
allocation columns show every observed minimum–maximum, legacy / C2.

| Profile / timer shape / mode | Legacy ns/op | C2 ns/op | B/op legacy / C2 | Allocs/op legacy / C2 |
| --- | ---: | ---: | ---: | ---: |
| full / cold / disabled | 202.45 | 75.47 | 0 / 0 | 0 / 0 |
| full / warm / disabled | 134.65 | 73.48 | 0 / 0 | 0 / 0 |
| full / cold / json | 2179.00 | 2212.50 | 1586–1587 / 1586–1587 | 33 / 33 |
| full / warm / json | 2128.50 | 2127.00 | 1586 / 1586 | 33 / 33 |
| full / cold / debug | 1230.50 | 1230.50 | 232 / 232 | 12 / 12 |
| full / warm / debug | 1166.00 | 1164.00 | 232 / 232 | 12 / 12 |
| minimal / cold / disabled | 208.60 | 75.10 | 0 / 0 | 0 / 0 |
| minimal / warm / disabled | 140.05 | 73.44 | 0 / 0 | 0 / 0 |
| minimal / cold / json | 2176.50 | 2208.00 | 1586–1587 / 1586–1587 | 33 / 33 |
| minimal / warm / json | 2141.50 | 2147.00 | 1586–1587 / 1586–1587 | 33 / 33 |
| minimal / cold / debug | 1222.50 | 1232.00 | 232 / 232 | 12 / 12 |
| minimal / warm / debug | 1157.00 | 1162.50 | 232 / 232 | 12 / 12 |

Disabled cold timing saved approximately 127–134 ns and warm timing 61–67 ns in
these probes. Disabled allocation counts stayed zero in both arms. C2's cold JSON
medians remained slightly higher; enabled results are all retained without claiming
an across-mode speedup. Conditional clock/formatting paths establish attribution;
these probes exclude payload environment construction, parsing, cgroups, codecs,
I/O, integrity hashing and exec. They establish neither allocation-free launching
nor a useful complete-process startup benefit.

## Complete-process results

All **12,160/12,160 attempts succeeded**, exactly matching the frozen schedule.
Every cell has 320 complete pairs. Eighteen launcher wall intervals satisfy the
frozen ±5% equivalence band; the unchanged native control is **inconclusive**.
Every CPU/RSS resource gate passes. No disabled cell meets the unchanged useful
startup rule: ratio upper bound <=0.99 and saving lower bound >=5,000 ns.

The table retains all nineteen cells. Ratios are candidate/baseline paired medians
with cell-wise 95% bootstrap intervals (2,000 resamples, seed 41). Each CPU upper
bound must be <=1.10. Linux wait4 median paired RSS increases and their intervals
are zero in every cell, passing the <=10% baseline median +2 MiB gate; that is not
zero allocation or an imposed memory-cap result. Lifetimes include complete fixed
command execution and output capture, not readiness, requests or population p99.

| Profile / path / logging | Wall ratio [95% interval] | CPU ratio [95% interval] | Wall outcome | Resource qualified |
| --- | ---: | ---: | --- | --- |
| native / native / off | 1.0383 [1.0110, 1.0598] | 1.0404 [1.0162, 1.0734] | inconclusive | yes |
| full / memfd / off | 1.0064 [0.9861, 1.0266] | 0.9936 [0.9671, 1.0203] | equivalent | yes |
| full / memfd / json | 0.9906 [0.9734, 1.0181] | 1.0003 [0.9644, 1.0251] | equivalent | yes |
| full / memfd / debug | 0.9839 [0.9712, 0.9989] | 0.9872 [0.9655, 1.0132] | equivalent | yes |
| full / cold-cache / off | 0.9971 [0.9920, 1.0035] | 0.9970 [0.9816, 1.0089] | equivalent | yes |
| full / cold-cache / json | 0.9938 [0.9889, 0.9985] | 0.9913 [0.9797, 1.0054] | equivalent | yes |
| full / cold-cache / debug | 0.9919 [0.9852, 0.9975] | 0.9911 [0.9742, 1.0041] | equivalent | yes |
| full / warm-cache / off | 0.9861 [0.9756, 0.9946] | 0.9885 [0.9776, 0.9998] | equivalent | yes |
| full / warm-cache / json | 1.0087 [0.9994, 1.0179] | 1.0059 [0.9967, 1.0170] | equivalent | yes |
| full / warm-cache / debug | 1.0118 [1.0048, 1.0209] | 1.0128 [1.0043, 1.0239] | equivalent | yes |
| minimal / memfd / off | 1.0079 [0.9899, 1.0461] | 1.0174 [0.9881, 1.0471] | equivalent | yes |
| minimal / memfd / json | 0.9990 [0.9757, 1.0236] | 1.0055 [0.9744, 1.0375] | equivalent | yes |
| minimal / memfd / debug | 0.9887 [0.9739, 1.0136] | 0.9953 [0.9529, 1.0215] | equivalent | yes |
| minimal / cold-cache / off | 1.0043 [0.9966, 1.0102] | 1.0037 [0.9879, 1.0209] | equivalent | yes |
| minimal / cold-cache / json | 1.0014 [0.9933, 1.0079] | 0.9929 [0.9686, 1.0150] | equivalent | yes |
| minimal / cold-cache / debug | 0.9923 [0.9858, 0.9977] | 0.9925 [0.9787, 1.0119] | equivalent | yes |
| minimal / warm-cache / off | 0.9876 [0.9812, 0.9943] | 0.9891 [0.9844, 1.0002] | equivalent | yes |
| minimal / warm-cache / json | 1.0032 [0.9925, 1.0122] | 1.0033 [0.9947, 1.0101] | equivalent | yes |
| minimal / warm-cache / debug | 0.9966 [0.9872, 1.0033] | 0.9997 [0.9891, 1.0072] | equivalent | yes |

The native control executes identical bytes in both arms, yet its wall interval
extends to 1.0598. Its CPU interval passes, but does not remove the wall uncertainty.
This control is retained without subtracting an estimated drift correction or
selecting the eighteen favorable launcher cells as the adoption cohort.

The exact macro outcome is `correctness_pass=true`, `resource_pass=true`,
`all_cells_equivalent=false`, `complete_process_qualified=false`. The study
`study=snapshot` retained 12,160 planned and recorded attempts. The harness exited
nonzero with `snapshot timing/control equivalence failed or is inconclusive`;
its captured command output ends with `exit status 1`.

## Final disposition and evidence boundary

C2's local clock-removal attribution and resource results do not override the
failed all-cell/control equivalence requirement. Restore production and retain
this source/protocol/evidence as a rejected experiment. Neither C1 nor C2 is
qualified as a production startup optimization; no extra cohort was collected to
rescue either result. This outcome does not prove negligible overhead or a causal
candidate regression, and does not close the separate service-p99 question #230.

The final delivery retains the evaluation and behavioral compatibility coverage;
ordinary exact-head lint/security/race/coverage and native runtime gates apply to
that final source. Historical C2 performance observations belong only to frozen
`813d168` on this amd64 host. No native arm64 performance, service-p99 or useful
complete-process improvement claim follows. Issue closure requires the reviewed,
merged retention disposition; a later green CI run does not qualify C2.
