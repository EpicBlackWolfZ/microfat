# ADR-0012: Enforced ADR, Normative SPEC, Formal Verification Ledger, TDD, and Docs-as-Code Gates

- **Status**: Accepted
- **Date**: 2026-11-27
- **Deciders**: Project Maintainer (@EpicBlackWolfZ)
- **Related Issues**: [#523](https://github.com/EpicBlackWolfZ/microfat/issues/523), [#524](https://github.com/EpicBlackWolfZ/microfat/issues/524), [#525](https://github.com/EpicBlackWolfZ/microfat/issues/525), [#526](https://github.com/EpicBlackWolfZ/microfat/issues/526), [#527](https://github.com/EpicBlackWolfZ/microfat/issues/527), [#528](https://github.com/EpicBlackWolfZ/microfat/issues/528)
- **Related Specs**: [SPEC Index](../specs/README.md), [Verification Ledger](../verification/README.md)
- **Supersedes**: None
- **Superseded By**: None

## Context

Through `v0.4.1`, `microfat` maintained high code quality (`>= 95%` statement coverage, `-race`, `golangci-lint`, ShellCheck, Gobra proofs on cgroup memory and Format v2 bounds) and extensive prose documentation under `docs/`. However, two structural gaps remained:

1. **No standardized ADR/SPEC/ledger traceability:** Only [ADR-0001](../design/adr-0001-formal-verification-and-v3-lab.md) and the [installation contract](../design/installation-discovery-contract.md) existed as structured design artifacts. Normative requirements lacked `REQ-*` / `LAW-*` identifiers linking specs to tests and formal proofs, and historical `v0.1`–`v0.4.1` decisions lacked ADRs and complete formal coverage ledgers.
2. **Documentation drift & unverified docs-only PRs:** Broken internal Markdown links, missing heading anchors, and code-to-doc drift (CLI commands, `MICROFAT_*` environment variables, binary format constants, and verification ledger claims) were not automatically checked by CI, and docs-only PRs skipped CI code checks entirely.

With `v0.5.0` introducing dual Go + Rust production implementations, Format v3 alongside Format v2, and mandatory multi-toolchain formal verification (Gobra, Kani, Verus, Lean 4, Z3), documentation and formal contracts must be validated as code.

## Decision

Starting in `v0.5.0`, `microfat` enforces five engineering governance pillars:

1. **Architecture Decision Records (`docs/adr/`):**
   - Preserve [ADR-0001](../design/adr-0001-formal-verification-and-v3-lab.md) at its original path without renumbering.
   - Reserve `ADR-0002`–`ADR-0011` for retroactive `v0.1.0`–`v0.4.1` backfill ([#526](https://github.com/EpicBlackWolfZ/microfat/issues/526)) and record all subsequent architectural decisions as `docs/adr/ADR-NNNN-<slug>.md` indexed in [docs/adr/README.md](README.md).
2. **Normative RFC-2119 Specifications (`docs/specs/`):**
   - Author and maintain normative specs (`docs/specs/SPEC-NNNN-<slug>.md`, indexed in [docs/specs/README.md](../specs/README.md)) using globally unique `REQ-*` and `LAW-*` identifiers mapped to concrete tests and formal proofs.
3. **Formal Verification Ledger (`docs/verification/README.md`):**
   - Maintain a single machine-checked [formal verification ledger](../verification/README.md) recording every `LAW-*` claim, target source file/symbol, pinned verifier, Task target, CI gate step ID, trusted assumptions, and excluded paths.
4. **TDD & Dual-Language Coverage Enforcement:**
   - Behavioral changes require red-to-green evidence in PR descriptions and automated regression tests (`>= 95%` statement coverage + `-race` + leak checks in Go, expanded to Rust `>= 95%` + cross-language Lean-derived conformance vectors in [#525](https://github.com/EpicBlackWolfZ/microfat/issues/525)).
5. **Go-Native Docs-as-Code Gate (`task lint-docs`):**
   - Implement a zero-Python Go validator (`internal/docgate` and `internal/cmd/doc-gate`) wired into `task lint-docs`, `task lint`, `task all`, `.github/workflows/ci.yml`, and `.github/workflows/release.yml`.
   - `task lint-docs` runs on **every PR** (including docs-only PRs) and enforces Markdown links/anchors, ADR/SPEC schemas and index sync, formal ledger integrity (live paths, tool pins, Task targets, and CI step IDs), and code-to-doc parity (`cmd/microfat` subcommands, `MICROFAT_*` env vars, and Format v2 constants across Go, docs, and Rust).

### Alternatives Rejected

- **Python-based Sphinx/MkDocs/link-check scripts:** Rejected to honor the repository's zero-Python engineering baseline (`ADR-0010` candidate) and avoid introducing external Node/Python runtime dependencies into CI.
- **Third-party unpinned Markdown link checkers:** Rejected because they cannot verify ADR/SPEC frontmatter, `REQ-*`/`LAW-*` uniqueness, verification ledger pins/CI step IDs, or Go/Rust code-to-doc constant parity.

## Consequences & Tradeoffs

- **Bootstrap safety:** `task lint-docs` passes on the Phase 1 seed set (`ADR-0001`, `ADR-0012`, and existing Gobra proof docs) immediately so CI remains green while Phase 2 backfill issues ([#526](https://github.com/EpicBlackWolfZ/microfat/issues/526)–[#528](https://github.com/EpicBlackWolfZ/microfat/issues/528)) land incrementally.
- **Docs-only PR latency:** Docs-only PRs now set up Go + Task (both cached) and run `task lint-docs` (~5–15s) instead of skipping all verification; heavy test/snapshot/qualification stages remain skipped on docs-only PRs.
- **Maintainer discipline:** Adding new CLI commands, `MICROFAT_*` env vars, format constants, ADRs, SPECs, or formal laws requires updating documentation and indexes in the same PR.

## Rollback

The `doc-gate` check lives in standalone packages (`internal/docgate`, `internal/cmd/doc-gate`) and one CI step (`id: docs-gate`). If a false positive blocks an urgent release, the offending rule can be fixed directly in `internal/docgate` or temporarily bypassed only by explicit maintainer-reviewed removal of the `docs-gate` step in `.github/workflows/ci.yml` and `.github/workflows/release.yml` (alongside updating `internal/cigate`).
