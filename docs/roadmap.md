# Production roadmap

Updated 2026-09-09 against `main` at `0e7f296c1fae4d5f308eee9d62abdb2638d3ce58`.
This records planned work, not fixes already shipped. GitHub issues hold acceptance criteria;
[the implementation plan](implementation-plan.md) defines ordering and verification.

## Release principles

1. Ship a smaller, demonstrably safe production core before expanding optimization.
2. Urgent security and compatibility fixes do not wait for a complete benchmark framework.
3. Preserve mandatory payload/cache hashes, descriptor-bound execution, bounded decompression,
   and mandatory memfd sealing. Policy changes must preserve those invariants.
4. CPU requirements establish compatibility; ranking only chooses among compatible variants.
5. Describe resource tuning as a soft runtime budget, not a guarantee against container OOM.
6. Publish reproducible performance evidence and distinguish specialization from tuning effects.
7. Keep experimental reconstruction formats out of the production decoder until correctness,
   bounded resources and representative benefit are demonstrated.

## v0.2.3: urgent safety and measurement foundation

[Milestone](https://github.com/EpicBlackWolfZ/microfat/milestone/7)

The patch release prioritizes the following safety work:

| Scope | Issue |
| --- | --- |
| Terminating, bounded legacy JSON parsing | [#170](https://github.com/EpicBlackWolfZ/microfat/issues/170) |
| Reject unsupported elevated launcher execution | [#171](https://github.com/EpicBlackWolfZ/microfat/issues/171) |
| Feature-compatible ARM64 dispatch | [#172](https://github.com/EpicBlackWolfZ/microfat/issues/172) |
| Nonblocking rejection of special cache files | [#173](https://github.com/EpicBlackWolfZ/microfat/issues/173) |
| Cache owner/mode validation and mutable-inode boundary | [#174](https://github.com/EpicBlackWolfZ/microfat/issues/174) |
| Descriptor-bound, bounded packaging and dictionary sampling | [#151](https://github.com/EpicBlackWolfZ/microfat/issues/151) |
| Extraction and post-exec memory accounting | [#152](https://github.com/EpicBlackWolfZ/microfat/issues/152) |
| CI parity with local full/minimal and benchmark tests | [#175](https://github.com/EpicBlackWolfZ/microfat/issues/175) |
| Action pinning and job-scoped permissions | [#176](https://github.com/EpicBlackWolfZ/microfat/issues/176) |
| Supported integration examples and qualified claims | [#177](https://github.com/EpicBlackWolfZ/microfat/issues/177) |

Measurement work continues through #88–#93, building on merged #87. It is a separate track,
not a dependency for releasing the safety fixes. At release cut, unfinished measurement issues
move to a subsequent patch milestone; do not mark them complete or hold a security patch for them.

Exit: targeted regressions, shared race/coverage checks, applicable execution matrix and release
verification pass. Document any unavailable privileged or architecture-specific validation.
An urgent independently validated fix can ship earlier in the patch stream at maintainer discretion.

## v0.3.0: runtime invariants, compatibility and lifecycle safety

[Milestone](https://github.com/EpicBlackWolfZ/microfat/milestone/2)

- #85: complete the threat model after the immediate cache boundary correction in #174.
- #150: one safe transformation policy for CLI and launcher trim/optimize operations.
- #154: validate dynamic-linker and libc ABI consistency, with explicit override semantics.
- #44: cache-first auto policy with mandatory verification and descriptor-bound execution.
- #40: expose full/minimal stub selection while enforcing the same runtime security invariants.
- #155, #158: artifact selection, deliberate v1 deprecation, and executable-path guidance.
- #157: container mount/executable-path tests and release SBOM verification.
- #15: supported runtime observability recipes.
- #86: external artifact verification and publisher-identity policy before first execution.

Exit: policy and lifecycle contracts compose correctly across profiles, modes and architectures;
external verification rejects wrong publishers and modified artifacts. No nanosecond target gates release.
Legacy parser hardening remains necessary until support is actually removed by a documented policy.

## v0.4.0: focused, evidence-gated improvements

[Milestone](https://github.com/EpicBlackWolfZ/microfat/milestone/3)

Scope is limited to compatible-level bitsets (#153), telemetry overhead (#41), codec selection and
per-variant ergonomics (#42–#43), and tuning diagnostics (#156). Implement only after correctness
contracts and measurement infrastructure can show benefit. A sub-50 ns selection time is a hypothesis,
not a release requirement. Heuristics need a measured baseline and must retain explicit user overrides.

## Future / uncommitted

Fleet policy (#16), macOS (#17), Pareto tuning (#48), FinOps (#94), user-facing benefit analysis (#95),
generalized capability solving (#159), empirical compression prediction (#160), and dictionary mining
(#161) are separate proposals in an uncommitted milestone. Each requires a focused design, demonstrated
operator need, dependencies and an evidence gate before promotion. None is silently closed or promised
for v0.4.0. More elaborate authentication envelopes/launcher hooks require a follow-up to external
verification #86 rather than delaying the initial trust workflow.

## Experimental releases and lab

| Milestone | Scope | Promotion gate |
| --- | --- | --- |
| v0.5.0 | Delta codecs/reconstruction, bounded delta trees, ELF section deduplication (#45–#47, #162) | Byte-exact reconstruction, bounded resources and representative startup/size evidence |
| v0.6.0 | Instruction normalization, decomposition, joint representation (#163–#165) | Reconstruction/semantic correctness, resource bounds and measured benefit |
| v0.7.0 | Post-link optimization (#166) | Semantic validation and steady-state parity |
| v1.0.0-lab | Predictive materialization and entropy modeling (#167–#168) | Independent research evidence before production consideration |

Keep research isolated. Any format/decoder promotion requires a complete supported matrix of format
versions, profiles, codecs and execution modes, corruption/fuzz tests, and a compatibility/rollback plan.
A compression ratio alone is insufficient evidence.
