# Production roadmap

Updated 2026-09-21 against `main` at `de191f0`. GitHub issues define remaining scope,
dependencies and acceptance criteria. Milestones group deliverable outcomes; they are not dates.

## Current baseline

[v0.2.3](https://github.com/EpicBlackWolfZ/microfat/releases/tag/v0.2.3) was published on
2026-09-16. It includes the safety and hosted-measurement foundation, per-architecture fat CLI
archives with companion full/minimal stubs, and release integrity/SBOM validation. Format v1
is deprecated; support must follow the documented policy rather than disappear incidentally.
See [release artifacts](release-artifacts.md) and [benchmark evidence](benchmarks/README.md).
The historical release-note verification instructions need correction in
[#197](https://github.com/EpicBlackWolfZ/microfat/issues/197); publication is already complete.

Since that release, [PR #192](https://github.com/EpicBlackWolfZ/microfat/pull/192) added developer
workflow/profiling/leak checks and [PR #194](https://github.com/EpicBlackWolfZ/microfat/pull/194)
added ShellCheck. They are merged into `main`; they are not new work to implement again.

## Delivery sequence

| Milestone | Outcome | What must not delay it |
| --- | --- | --- |
| [v0.2.4](https://github.com/EpicBlackWolfZ/microfat/milestone/12) | Correct existing runtime, packaging, verification and documentation defects | Tooling migrations and new distribution features |
| [v0.2.5](https://github.com/EpicBlackWolfZ/microfat/milestone/13) | Consolidate tooling, release trust and CI without losing validation | Installer/updater/tap features or speculative attestations |
| [v0.3.0](https://github.com/EpicBlackWolfZ/microfat/milestone/2) | Coherent installation and executable lifecycle contracts | Native macOS support and new compression formats |
| [v0.4.0](https://github.com/EpicBlackWolfZ/microfat/milestone/3) | Observability and performance improvements supported by evidence | Arbitrary latency targets and dedicated-hardware availability |

Urgent, independently validated corrections can ship earlier in the patch stream. A tooling
migration must not hold a known runtime fix. Work can proceed independently where dependencies
allow, but overlapping refactors follow correctness fixes and retain their regressions.

## v0.2.4: correctness and compatibility

The next patch fixes existing behavior using the current working build/release tools.

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

Preserve the agreed Go/Task direction and modern SBOM goals as a separate engineering release.

| Scope | Issues |
| --- | --- |
| Complete the existing threat model, then external verification policy | [#85](https://github.com/EpicBlackWolfZ/microfat/issues/85), [#86](https://github.com/EpicBlackWolfZ/microfat/issues/86) |
| Replace repository Python helpers with Go, then migrate Make callers to Task | [#198](https://github.com/EpicBlackWolfZ/microfat/issues/198), [#199](https://github.com/EpicBlackWolfZ/microfat/issues/199) |
| Prove and migrate the cdxgen-based modern SBOM path | [#202](https://github.com/EpicBlackWolfZ/microfat/issues/202) |
| Stage all PR work behind fast checks and a fail-closed final result | [#200](https://github.com/EpicBlackWolfZ/microfat/issues/200) |
| Resolve actionable quality findings and scan the integrated candidate | [#201](https://github.com/EpicBlackWolfZ/microfat/issues/201) |

The dependency order is threat model → external verification, and Go helper contracts → Task callers.
The SBOM experiment can proceed alongside helper design: prove CycloneDX 1.7 / SPDX 3.0.1 schema and
archive/variant inventory fidelity without Python/BLINT before retiring the old generator. Final CI
rollout consumes the completed commands and release contracts. Quality triage starts early; the final
scan follows the integrated migrations and permits only narrow justified exceptions.

Exit: active workflows work without repository Python/Make dependencies; exact coverage, benchmark
replay, verified downloads, process cleanup, developer workflows and meaningful validation remain
intact. Modern SBOMs pass schema and independent semantic checks on both architectures. Keep historical
schema support explicit. Signed checksums and draft-first publication remain mandatory. CDXA, new
attestations and historical asset replacement are outside this milestone.

## v0.3.0: installation and lifecycle contracts

| Scope | Issues |
| --- | --- |
| Shared atomic trim/optimize identity and metadata policy | [#150](https://github.com/EpicBlackWolfZ/microfat/issues/150) |
| Expose selection of the already shipped full/minimal stubs | [#40](https://github.com/EpicBlackWolfZ/microfat/issues/40) |
| Dynamic-linker/ABI checks after basic executable validation | [#154](https://github.com/EpicBlackWolfZ/microfat/issues/154) |
| Mount and executable-hint qualification; SBOM scope belongs to v0.2.5 | [#157](https://github.com/EpicBlackWolfZ/microfat/issues/157) |
| Verified user installer with coherent transaction and ownership/layout metadata | [#203](https://github.com/EpicBlackWolfZ/microfat/issues/203) |
| Explicit updater and Linux Homebrew tap consuming that installation contract | [#204](https://github.com/EpicBlackWolfZ/microfat/issues/204), [#205](https://github.com/EpicBlackWolfZ/microfat/issues/205) |
| Cache-first auto policy after trust, memfd, image-identity and cache fixes | [#44](https://github.com/EpicBlackWolfZ/microfat/issues/44) |

Transformations follow the threat model and running-image fix, with deliberate ownership, mode,
hard-link, ACL/xattr, capability and signature handling. Mount tests follow image-identity and path
contracts. The installer comes before the updater; tap and updater implementations can proceed
independently once ownership/layout is fixed. The installer defaults to a user directory, while
system-wide writes are explicit. Ordinary commands do not gain background update checks. Homebrew
covers Linux amd64/arm64 and retains control of package-managed upgrades; it does not imply macOS support.

Exit: installation/update failure leaves a coherent usable prior release. Ownership and running-image
identity are checked before replacement. CLI/full/minimal stubs remain discoverable through supported
links and execution modes. Transformations and cache policy preserve the documented trust boundary;
matching loader metadata does not prove a target system has a compatible libc.

## v0.4.0: measured improvements and observability

| Scope | Issues |
| --- | --- |
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

- Keep Go 1.27.1, race checks, exact default/minimal statement coverage of at least 95%, lint,
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
