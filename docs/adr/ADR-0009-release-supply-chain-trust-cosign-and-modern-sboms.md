# ADR-0009: Release Supply-Chain Trust (Cosign Keyless, Draft-First Immutable Releases & Modern SBOMs)

- **Status**: Accepted (Retroactive — v0.2.5–v0.4)
- **Date**: 2026-11-27
- **Deciders**: Project Maintainer (@EpicBlackWolfZ)
- **Related Issues**: [#429](https://github.com/EpicBlackWolfZ/microfat/pull/429), [#441](https://github.com/EpicBlackWolfZ/microfat/issues/441), [#449](https://github.com/EpicBlackWolfZ/microfat/pull/449), [#451](https://github.com/EpicBlackWolfZ/microfat/pull/451), [#473](https://github.com/EpicBlackWolfZ/microfat/issues/473), [#522](https://github.com/EpicBlackWolfZ/microfat/issues/522), [#536](https://github.com/EpicBlackWolfZ/microfat/issues/536), [#542](https://github.com/EpicBlackWolfZ/microfat/issues/542), [#545](https://github.com/EpicBlackWolfZ/microfat/issues/545), [#546](https://github.com/EpicBlackWolfZ/microfat/issues/546), [#523](https://github.com/EpicBlackWolfZ/microfat/issues/523), [#526](https://github.com/EpicBlackWolfZ/microfat/issues/526)
- **Related Specs**: [SPEC Index](../specs/README.md) (`SPEC-0008`), [Release Verification](../release-verification.md), [Release Artifacts](../release-artifacts.md), [Security Standards Compliance Matrix](../security-standards-compliance.md)
- **Supersedes**: None
- **Superseded By**: None

## Context

Software supply-chain integrity requires publishing binaries that downstream operators can verify end-to-end before execution, preventing unverified draft publication, toolchain mismatch between GoReleaser and source CI, stale/downgraded SBOM schemas, or post-publication asset tampering. Early `v0.2.4` archives emitted SPDX 2.3 and CycloneDX 1.5 metadata without independent Go inventory cross-checking against the packed archives.

## Decision

`microfat` enforces a staged, cryptographically verifiable release pipeline in `.github/workflows/release.yml`, `internal/releasesbom/`, `internal/releasecheck/`, and `internal/releaseaudit/`:

1. **Draft-First Immutable Release Choreography:** Tag workflows (`v*`) stage signed archives into a private GitHub draft release, run native `amd64`/`arm64` signed-candidate installer & runtime qualification plus hosted benchmark qualification on exact draft bytes, and invoke `release-finalize` only after all gates pass. Published releases are marked immutable, and GitHub rulesets protect release tags.
2. **Tag-Bound Keyless Sigstore Cosign Signing:** Signs `checksums.txt` (`checksums.txt.sig`) bound strictly to `release.yml@refs/tags/v<version>` and GitHub's OIDC issuer (`COSIGN_LINUX_*` pinned by SHA-256 in [`.config/tool-versions.env`](../../.config/tool-versions.env)). Downstream consumers verify `checksums.txt` first and verify archive/SBOM/helper hashes against that manifest.
3. **Modern SBOMs (CycloneDX 1.7 + SPDX 3.0.1) & Compiler Checks (`v0.2.5+`–`v0.4+`):** Generates and validates modern CycloneDX 1.7 and SPDX 3.0.1 SBOMs per architecture archive and installer helper using the pinned standalone `cdxgen` converter (`cdx-convert` `CDXGEN_LINUX_*`) combined with exact Go module/embedded-variant/archive inventory checks (`internal/releasesbom`, `internal/sbom`) and Go compiler version verification (`#449`).
4. **Vulnerability Auditing & `v0.5.0` Conformance Expansion:** Runs `govulncheck` across source and released artifacts, `gitleaks` secret scans, and `CodeQL`, with `v0.5.0` extending coverage to Rust Cargo inventories (`#536`), SLSA Build L3 provenance + OpenSSF Scorecard + license parity + deterministic rebuild verification (`#545`), BSI TR-03183-2 SBOM checks, and OpenVEX advisories (`#546`).

### Alternatives Rejected

- **Publishing unsigned releases directly on tag creation before candidate qualification:** Rejected because unverified binaries could briefly become downloadable via `/releases/latest`.
- **Using legacy unvalidated SPDX 2.3 / CycloneDX 1.5 without archive cross-checks:** Rejected in `v0.2.5` because broken relationships, missing hashes, and stale versions were not caught prior to publication.

## Consequences & Tradeoffs

- **Assurance:** Release promotion prevents partial/mismatched assets and detects compiler/toolchain or SBOM schema regressions automatically before public release finalization.
- **Pipeline Duration & Tooling Pins:** Tag releases require pinned SHA-256 external tool downloads and multi-stage native runner qualification prior to publication.

## Rollback

Draft release failures leave public latest pointers and previous immutable releases untouched; failed drafts can be deleted and re-run cleanly without mutating historical signed artifacts. Standalone `sbom` and SBOM validation checks remain independently inspectable via `task snapshot` before tagging.
