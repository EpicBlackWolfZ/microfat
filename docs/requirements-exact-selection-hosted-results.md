# Hosted requirements-exact selection: retain the simpler selector

Both separately retained native cohorts miss the unchanged adoption gates for
issue #153. Keep the existing requirements-based selector; the exact-mask
candidate remains test-only. The [hosted protocol](requirements-exact-selection-hosted.md)
was committed before timing. These are additional descriptive characterizations,
with no pooling, replacement attempts, trimmed samples or favorable-subset rescue.
The [local cohort](requirements-exact-selection-results.md) remains exploratory:
gaming was disclosed after collection and exact overlap was not recorded. Hosted
results do not remove that limitation or replace its preserved observations.

## Frozen source and collection

Measured source is `39a7fccc97061b588a9ef17bc104ebcbb328aa89` in
[run 40111512777, attempt 1](https://github.com/EpicBlackWolfZ/microfat/actions/runs/40111512777).
Each native runtime job collected exactly **304 selector rows** (19 fixtures ×
two implementations × eight repetitions) and **32 current-only process rows**
(four modes × eight). Both benchmark logs report `PASS`; all expected groups and
repetitions are present. No fetch or collection failures occurred. The six
intentional error-return fixtures remain in each table and are assessed separately
from the successful-fixture nonregression gate.

Both hosts ran native Linux with Go 1.27.3, `GOTOOLCHAIN=local`, parent
`GOMAXPROCS=1`, `-cpu=1`, a 100 ms benchmark budget and no race/coverage
instrumentation. The native amd64 host reported **AMD EPYC 9V45**; the native
arm64 host reported **Neoverse-N2**. Both exposed four logical CPUs with affinity
0–3 and kernel `6.17.0-1022-azure`. Each machine executes the selector's fixed
AMD64 and ARM64 inputs: ARM64 inputs on amd64 and AMD64 inputs on arm64 are
synthetic selector inputs, not native instruction-execution evidence for the
other architecture.

Before/after Git statuses are clean. All seven recorded protocol/source hashes
match the Git objects at the frozen source and the post-collection hash recheck.
Go, compiler and linker hashes are retained as remote observations; their binary
bytes were not supplied for independent hash replay. Process-relative and ancestor
cgroup observations were captured before timing: readable process/system-slice
CPU quota and memory ceilings are `max`; root quota/memory files are explicitly
unavailable. Those observations establish neither dedicated hardware, an idle VM
nor resource isolation. OS page cache and shared-host activity remain uncontrolled.

Original runtime artifacts were downloaded as ZIPs, retained intact locally and verified
against both the GitHub-reported SHA-256 and byte count before selective extraction:

- [Native amd64 artifact 12567266928](https://github.com/EpicBlackWolfZ/microfat/actions/runs/40111512777/artifacts/12567266928),
  `runtime-source-amd64-40111512777-1`, **68,828,942 bytes**;
  SHA-256 `8f4007d34c179abe86751dc31e224472a6861131eadeca6d1ce881e10e3122be`.
- [Native arm64 artifact 12566724515](https://github.com/EpicBlackWolfZ/microfat/actions/runs/40111512777/artifacts/12566724515),
  `runtime-source-arm64-40111512777-1`, **62,082,755 bytes**;
  SHA-256 `aa3cb2a3c1333de885750d3341402b5a6d130faeefce6cc993396ce76694c561`.

## Native amd64 cohort

Tables report median ns/op with minimum–maximum in brackets, rounded to whole
nanoseconds. Allocation counts were constant within every selector group.
The retained machine-readable summaries preserve exact medians/ranges, every
iteration count, B/op, allocs/op and every raw repetition.

| Fixture | Current ns/op [range] | Exact mask ns/op [range] | Current → mask allocs/op |
| --- | ---: | ---: | ---: |
| AMD64 sparse | 293 [288–301] | 334 [330–345] | 5 → 5 |
| AMD64 dense | 591 [589–598] | 640 [634–646] | 5 → 5 |
| AMD64 cap | 524 [518–530] | 577 [564–590] | 7 → 7 |
| AMD64 disabled | 645 [634–650] | 669 [662–684] | 8 → 8 |
| AMD64 forced | 353 [351–357] | 386 [371–395] | 3 → 3 |
| AMD64 normalization | 841 [835–904] | 926 [916–933] | 21 → 22 |
| AMD64 unknown (expected error) | 64 [63–65] | 112 [107–116] | 1 → 1 |
| AMD64 unavailable (expected error) | 75 [74–79] | 109 [107–111] | 1 → 1 |
| ARM64 sparse v8.0 | 397 [395–403] | 261 [259–264] | 3 → 3 |
| ARM64 sparse v8.2 | 1,309 [1,294–1,319] | 457 [454–472] | 8 → 5 |
| ARM64 sparse v9.0 without v8.9 | 2,584 [2,578–2,640] | 490 [483–494] | 13 → 5 |
| ARM64 dense, all 16 compatible | 19,671 [19,597–19,768] | 2,336 [2,312–2,440] | 60 → 5 |
| ARM64 cap | 2,632 [2,615–2,674] | 499 [494–502] | 13 → 5 |
| ARM64 disabled | 2,657 [2,648–2,669] | 519 [517–534] | 13 → 5 |
| ARM64 forced | 1,737 [1,709–1,790] | 451 [447–454] | 6 → 3 |
| ARM64 forced unavailable (expected error) | 3,070 [3,051–3,143] | 655 [650–663] | 15 → 7 |
| ARM64 forced incompatible (expected error) | 3,623 [3,604–3,654] | 326 [324–329] | 14 → 1 |
| ARM64 unknown (expected error) | 80 [79–81] | 191 [189–195] | 1 → 1 |
| ARM64 unavailable (expected error) | 1,315 [1,305–1,339] | 200 [198–202] | 6 → 1 |

Sparse ARM64-input median savings are **0.13615, 0.85115 and 2.09335 µs** for
v8.0, v8.2 and v9.0 without v8.9, respectively. All miss the **100 µs** threshold.
Relative savings are **34.290%, 65.048% and 81.028%**; v8.0 retains three
allocations instead of reducing them. The sparse-work gate therefore fails.

Five successful AMD64-input fixtures exceed the 5% median-time nonregression
ceiling: sparse **14.030%**, dense **8.187%**, cap **10.237%**, forced **9.111%**
and normalization **10.141%** slower in this cohort. Disabled is **3.807%** slower,
within that ceiling. Normalization also grows from **456 to 472 B/op** and from
**21 to 22 allocs/op**. The successful-representative gate fails. Dense ARM64-input
improvement and favorable error-return paths cannot override either failed gate;
the unknown/error rows also retain their observed slowdowns.

## Native arm64 cohort

The same fixed fixtures and all eight repetitions are retained independently.
No result is pooled with native amd64 or the local workstation cohort.

| Fixture | Current ns/op [range] | Exact mask ns/op [range] | Current → mask allocs/op |
| --- | ---: | ---: | ---: |
| AMD64 sparse | 538 [532–539] | 616 [612–624] | 5 → 5 |
| AMD64 dense | 1,005 [995–1,013] | 1,150 [1,131–1,164] | 5 → 5 |
| AMD64 cap | 924 [919–1,000] | 1,084 [1,055–1,094] | 7 → 7 |
| AMD64 disabled | 1,064 [1,057–1,070] | 1,201 [1,193–1,214] | 8 → 8 |
| AMD64 forced | 582 [580–586] | 709 [705–711] | 3 → 3 |
| AMD64 normalization | 1,554 [1,536–1,717] | 1,765 [1,754–1,841] | 21 → 22 |
| AMD64 unknown (expected error) | 115 [108–116] | 179 [178–181] | 1 → 1 |
| AMD64 unavailable (expected error) | 131 [130–133] | 184 [182–186] | 1 → 1 |
| ARM64 sparse v8.0 | 643 [632–651] | 516 [511–522] | 3 → 3 |
| ARM64 sparse v8.2 | 1,978 [1,955–2,013] | 819 [815–851] | 8 → 5 |
| ARM64 sparse v9.0 without v8.9 | 3,857 [3,843–3,923] | 894 [883–924] | 13 → 5 |
| ARM64 dense, all 16 compatible | 28,875 [28,558–31,166] | 4,050 [3,951–4,326] | 60 → 5 |
| ARM64 cap | 3,936 [3,918–3,964] | 938 [935–944] | 13 → 5 |
| ARM64 disabled | 3,961 [3,936–4,144] | 968 [960–1,000] | 13 → 5 |
| ARM64 forced | 2,532 [2,469–2,550] | 798 [796–801] | 6 → 3 |
| ARM64 forced unavailable (expected error) | 4,531 [4,487–4,586] | 1,178 [1,173–1,237] | 15 → 7 |
| ARM64 forced incompatible (expected error) | 5,590 [5,409–5,803] | 582 [580–585] | 14 → 1 |
| ARM64 unknown (expected error) | 130 [130–132] | 326 [325–328] | 1 → 1 |
| ARM64 unavailable (expected error) | 2,044 [2,035–2,099] | 328 [327–331] | 6 → 1 |

Sparse ARM64-input median savings are **0.1265, 1.159 and 2.96225 µs** for v8.0,
v8.2 and v9.0 without v8.9. All miss the **100 µs** threshold. Relative savings
are **19.689%, 58.609% and 76.812%**; v8.0 also misses the **25%** requirement
and retains three allocations. The sparse-work gate fails independently.

All six successful AMD64-input fixtures exceed the 5% median-time ceiling:
sparse **14.505%**, dense **14.435%**, cap **17.310%**, disabled **12.876%**,
forced **21.874%** and normalization **13.578%** slower in these medians.
Normalization again grows from **456 to 472 B/op** and **21 to 22 allocs/op**.
The successful-representative gate fails independently. Error-return fixtures
remain reported separately and cannot rescue the successful-fixture gate.

## Separate current-only process context

Every timed invocation starts a fresh native process and includes
`exec.Cmd.Output`, process creation, actual launcher work where applicable and
payload exit. Each result passed the exact payload-marker, empty-stderr and
successful-exit checks. The existing golden fixture uses the full stub, balanced
Zstd, no dictionary and the default format; its variants are built for the
actual native architecture. Cold-cache removal and warm-cache preparation occur
outside timing and refer to the extracted-payload cache, with OS page cache
uncontrolled. The direct-native control executes the baseline-tier payload.

Each architecture is a separate cohort; each table cell below summarizes eight
repetitions for one mode. Values are median ms [minimum–maximum], rounded to
three decimal places. The arm64 cold-cache maximum of **12.517 ms** remains included.

| Current-only process mode | Native amd64 ms [range] | Native arm64 ms [range] |
| --- | ---: | ---: |
| Direct native | 0.825 [0.818–0.841] | 1.201 [1.178–1.246] |
| Explicit memfd | 4.531 [4.488–4.594] | 5.983 [5.877–6.258] |
| Explicit cold cache | 6.967 [6.834–7.130] | 8.753 [8.439–12.517] |
| Explicit warm cache | 2.702 [2.668–2.728] | 3.498 [3.437–3.635] |

No candidate launcher was executed. These current-only lifetimes establish no
mask-attributable launch improvement or service-request p99. Parent counters
are not child allocations or RSS. Launcher children retain production autotuning;
direct children use runtime defaults. These descriptive medians and ranges are
not independent population samples or confidence bounds.

## Retention and qualification boundary

The [hosted evidence archive](benchmarks/selection-153/hosted-40111512777-1.tar.gz)
retains both verified study subsets and runtime artifact metadata, source/toolchain/
host observations, all samples and summaries, the replay script and independent
audits. The large original runtime ZIPs remain retained locally; they are not
embedded in this repository archive. The small original Homebrew failure ZIP,
its log/metadata and the initial run conclusions are included. Archive SHA-256 is
`df735d592372d1fdf0aa501c0ddcbb3a72e14d5ceded158eb21ba97b9102000b`;
the adjacent [SHA256SUMS](benchmarks/selection-153/SHA256SUMS) verifies retained
archive integrity. Numerical replay reads the raw files and collects no new
measurements. The two architectures retain separate dispositions and neither can
rescue the exploratory local promotion decision.

Both [native amd64 runtime qualification](https://github.com/EpicBlackWolfZ/microfat/actions/runs/40111512777/job/120926830934)
and [native arm64 runtime qualification](https://github.com/EpicBlackWolfZ/microfat/actions/runs/40111512777/job/120926830999)
passed at the measured source. The overall initial run failed in
[Native Homebrew (arm64)](https://github.com/EpicBlackWolfZ/microfat/actions/runs/40111512777/job/120927804210)
and its dependent `CI Complete` gate: the previously installed v0.3.0 binary's
`update --check --version 0.2.5` request received HTTP 403 while downloading a
release. Updater code and that Homebrew test were unchanged in this PR. The
[Homebrew failure artifact 12566924162](https://github.com/EpicBlackWolfZ/microfat/actions/runs/40111512777/artifacts/12566924162)
is preserved with SHA-256
`1a3fc99a336aa1e43217a8013088ac1eb5bbf87fa63921e99dc20d8808a29928`.
That separate failure does not erase the complete native timing cohorts or imply
that the overall measured-source workflow passed.

Final qualification belongs to [PR #400](https://github.com/EpicBlackWolfZ/microfat/pull/400)
after the temporary timing step is removed. Its exact source and hosted checks
must be assessed separately; this report does not claim future CI is green or
authorize merge or release.
