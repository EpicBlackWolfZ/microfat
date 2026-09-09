# Production-core implementation plan

Planning baseline: `main` at `0e7f296c1fae4d5f308eee9d62abdb2638d3ce58`, 2026-09-09.
Read with the [roadmap](roadmap.md) and linked GitHub issue acceptance criteria.
This plan schedules implementation; no runtime fixes or new test results are implied.

## Evidence and working state

The supplied assessment reviewed the same commit now checked out as the base. Source inspection in
this planning task confirmed the JSON non-progress path, rank-based selection, blocking cache openers,
and CI command drift. The reviewer's FIFO and mutable-inode experiments are supporting evidence,
not experiments rerun here. The privileged-execution finding is conditional on deployment;
no privileged exploit was performed. Passing existing CI does not establish these missing contracts.

Existing benchmark work was stashed from `feat/benchmark-workloads-and-load` before updating main.
Keep that stash intact. Resume it on its original branch when working on #88/#89, reconcile against
merged #87 and the revised requirements, and review its toolchain changes without downgrading Go.
Do not apply it to a safety-fix branch. Inventory existing `benchmarks/env`, `runner`, `schema`, and
`analysis` implementations before introducing overlapping packages proposed by older issue bodies.

## Delivery sequence and dependencies

Each row is a focused PR unless its scope justifies several independently reviewable PRs.
Estimates are relative: S is narrow, M spans a few components, L needs design and integration work.
They are not calendar commitments; runner availability and review can dominate elapsed time.

| Order | Work | Depends on | Size | Completion evidence |
| --- | --- | --- | --- | --- |
| 1 | #175 CI command parity; #176 workflow permissions/pins | Existing CI inventory | M | Required checks cover intended packages/profiles; release verifier has read-only permissions |
| 2 | #170 parser termination; #171 privilege rejection; #173 FIFO handling | None; run regressions even before CI parity merges | S–M each | Deadline-bound regressions and no-side-effect rejection |
| 3 | #172 ARM64 dispatch | Explicit feature compatibility contract | M | Detection-to-selection and independent subset-oracle tests |
| 4 | #174 cache validation and minimum #85 threat boundary | Document same-UID policy | M | Descriptor ownership/mode checks and accurate guarantee statements |
| 5 | #151 bounded input pipeline | Inventory pack/builder/sample readers | M | Boundary and changing-input tests without unbounded allocations |
| 6 | #152 memory accounting; #177 documentation | Mode/trust policy from #174; bounded extraction assumptions | L / S | Cgroup extraction/post-exec evidence and validated external CLI example |
| 7 | #88–#90 workloads, load and controls | Reuse #87; reconcile stashed work | L | Stable workload contracts, isolated generator, recorded controls |
| 8 | #91 comparisons; #93 reporting; #92 performance CI | Workload/control contracts; shared CI | M–L each | Reproducible raw paired trials, reports and tiered regression checks |
| 9 | #85, #150, #154, #86 | Safety patch contracts | M–L each | Threat model, safe transformation failures, ABI checks, external verification |
| 10 | #44, #40, #155, #157, #158, #15 | Relevant runtime/lifecycle contracts | M each | Full/minimal execution, mount/SBOM and documentation validation |
| 11 | #153, #41–#43, #156 | Correct dispatch and published baseline | M each | Measured benefit with unchanged safety/compatibility |

Rows within a release are not a requirement to batch fixes. Merge and release verified urgent changes
independently; measurement and optimization do not gate their shipment. Before beginning any issue,
check current main and open PRs to avoid reimplementing already delivered behavior.

## 1. Make CI enforce the actual contracts (#175, #176)

Affected: `Makefile`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`, remaining workflows.

1. Extract one authoritative test/coverage invocation into Makefile targets or a shared script.
   Preserve JUnit output and existing required check names. Include `./benchmarks/...` in coverage
   and run the minimal-tag stub suite as well as the default suite, both with race detection.
2. Document coverage scope and profile accounting. Retain the repository's >=95% overall gate;
   do not merge incompatible coverage profiles or exclude new packages to manufacture a pass.
3. Temporarily inject a failing benchmark test and minimal-only test to prove each reaches CI's
   shared command, then remove the injections. Test independent failure propagation, not command text.
4. Inventory action references and intended versions, resolve each to an upstream full commit SHA,
   retain version comments, and check automated updates still work. Review nested/composite actions.
5. Set workflow defaults read-only. Give write/OIDC permissions only to specific publisher/reporting
   jobs that need them; release verification must not inherit publishing rights. Review fork behavior.

Gate: workflow validation, shared test commands and a non-publishing release check pass. Do not trigger
a real release or tag to validate these changes. Keep Go 1.27.1 and existing toolchain definitions.

## 2. Close parser and launcher-entry failure paths (#170, #171)

Affected: `internal/format/format.go`, format fuzz/tests, `cmd/microfat-stub/main.go`, profile entry tests.

For JSON, first add a timeout-isolated regression through real inspection/verification with a valid
trailer digest. Then make skipped values validate primitive grammar and separators. Every successful
value parse must advance; mismatched closing delimiters must fail immediately. Thread a named depth
limit through every recursive route, including unknown fields. Test depth at/over the limit, malformed
numbers/literals, trailing commas, missing separators, EOF and valid legacy manifests. Differential
fuzz against `encoding/json` for syntax while separately testing the stricter manifest schema. Avoid
asserting identical schema acceptance where the standard parser has no schema knowledge.

For privilege handling, introduce a small testable entry guard before meta-command routing, cache
resolution, environment-based policy or writes. Cover UID and GID mismatches and define Linux secure
execution/file-capability handling, including equal-ID cases. Specify ordinary root behavior separately.
Both full and minimal profiles must enforce the same rule. Inject identity/secure-execution probes in
unit tests to verify ordering and absence of side effects; use an isolated privileged fixture for real
setuid/setgid/capability behavior where available. Record skips explicitly. Remove comments suggesting
supported setuid semantics. No fallback may bypass the entry guard.

## 3. Preserve CPU capabilities through dispatch (#172, then #153)

Affected: `internal/microarch/microarch.go`, architecture fixtures/property tests and detection callers.

1. Define a host capability representation from detected features. For a variant, compatibility means
   its complete required feature set is a subset of available host features.
2. Carry that representation from detection to selection. Keep ranking only for preference after
   compatibility filtering; do not reconstruct host capabilities as a numeric prefix.
3. Inventory `BestMatchingVariant`, `BestMatchingVariantFor`, `SelectVariantWithPolicy` and all callers.
   Define conservative behavior for level-only APIs using the level's guaranteed requirements, with
   no assumption that an ARM64 v9 level supports every numerically lower v8 level.
4. Apply force/max/denylist/downclock policy without silently introducing an ISA-unsafe selection.
   Record intended behavior for unknown levels, empty sets and no compatible candidates.
5. Test a v9.0-capable host lacking v8.9 requirements with both variants present; combine detection
   fixtures with real selection. Generate sparse capability sets and compare with an independent
   requirement-subset oracle. Retain AMD64 behavior and test both architecture branches.

Gate: compatibility composition passes on simulated feature sets plus available native ARM64/AMD64
smoke runs; emulation is not evidence of every physical CPU feature combination. Only afterward should
#153 replace internals with bitsets, differential-test against the contract, and measure allocations.

## 4. Harden cache opens and state the trust boundary (#173, #174, #85)

Affected: `internal/cache/cache_unix.go`, cache tests, `cmd/microfat-stub/exec_linux.go`,
`internal/pack` prewarm/verify paths, `SECURITY.md` and cache comments.

Add `O_NONBLOCK` to both `OpenFileFunc` and `OpenFileAtFunc`, retaining no-follow and close-on-exec.
Validate type, expected owner, size, allowed permission bits and mandatory SHA-256 from the descriptor.
Execution must use that descriptor. Preserve cleanup semantics: invalid special files must not be
blindly unlinked as corrupt regular cache entries. Test descriptor closure on all failures.

Use subprocess deadlines for FIFO-without-writer fixtures across execution, prewarm and verification.
Cover path and directory-relative APIs, symlinks, directories, mode/owner mismatch and normal warm hits.
Separate tests of pathname replacement from a controlled demonstration of same-inode content mutation.

Proposed baseline decision: cache mode trusts same-UID writers. Owner/mode checks do not block the
owner or previously opened writable descriptors. Sealed memfd provides stronger content immutability;
a deployment requiring that guarantee must fail closed when it is unavailable. Record the agreed
boundary in #85 before shipping claims or changing #44 defaults. A hostile same-UID cache guarantee
would require a separate audited design, not another hash or permission check.

## 5. Bound packaging and extraction memory (#151, #152)

Affected: `internal/pack/pack.go` (`sampleVariantPayloads`, `writeVariantPayload`, `ValidateELFBinary`),
`internal/builder`, decoder limits, `cmd/microfat-stub/exec_linux.go`, `internal/cgroup`, `runtimeinit`.

For inputs, open once, inspect the descriptor, and enforce a bounded read even after a passing size
check. Reject more than `MaxPayloadSize` by reading at most limit+1; reject empty or invalid ELF inputs.
Audit dictionary sampling, aggregate sample budgets, builder paths and other whole-file reads. Avoid
validating one pathname inode and reopening another for consumption. Test exact/over bounds, files
that grow/shrink, read errors and cleanup. Use injected small test ceilings where a GiB fixture would
only create resource pressure; retain a real format-limit contract test.

For execution, write down two budgets before changing arithmetic:

- Extraction peak: memfd pages plus decoder/dictionary state, buffers, launcher and reserved headroom.
- Post-exec runtime: effective cgroup limit minus applicable retained memfd storage and non-Go reserve,
  then the documented Go-runtime tuning policy.

Use checked arithmetic for tiny limits, overflow and unlimited/unknown values. Ensure launcher and
`runtimeinit` deduct applicable storage once, define internal metadata provenance/precedence, and
preserve explicit user overrides. In auto mode, fallback must respect the cache threat boundary;
explicit memfd must not silently become cache. Cache page pressure and decoder memory still matter.

Validate with cgroup v1/v2 runs where available: collect peak process memory, cgroup memory/shmem and
post-exec usage for small/large payloads, dictionaries and codecs in both execution modes. Record skips
and uncertainty. These measurements characterize risk; they cannot establish categorical OOM immunity.

## 6. Correct user-facing contracts (#177, #155, #158)

Replace the README external `internal/pack` import with a supported CLI integration, then execute the
example from outside the module. A public Go API is a separate design decision. Audit README and docs
for OOM prevention, guaranteed quota behavior, universally optimal selection and zero startup claims.
Use qualified wording and link numerical claims to evidence with named baselines.

Document `os.Executable()` under memfd, `runtimeinit.Executable()` and original-executable metadata,
including when cache mode meets legacy pathname needs. Provide artifact/profile selection guidance.
Announce any v1 deprecation with a compatibility period; do not remove parsing to bypass #170.

## 7. Build credible performance evidence (#88–#93)

1. Reuse #87's schema/environment/runner/statistics; reconcile the preserved workload work before
   extending it. Pin workload/toolchain/dependencies and record hashes. Use identical source and
   handlers across native and fat variants, with correctness checks for workload responses.
2. Run load generation in a separate process. Validate the selected engine's actual output, pacing
   and latency semantics against its pinned version rather than assuming a histogram implementation
   or coordinated-omission guarantee from an old issue description. Record version and all flags.
3. Distinguish controls (affinity/cgroups), observations (frequency, memory, throttling) and warnings.
   Record unavailable controls; uncontrolled runs cannot substantiate precise performance claims.
4. Compare generic native, specialized native, fat/memfd and fat/cache under identical tuning.
   Separately test tuning on/off. Distinguish cold extraction, cold process startup, warm cache and
   sustained workload behavior. Record cache state rather than silently mixing it.
5. Randomize or counterbalance within paired trial blocks; preserve schedule, seed, warmup, repeats,
   measurement windows, failed trials and exclusion reasons. Fixed repeated A/B order is insufficient.
6. Apply paired differences and deterministic bootstrap analysis; report uncertainty and practical
   magnitude, not significance alone. Publish throughput, tail latency, startup, binary sizes, peak
   process/cgroup memory and hardware/kernel/toolchain metadata alongside immutable raw trials.
7. Add coarse PR smoke checks, broader nightly runs and authoritative dedicated-host release evidence.
   Keep noisy hosted-runner changes separate from controlled regression conclusions. Publish step
   summaries without automatic issue/comment spam.

Gate: reports can be regenerated from raw inputs with documented commands/checksums; independent
synthetic statistical fixtures validate calculations and missing/failed trials cannot disappear.

## 8. Deliver the focused production follow-up (v0.3.0)

For #150, design a shared transformation primitive used by CLI and launcher trim/optimize. Define
source/destination policy for ownership, mode, ACLs, xattrs, capabilities, hard links and signatures.
Retain atomic replacement rather than updating linked executable inodes in place. Refuse unsafe cases
by default; explicit break/strip policies must be named and tested. Do not blindly preserve capability
attributes or stale signatures after changing executable bytes. Specify re-signing outside transformation.
Test metadata application ordering, destination collisions, permission failures and unsupported xattrs;
on every failure the original must remain usable and temporary files must be cleaned up.

For #154, inspect `PT_INTERP` and version requirements across variants, covering static/dynamic and
musl/glibc mixtures and intentionally mixed ABI overrides. CPU compatibility alone is not a loader
compatibility guarantee. Test fixtures should not depend on the host distribution's current libc.

For #86, document an independently trusted external verifier, publisher identity/issuer or pinned key,
signed-checksum verification and exact artifact hashing before first execution. Test wrong identity,
missing signature and modified bytes. Define offline/transparency and version policy. Do not introduce
a format extension or claim SLSA Level 3 solely from signatures. Launcher self-verification cannot
establish trust before the launcher runs.

Then deliver #44 cache-first policy, #40 profile packaging, #157 mount/SBOM verification and #15 runtime
observability. Test defaults and override precedence, mandatory hashes, descriptor binding, memfd seals
and failure paths across profiles. Existing partial behavior should be retained and completed, not rebuilt.

## Verification and release gates

Every code PR uses Go 1.27.1, table-driven contract tests, safe parallel tests, checked errors and named
limits. Run focused tests while iterating, then the relevant Makefile race, lint, coverage, E2E, fuzz and
fault-injection targets; run dependency/vulnerability/build checks as required by CI and release scope.
The >=95% coverage requirement remains, but independent composition and failure tests are mandatory
evidence even if a coverage number already passes. Never lower the toolchain or threshold to unblock CI.

For execution/format changes, enumerate all supported cells:

| Dimension | Required coverage |
| --- | --- |
| Format | v1 and v2, valid/corrupt/truncated inputs, dictionary present/absent where supported |
| Stub | full and minimal |
| Codec | none, zstd, lz4; applicable dictionary settings |
| Execution | memfd, cache and supported auto/fallback behavior; cold and warm |
| CPU | AMD64 and ARM64 requirement branches, compatible and incompatible sparse variant sets |
| Environment | cgroup v1/v2/absent, restricted memfd/sealing, read-only filesystem and mount cases |

Record unsupported combinations and why rather than calling them passed. For optimizations or format
changes run the full supported combinatorial benchmark matrix, verify payload reconstruction and
execution correctness, and separate microbenchmarks from end-to-end cold startup. For privileged and
hardware tests, an unavailable runner is an explicit release-validation gap requiring maintainer review.

Each PR references its issue with `Resolves #<id>` only when its acceptance criteria are complete;
otherwise use `Refs`. Monitor required GitHub checks to completion. Maintainers merge manually; issues
remain open until their implementation PR merges. Release tags and publishing remain maintainer actions.
A rollback keeps the prior artifact available and reverts the focused change; never roll back by
weakening required hashes, seals or compatibility checks. Update roadmap milestones at release cut.

## Decisions to record before dependent implementation

- #171: secure-execution/file-capability detection and ordinary-root policy.
- #174/#85: same-UID writer scope and strict memfd behavior when cache is insufficient.
- #172: conservative level-only API semantics and force/max-level compatibility behavior.
- #152: reserves, decoder estimates, internal metadata precedence and explicit-mode fallback policy.
- #150: unsupported metadata handling, explicit stripping, hard-link and re-signing policies.
- #86: publisher identity/issuer or key trust roots and offline/version policy.

Resolve these in the corresponding issue/design PR with tests demonstrating the selected contract.
They do not block independent parser, FIFO or CI fixes and are not reasons to wait for the research roadmap.
