# Production roadmap

Updated 2026-10-23: v0.3.1 passed source/candidate qualification and hosted evidence gates and
was published immutable. Independently reviewed public archive and clean native bootstrap checks
passed, the authenticated v0.3.1 Homebrew recipe is merged, and final combined public installer/tap
acceptance passed on both native architectures.
GitHub issues define remaining scope,
dependencies and acceptance criteria. Milestones group deliverable outcomes; they are not dates.

## Current baseline

[v0.3.1](https://github.com/EpicBlackWolfZ/microfat/releases/tag/v0.3.1) is the latest published
release, published on 2026-10-23 at 05:04:02 UTC as immutable release `421362795` from source
`94cb9f6ad26c14931ea28e53a8969f09790eff80`. Its
[acceptance record](releases/v0.3.1.md#release-acceptance) separates source CI, signed-candidate
qualification, hosted measurements, publication and public installer/tap results.

[v0.3.0](https://github.com/EpicBlackWolfZ/microfat/releases/tag/v0.3.0) was published on
2026-10-13 at 05:25:54 UTC as immutable release `411193467`.
Its source is `d3883d59674eb32351717f6d28744862d1cc45c9`; its
[final main CI](https://github.com/EpicBlackWolfZ/microfat/actions/runs/38320256153/attempts/4) passed: the full same-source
attempt 3 supplied 23 successful checks, and the bounded retry completed native arm64 installer and `CI Complete`
in attempt 4. Failed earlier attempts remain available.
Its [then-private signed candidate](https://github.com/EpicBlackWolfZ/microfat/actions/runs/38334917557/attempts/1)
passed native installer and runtime qualification on amd64 and arm64.
The authenticated recipe is merged in [tap PR #4](https://github.com/EpicBlackWolfZ/homebrew-tap/pull/4).
Verified public installer/tap acceptance passed in the same fresh
[38376089070, attempt 2](https://github.com/EpicBlackWolfZ/microfat/actions/runs/38376089070/attempts/2)
cohort, using qualification source `5f09b0185b1c51daa54a641d7241ac111c340555` against the immutable
release source above. See its
[release notes and acceptance record](releases/v0.3.0.md#release-acceptance).

[v0.2.5](https://github.com/EpicBlackWolfZ/microfat/releases/tag/v0.2.5) was published on
2026-09-21. Both v0.2.4 and v0.2.5 milestones are closed.

[v0.2.3](https://github.com/EpicBlackWolfZ/microfat/releases/tag/v0.2.3) was published on
2026-09-16. It includes the safety and hosted-measurement foundation, per-architecture fat CLI
archives with companion full/minimal stubs, and release integrity/SBOM validation. Format v1
is deprecated; support must follow the documented policy rather than disappear incidentally.
See [release artifacts](release-artifacts.md) and [benchmark evidence](benchmarks/README.md).
The historical release-note verification instructions were corrected in
[#197](https://github.com/EpicBlackWolfZ/microfat/issues/197), using the published checksum signature.
The [v0.2.4 release notes](releases/v0.2.4.md) describe the subsequent correctness fixes.
The [v0.2.5 release notes](releases/v0.2.5.md) describe the Go/Task tooling migration, modern
SBOMs, staged CI and benchmark corrections.

Before v0.2.5, [PR #192](https://github.com/EpicBlackWolfZ/microfat/pull/192) added developer
workflow/profiling/leak checks and [PR #194](https://github.com/EpicBlackWolfZ/microfat/pull/194)
added ShellCheck. They are merged into `main`; they are not new work to implement again.

## Delivery sequence

| Milestone | Outcome | What must not delay it |
| --- | --- | --- |
| [v0.2.4](https://github.com/EpicBlackWolfZ/microfat/milestone/12) | Correct existing runtime, packaging, verification and documentation defects | Tooling migrations and new distribution features |
| [v0.2.5](https://github.com/EpicBlackWolfZ/microfat/milestone/13) | Consolidate tooling, release trust and CI without losing validation | Installer/updater/tap features or speculative attestations |
| [v0.3.0](https://github.com/EpicBlackWolfZ/microfat/milestone/2) | Coherent installation and executable lifecycle contracts | Native macOS support and new compression formats |
| [v0.3.1](releases/v0.3.1.md) | Installer/dictionary corrections and dependency/toolchain updates | New binary formats and dedicated-hardware certification |
| [v0.4.0](https://github.com/EpicBlackWolfZ/microfat/milestone/3) | Observability and performance improvements supported by evidence | Arbitrary latency targets and dedicated-hardware availability |

Urgent, independently validated corrections can ship earlier in the patch stream. A tooling
migration must not hold a known runtime fix. Work can proceed independently where dependencies
allow, but overlapping refactors follow correctness fixes and retain their regressions.

## v0.2.4: correctness and compatibility

The v0.2.4 patch fixes existing behavior using the current build/release tools. The linked issues
record the acceptance criteria and regression evidence; later milestones retain the broader work.

| Scope | Issues |
| --- | --- |
| Kernel-held image identity, executable memfd, safe GC and complete AMD64 checks | [#206](https://github.com/EpicBlackWolfZ/microfat/issues/206), [#195](https://github.com/EpicBlackWolfZ/microfat/issues/195), [#208](https://github.com/EpicBlackWolfZ/microfat/issues/208), [#209](https://github.com/EpicBlackWolfZ/microfat/issues/209) |
| Integrity exit status, nonblocking inputs, executable ELF validation and read-only cache checks | [#207](https://github.com/EpicBlackWolfZ/microfat/issues/207), [#211](https://github.com/EpicBlackWolfZ/microfat/issues/211), [#216](https://github.com/EpicBlackWolfZ/microfat/issues/216), [#214](https://github.com/EpicBlackWolfZ/microfat/issues/214) |
| Manifest PGO paths and explicit dictionary-size precedence | [#212](https://github.com/EpicBlackWolfZ/microfat/issues/212), [#213](https://github.com/EpicBlackWolfZ/microfat/issues/213) |
| Observed benchmark duration and completeness | [#210](https://github.com/EpicBlackWolfZ/microfat/issues/210) |
| Existing installer regression, post-pack stripping guidance and signature instructions | [#215](https://github.com/EpicBlackWolfZ/microfat/issues/215), [#196](https://github.com/EpicBlackWolfZ/microfat/issues/196), [#197](https://github.com/EpicBlackWolfZ/microfat/issues/197) |
| Executable-path, stale-hint and re-exec guidance after the image-identity fix | [#158](https://github.com/EpicBlackWolfZ/microfat/issues/158) |

Exit: targeted regressions and applicable full/minimal, memfd/cache and architecture checks pass.
Benchmark qualification examines actual observations before accepting new release evidence.
Documentation describes verified behavior. Passing a general test suite alone does not resolve a
reproduced defect outside its assertions. Task, Python removal and SBOM migration are not prerequisites.

## v0.2.5: tooling and release trust

The published release consolidates Go/Task tooling and release trust. The linked issues record
acceptance evidence; subsequent releases must retain the signed-asset and benchmark gates below.

| Scope | Issues |
| --- | --- |
| Complete the existing threat model, then external verification policy | [#85](https://github.com/EpicBlackWolfZ/microfat/issues/85), [#86](https://github.com/EpicBlackWolfZ/microfat/issues/86) |
| Replace repository Python helpers with Go, then migrate Make callers to Task | [#198](https://github.com/EpicBlackWolfZ/microfat/issues/198), [#199](https://github.com/EpicBlackWolfZ/microfat/issues/199) |
| Prove and migrate the cdxgen-based modern SBOM path | [#202](https://github.com/EpicBlackWolfZ/microfat/issues/202) |
| Stage all PR work behind fast checks and a fail-closed final result | [#200](https://github.com/EpicBlackWolfZ/microfat/issues/200) |
| Resolve actionable quality findings and scan the integrated candidate | [#201](https://github.com/EpicBlackWolfZ/microfat/issues/201) |
| Correct memfd benchmark helpers, user-writable installation and ignored-file formatting | [#227](https://github.com/EpicBlackWolfZ/microfat/issues/227), [#228](https://github.com/EpicBlackWolfZ/microfat/issues/228), [#229](https://github.com/EpicBlackWolfZ/microfat/issues/229) |
| Bound executable-fixture retries and report nested CPU limits correctly | [#245](https://github.com/EpicBlackWolfZ/microfat/issues/245), [#249](https://github.com/EpicBlackWolfZ/microfat/issues/249) |

Go helpers and Task callers now preserve the existing contracts without repository Python or Make.
The cdxgen path validates CycloneDX 1.7 / SPDX 3.0.1 schemas and archive/variant inventory fidelity
without Python/BLINT. Staged CI includes a required fail-closed completion check. Quality review
follows the integrated migrations; exact function exceptions preserve readable validation and
resource ownership rather than moving branches merely to improve a metric.

Exit: active workflows work without repository Python/Make dependencies; exact coverage, benchmark
replay, verified downloads, process cleanup, developer workflows and meaningful validation remain
intact. Modern SBOMs pass schema and independent semantic checks on both architectures. Keep historical
schema support explicit. Signed checksums and draft-first publication remain mandatory. CDXA, new
attestations and historical asset replacement are outside this milestone.

## v0.3.0: installation and lifecycle contracts

### Integrated v0.3.0 source

| Scope | Issues |
| --- | --- |
| Shared atomic trim/optimize identity and metadata policy | [#150](https://github.com/EpicBlackWolfZ/microfat/issues/150) |
| Full/minimal stub selection through direct pack, manifest pack and pgo-pack | [#40](https://github.com/EpicBlackWolfZ/microfat/issues/40) |
| Bounded declared dynamic-linker, dependency and ABI consistency checks | [#154](https://github.com/EpicBlackWolfZ/microfat/issues/154) |
| Builder target validation, memory-budget/byte-size corrections and compression precedence | [#252](https://github.com/EpicBlackWolfZ/microfat/issues/252), [#253](https://github.com/EpicBlackWolfZ/microfat/issues/253), [#255](https://github.com/EpicBlackWolfZ/microfat/issues/255), [#254](https://github.com/EpicBlackWolfZ/microfat/issues/254) |
| Codec lock correction, executable-path whitespace and mode-aware doctor diagnostics | [#257](https://github.com/EpicBlackWolfZ/microfat/issues/257), [#256](https://github.com/EpicBlackWolfZ/microfat/issues/256), [#259](https://github.com/EpicBlackWolfZ/microfat/issues/259) |
| Codecov reporting while retaining the authoritative exact coverage gate | [#262](https://github.com/EpicBlackWolfZ/microfat/issues/262) |
| Explicit verified update checks and generation-based self-update | [#204](https://github.com/EpicBlackWolfZ/microfat/issues/204) |
| Source-built native mount, executable-hint and mounted-generation qualification | [#157](https://github.com/EpicBlackWolfZ/microfat/issues/157) |
| Verified installer source implementation, generation transactions and ownership | [#203](https://github.com/EpicBlackWolfZ/microfat/issues/203) |
| Authenticated Homebrew cask generator and native lifecycle qualification | [#205](https://github.com/EpicBlackWolfZ/microfat/issues/205) |
| Source-built real-process concurrency/policy qualification and signed-candidate release gate | [#260](https://github.com/EpicBlackWolfZ/microfat/issues/260) |
| F-only ARM64 QEMU descriptor-execution qualification | [#231](https://github.com/EpicBlackWolfZ/microfat/issues/231) |
| Independent native Go CPU adaptation with explicit tuning precedence | [#258](https://github.com/EpicBlackWolfZ/microfat/issues/258) |
| Cache-first auto dispatch with descriptor verification and non-mutating misses | [#44](https://github.com/EpicBlackWolfZ/microfat/issues/44) |
| Exact cgroup paths, independent v1 mounts and finite zero memory boundaries | [#340](https://github.com/EpicBlackWolfZ/microfat/issues/340), [#341](https://github.com/EpicBlackWolfZ/microfat/issues/341), [#342](https://github.com/EpicBlackWolfZ/microfat/issues/342) |
| Independent Linux release validation, canonical launcher discovery and exact cache paths | [#343](https://github.com/EpicBlackWolfZ/microfat/issues/343), [#344](https://github.com/EpicBlackWolfZ/microfat/issues/344), [#348](https://github.com/EpicBlackWolfZ/microfat/issues/348) |
| Bounded benchmark trials, complete selected-CPU evidence and effective-limit density | [#345](https://github.com/EpicBlackWolfZ/microfat/issues/345), [#346](https://github.com/EpicBlackWolfZ/microfat/issues/346), [#347](https://github.com/EpicBlackWolfZ/microfat/issues/347) |

These source implementations are merged into the final integrated revision. Issues #203, #205 and
#260 have completed the acceptance requirements below; final closure follows the reviewed documentation merge.
Installer source landed in
[#276](https://github.com/EpicBlackWolfZ/microfat/pull/276), Homebrew tooling in
[#282](https://github.com/EpicBlackWolfZ/microfat/pull/282), and runtime qualification in
[#283](https://github.com/EpicBlackWolfZ/microfat/pull/283). ABI metadata consistency does not
prove that a target system provides a compatible loader or libc. The #203 implementation supplies
the [installation contract](design/installation-discovery-contract.md), authenticated bootstrap,
generation transactions and native qualification jobs. Published signed helpers and actual public-path
acceptance now establish its release availability; source and snapshot results retain separate scope.

### Release acceptance

The acceptance issues track the integrated release and its public distribution. The
[release acceptance record](releases/v0.3.0.md#release-acceptance) binds the evidence to its source,
producer attempt, signed inventory and reviewed tap recipe.
Native source qualification, unsigned packaging snapshots, authenticated candidate qualification,
and public availability establish different facts and must retain separate evidence.

| Scope | Issue | Acceptance evidence and completion boundary |
| --- | --- | --- |
| Verified user installer | [#203](https://github.com/EpicBlackWolfZ/microfat/issues/203) | Signed-candidate installer qualification passed with eight detection and four update observations per native architecture. Independently verified [public acceptance 38376089070/2](https://github.com/EpicBlackWolfZ/microfat/actions/runs/38376089070/attempts/2) passed the actual versioned bootstrap on both architectures in fresh nonroot containers without Go or preinstalled Cosign: authenticated triad hashes, default latest/pinned/reinstall, full/minimal discovery, memfd/cache and conservative uninstall. Custom/system roots and adversarial cases retain source/candidate scope. Closure follows reviewed documentation merge. |
| Official Linux Homebrew tap | [#205](https://github.com/EpicBlackWolfZ/microfat/issues/205) | The [final tooling pin](https://github.com/EpicBlackWolfZ/homebrew-tap/pull/3), immutable publication and authenticated [recipe PR #4](https://github.com/EpicBlackWolfZ/homebrew-tap/pull/4) are complete. Both native legs of [38376089070/2](https://github.com/EpicBlackWolfZ/microfat/actions/runs/38376089070/attempts/2) passed the documented official remote install at tap `87061d5bd8411a3e729ad5a6d25925ff76f669bb`: authenticated product triads, full/minimal memfd/cache discovery, ownership/self-update refusal, reinstall, same-version upgrade and conservative uninstall. Controlled cross-version/recovery fixtures remain separate. Closure follows reviewed documentation merge. |
| Authenticated runtime candidate qualification | [#260](https://github.com/EpicBlackWolfZ/microfat/issues/260) | Passed in [producer 38334917557, attempt 1](https://github.com/EpicBlackWolfZ/microfat/actions/runs/38334917557/attempts/1) against then-private draft release `411193467`, subsequently published immutable: each native architecture completed 1,059/1,059 cases, 4,051 execution observations and no skips. The complete retained matrix binds the same source and signed inventory. The published aggregate's whole hash/gzip CRC and fourteen selected authentication/summary members were verified; native production validators passed. Independent review and public integration passed; closure follows reviewed documentation merge. Source-built and QEMU results retain their own scope. |

### Reusable release order and exit

1. Merge reviewed correctness fixes and require the full source CI, exact coverage, native qualification
   and product-oriented snapshot checks on the integrated revision. A branch-only `benchmarks.yml`
   `tier=release` rehearsal can check source benchmark/evidence before staging; it skips the tag-only
   publisher. Update the release notes and tap tooling pin to the reviewed source; preserve environment
   limits and unavailable checks explicitly.
2. For signed draft qualification before publication approval, set the exact tag in the repository
   Actions variable `MICROFAT_DRAFT_ONLY_TAG` before pushing it. The release and benchmark pipelines
   still qualify the candidate; that tag's automatic publication job is skipped. Without this hold,
   a successful tag finalizer publishes the release. After approval, explicit `release-finalize.yml`
   dispatch publishes only from the successful tag-bound evidence and complete draft inventory.
3. After approval, require the exact signed draft's installer and runtime gates on native amd64/arm64,
   complete hosted benchmark evidence, and inventory revalidation before immutable publication.
4. Verify the first public bootstrap/helper path. Authenticate and qualify the first public Homebrew
   recipe before merging it and verifying its install command. Close #203, #205 and #260 only from
   their own completed acceptance evidence, not from source CI or milestone readiness alone.

The installer defaults to a user directory; system-wide writes are explicit. Homebrew controls its
own upgrades and removal. Native macOS, new binary formats, speculative compression research and
dedicated-hardware certification are outside this milestone.

Exit: a failure before activation preserves the prior release; a failure after activation reports the
new coherent selected generation accurately. Ownership and running-image
identity are checked before replacement. CLI/full/minimal stubs remain discoverable through supported
links and execution modes without mixing generations. Native and emulator qualification records include
environment limits and skips. CPU-policy precedence, cache integrity and the documented trust boundary
remain intact. The supported architecture/profile/format/codec/execution matrix, exact coverage and
shared release gates below apply to the final candidate; green intermediate CI is not release publication.

## v0.4.0: measured improvements and observability

Start with reproducible new-Pack creation epochs [#391](https://github.com/EpicBlackWolfZ/microfat/issues/391)
and per-variant CLI compression #43. The live
[delivery index #378](https://github.com/EpicBlackWolfZ/microfat/issues/378) records the complete
ten-issue commitment and ordering, including verified-descriptor handoff #392, prewarm planning
and payload-byte admission #390, and the original matched service-p99 investigation #230.
Issue-local acceptance remains authoritative; these are pending release commitments.

| Scope | Issues |
| --- | --- |
| Explicit new-Pack epoch and CLI `SOURCE_DATE_EPOCH` | [#391](https://github.com/EpicBlackWolfZ/microfat/issues/391) |
| Runtime metadata accessor/recipes and non-mutating doctor tuning preview | [#15](https://github.com/EpicBlackWolfZ/microfat/issues/15), [#156](https://github.com/EpicBlackWolfZ/microfat/issues/156) |
| Attribute and reduce disabled-telemetry overhead | [#41](https://github.com/EpicBlackWolfZ/microfat/issues/41) |
| Derive automatic codec policy from representative measurements | [#42](https://github.com/EpicBlackWolfZ/microfat/issues/42) |
| Per-variant CLI compression with existing manifest support and explicit precedence | [#43](https://github.com/EpicBlackWolfZ/microfat/issues/43) |
| Exact compatible-level bitsets with independent requirements-based equivalence tests | [#153](https://github.com/EpicBlackWolfZ/microfat/issues/153) |

Exit: metadata fields/profiles and unknowns are accurate, previews preserve safe GC prerequisites,
and optimizations demonstrate useful benefit without weakening correctness. Include AMD64 missing-feature
and cross-branch ARM64 cases. Fixed codec thresholds, sub-50ns selection and sub-100us startup are
hypotheses, not portable release requirements. Retaining a simpler default is a valid result.

## Research / uncommitted

[Research milestone](https://github.com/EpicBlackWolfZ/microfat/milestone/4)

The old v0.5.0/v0.6.0/v0.7.0/v1.0.0-lab sequence implied releases before feasibility was known.
These proposals now share an uncommitted track. Empty successor placeholders are retired; closing
them does not mean the research was delivered.

| Scope | Issues |
| --- | --- |
| Delta baseline → bounded reconstruction → bounded-depth topology | [#45](https://github.com/EpicBlackWolfZ/microfat/issues/45), [#46](https://github.com/EpicBlackWolfZ/microfat/issues/46), [#162](https://github.com/EpicBlackWolfZ/microfat/issues/162) |
| Section study, reversible normalization, instruction streams and joint representation | [#47](https://github.com/EpicBlackWolfZ/microfat/issues/47), [#163](https://github.com/EpicBlackWolfZ/microfat/issues/163), [#164](https://github.com/EpicBlackWolfZ/microfat/issues/164), [#165](https://github.com/EpicBlackWolfZ/microfat/issues/165) |
| Advanced feasibility: post-link rewriting, lazy materialization and simple entropy models | [#166](https://github.com/EpicBlackWolfZ/microfat/issues/166), [#167](https://github.com/EpicBlackWolfZ/microfat/issues/167), [#168](https://github.com/EpicBlackWolfZ/microfat/issues/168) |

Start with isolated, bounded experiments. Require byte-exact reconstruction for reversible encoding
and explicit semantic proof obligations for code-changing transforms. Count model/metadata/base sizes,
packaging cost, peak memory and full startup. A compression ratio alone is insufficient. Production
promotion requires an explicit format/decoder design, corruption/fuzz validation, the complete supported
format/profile/codec/mode matrix and a compatibility/rollback plan.

## Future / uncommitted

[Future milestone](https://github.com/EpicBlackWolfZ/microfat/milestone/11)

| Scope | Issues |
| --- | --- |
| Fleet files, native macOS and generalized non-level CPU capabilities | [#16](https://github.com/EpicBlackWolfZ/microfat/issues/16), [#17](https://github.com/EpicBlackWolfZ/microfat/issues/17), [#159](https://github.com/EpicBlackWolfZ/microfat/issues/159) |
| Workload analysis first; packaging search and capacity/cost models consume its evidence | [#95](https://github.com/EpicBlackWolfZ/microfat/issues/95), [#48](https://github.com/EpicBlackWolfZ/microfat/issues/48), [#94](https://github.com/EpicBlackWolfZ/microfat/issues/94) |
| Bounded compression prediction and dictionary-mining experiments | [#160](https://github.com/EpicBlackWolfZ/microfat/issues/160), [#161](https://github.com/EpicBlackWolfZ/microfat/issues/161) |
| Optional dedicated-hardware qualification with explicit host availability/ownership | [#182](https://github.com/EpicBlackWolfZ/microfat/issues/182) |

These proposals have no promised release/date. A concrete user need, focused design, owner and
reproducible evidence are required before promotion. Dedicated hardware is optional; hosted results
remain comparative evidence and cannot be relabeled as exclusive-hardware certification. No paid
resources or homelab changes are committed by this roadmap.

## Shared release gates

- Keep the Go patch pinned in `go.mod`, race checks, exact default/minimal statement coverage of at least 95%, lint,
  ShellCheck, vulnerability/security checks and the applicable integration/packaging matrix.
- Preserve mandatory payload/cache hashes, descriptor-bound reads/execution, bounded decompression,
  mandatory memfd seals and elevated-launch rejection. Same-UID cache writers remain trusted.
- Establish CPU compatibility before ranking; describe Go resource tuning as a soft budget.
- Verify publisher identity and exact downloaded bytes externally before first execution. A launcher
  cannot establish its own authenticity merely by checking embedded hashes.
- Compare native baseline, native specialized, fat memfd and fat cache with identical tuning;
  distinguish microbenchmarks, full startup, steady-state effects and environment limitations.
- Keep routine PR checks bounded; deep sustained/stress work stays scheduled/manual/release-specific.
  Report unavailable architecture/privileged checks explicitly rather than treating skips as success.
- Attach and verify all release assets while still in draft. Merged code, passing tests, milestone
  closure and publication are separate states; maintainer review governs merge and release actions.
