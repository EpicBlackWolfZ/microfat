# ADR-0010: Go & Task Tooling Consolidation, Pinned Toolchains & Zero-Owned-Python Policy

- **Status**: Accepted (Retroactive — v0.2.5)
- **Date**: 2026-11-27
- **Deciders**: Project Maintainer (@EpicBlackWolfZ)
- **Related Issues**: [#523](https://github.com/EpicBlackWolfZ/microfat/issues/523), [#524](https://github.com/EpicBlackWolfZ/microfat/issues/524), [#526](https://github.com/EpicBlackWolfZ/microfat/issues/526)
- **Related Specs**: [SPEC Index](../specs/README.md) (`SPEC-0009`), [Governance ADR](ADR-0012-engineering-governance-and-docs-as-code.md), [Contributor Guide](../../CONTRIBUTING.md)
- **Supersedes**: Legacy Makefiles and Python helper scripts (`v0.1`–`v0.2.4`)
- **Superseded By**: None

## Context

Prior to `v0.2.5`, repository build orchestration, coverage merging, benchmark result aggregation, and release helper checks relied on Makefiles combined with ad-hoc Python scripts (`BLINT` and helper scripts). That split introduced Python runtime/dependency drift between developer machines and CI runners, lacked SHA-256 verification on downloaded CI tooling, and duplicated logic outside Go's type system, race detector, and `>= 95%` coverage gate.

## Decision

Starting in `v0.2.5` and reinforced in `v0.5.0` ([ADR-0012](ADR-0012-engineering-governance-and-docs-as-code.md)):

1. **Zero-Owned-Python Policy:** All repository-owned tooling, tests, CI helpers, and automation previously written in Python (`coverage` merging, `benchmark-matrix`, `benchmark-evidence`, `release-finalize`, `release-audit`, `doc-gate`, etc.) live in tested Go packages under `internal/` and `internal/cmd/`, with **no new owned Python** permitted in application code, tests, scripts, or CI.
2. **Pinned Task Orchestration (`Taskfile.yml` & `.config/tool-versions.env`):** Every developer and CI workflow entry point (`task all`, `task lint`, `task coverage-unit`, `task verify-formal`, etc.) uses Task (`TASK_VERSION`), sourcing centralized version and SHA-256 pins from [`.config/tool-versions.env`](../../.config/tool-versions.env) (`golangci-lint`, `govulncheck`, `cdxgen`, `shellcheck`, `gitleaks`, `cosign`, `goreleaser`, `gobra`, `z3`, `kani`, `elan`, `verus`, `cargo-llvm-cov`, `cargo-audit`, `cargo-deny`, `rust`).
3. **Strict Non-Go Shell & Static Analysis Baselines:** Shell scripts (`scripts/*.sh`) are restricted to lightweight plumbing enforcing `#!/usr/bin/env bash`, `set -euo pipefail`, `IFS=$'\n\t'`, and zero warnings under `.shellcheckrc` optional strict checks (`SC1090` retained), while Go enforces `golangci-lint` (`errcheck`, `mnd`, `lll <= 140`, `bodyclose`, `nakedret`) and `NO_COLOR`/`[ -t 1 ]` terminal hygiene.
4. **Go Toolchain Integrity:** Tooling and CI strictly match the exact Go patch pinned in `go.mod` (`toolchain.go`) without downgrading toolchain directives.

### Alternatives Rejected

- **Continuing with Make + virtual-environment Python scripts:** Rejected due to un-typed orchestration, slower cross-platform setup, and split coverage/lint pipelines.
- **Shell-only parsing for JSON/YAML/Markdown validation:** Rejected in favor of unit-tested Go packages (`>= 95%` coverage) with simple Bash wrappers where required.

## Consequences & Tradeoffs

- **Reproducibility & Security:** Centralized SHA-256-pinned installer scripts verify downloaded binary tools cleanly before execution, and helper logic is covered by Go race tests and coverage gates just like production code.
- **Constraint:** Contributors must implement new CLI/CI automation in Go, Task, or (when targeting stub-runtime code) Rust rather than Python scripts.

## Rollback

Every `internal/cmd/*` Go helper exposes standard CLI subcommands runnable directly via `go run ./internal/cmd/<tool>` independently of `Taskfile.yml`, and tool versions/hashes in [`.config/tool-versions.env`](../../.config/tool-versions.env) can be updated or reverted cleanly via standard single-file PRs.
