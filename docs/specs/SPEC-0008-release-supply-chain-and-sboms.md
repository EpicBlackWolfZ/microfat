# SPEC-0008: Release Artifacts, Supply-Chain Trust & Modern SBOM Fidelity

- **Spec ID**: SPEC-0008
- **Status**: Normative
- **Date**: 2026-11-28
- **Related ADRs**: [`ADR-0009`](../adr/ADR-0009-release-supply-chain-trust-cosign-and-modern-sboms.md), [`ADR-0010`](../adr/ADR-0010-go-task-tooling-consolidation-and-zero-python-policy.md), [`ADR-0012`](../adr/ADR-0012-engineering-governance-and-docs-as-code.md)
- **Related Issues**: [#441](https://github.com/EpicBlackWolfZ/microfat/issues/441), [#527](https://github.com/EpicBlackWolfZ/microfat/issues/527), [#542](https://github.com/EpicBlackWolfZ/microfat/issues/542), [#544](https://github.com/EpicBlackWolfZ/microfat/issues/544), [#545](https://github.com/EpicBlackWolfZ/microfat/issues/545), [#546](https://github.com/EpicBlackWolfZ/microfat/issues/546)
- **Verified By**: [`internal/releasesbom`](../../internal/releasesbom/generate_test.go), [`internal/releaseaudit`](../../internal/releaseaudit/audit_test.go), [`internal/sbom`](../../internal/sbom/spdx_test.go), [`cmd/microfat/release_matrix_test.go`](../../cmd/microfat/release_matrix_test.go), `LAW-SUPPLY-01`

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in RFC 2119.

## Scope & Terminology

This specification defines software bill of materials (SBOM) generation and schema fidelity, cryptographic verification and release signing gates, toolchain pinning, FIPS/CAVP hash conformance, and supply-chain audit checks across [`internal/releasesbom`](../../internal/releasesbom/generate.go), [`internal/releaseaudit`](../../internal/releaseaudit/audit.go), [`internal/sbom`](../../internal/sbom/cyclonedx.go), [`.config/tool-versions.env`](../../.config/tool-versions.env), and [`.github/workflows/release.yml`](../../.github/workflows/release.yml).

## Normative Requirements

- `REQ-0008-01`: Tagged releases SHALL publish in draft mode until every build artifact, checksum manifest, Cosign keyless OIDC bundle, SLSA/release audit, and SBOM verification gate succeeds (`internal/cmd/release-finalize`).
- `REQ-0008-02`: All release and CI build/verification toolchains SHALL be pinned by explicit version and SHA-256 checksum in [`.config/tool-versions.env`](../../.config/tool-versions.env), and third-party GitHub Actions SHALL be pinned by immutable full 40-character commit SHA.
- `REQ-0008-03`: Release and CI SBOM checks SHALL emit schema-validated `CycloneDX` (`v1.7`, via pinned `cdxgen`/`cdx-convert`) and `SPDX` (`3.0.1`) manifests covering both Go modules and Rust stub dependencies (without silent `SKIP` when `MICROFAT_SBOM_TESTS=required`), and [`docs/security-standards-compliance.md`](../security-standards-compliance.md) SHALL accurately track CycloneDX `1.7`, SPDX `3.0.1`, CRA/SSDF/SLSA alignment, and explicit deferrals ([#542](https://github.com/EpicBlackWolfZ/microfat/issues/542), [#545](https://github.com/EpicBlackWolfZ/microfat/issues/545), [#546](https://github.com/EpicBlackWolfZ/microfat/issues/546)).
- `REQ-0008-04`: Repository Go, Rust, and workflow dependencies SHALL pass `govulncheck` (`task vuln`), `cargo-audit` + `cargo-deny` (`task vuln-rust`), `gitleaks`, CodeQL, and `golangci-lint` without suppressed critical findings, and zero new owned Python helpers SHALL be introduced ([`ADR-0010`](../adr/ADR-0010-go-task-tooling-consolidation-and-zero-python-policy.md)).
- `REQ-0008-05`: Both the software and hardware-accelerated SHA-256 implementations in the Rust launcher ([`experiments/v3lab/rust-stub/src/sha256.rs`](../../experiments/v3lab/rust-stub/src/sha256.rs), [`sha256_hw.rs`](../../experiments/v3lab/rust-stub/src/sha256_hw.rs)) SHALL match all official NIST CAVP FIPS 180-4 short/long test vectors at [`formal/conformance/sha256_cavp.json`](../../formal/conformance/sha256_cavp.json) (`#544`).
- `REQ-0008-06`: [`internal/releasechecksums`](../../internal/releasechecksums/checksums.go) and [`internal/releaseaudit`](../../internal/releaseaudit/audit.go) SHALL verify digest consistency across GoReleaser archives, binaries, SBOMs, and release notes prior to finalizing a public release.

## Formal Laws & Invariants

- `LAW-SUPPLY-01`: SHA-256 digest matching, NIST CAVP FIPS 180-4 bit-for-bit test conformance, and non-empty CycloneDX 1.7 / SPDX 3.0.1 dependency completeness invariants enforced across Go (`task coverage-unit`, `task vuln`) and Rust (`task test-rust`, `task vuln-rust`) gates.

## Traceability Matrix

| ID | Description | Implementation | Verification (Tests / Proofs) |
| :--- | :--- | :--- | :--- |
| `REQ-0008-01` | Draft-first release gating & finalization | [`internal/releaseworkflow`](../../internal/releaseworkflow/finalize.go), [`.github/workflows/release.yml`](../../.github/workflows/release.yml) | [`cmd/microfat/release_matrix_test.go`](../../cmd/microfat/release_matrix_test.go) |
| `REQ-0008-02` | Pinned tool hashes & SHA-pinned GitHub Actions | [`.config/tool-versions.env`](../../.config/tool-versions.env), [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) | [`internal/docgate/governance.go`](../../internal/docgate/governance.go) |
| `REQ-0008-03` | CycloneDX 1.7 & SPDX 3.0.1 SBOM + standards matrix | [`internal/releasesbom/generate.go`](../../internal/releasesbom/generate.go), [`internal/sbom/cyclonedx.go`](../../internal/sbom/cyclonedx.go), [`docs/security-standards-compliance.md`](../security-standards-compliance.md) | [`internal/releasesbom/generate_test.go`](../../internal/releasesbom/generate_test.go), `MICROFAT_SBOM_TESTS=required task coverage-unit` |
| `REQ-0008-04` | Vulnerability, license, secret & CodeQL hygiene | [`Taskfile.yml`](../../Taskfile.yml), [`experiments/v3lab/rust-stub/deny.toml`](../../experiments/v3lab/rust-stub/deny.toml) | `task vuln`, `task vuln-rust`, `task lint` |
| `REQ-0008-05` | NIST CAVP FIPS 180-4 SHA-256 vector parity | [`experiments/v3lab/rust-stub/src/sha256.rs`](../../experiments/v3lab/rust-stub/src/sha256.rs), [`experiments/v3lab/rust-stub/src/sha256_hw.rs`](../../experiments/v3lab/rust-stub/src/sha256_hw.rs) | `nist_cavp_sha256_conformance`, `nist_cavp_sha256_hw_conformance` |
| `REQ-0008-06` | Archive checksum & release audit completeness | [`internal/releasechecksums/checksums.go`](../../internal/releasechecksums/checksums.go), [`internal/releaseaudit/audit.go`](../../internal/releaseaudit/audit.go) | [`internal/releaseaudit/audit_test.go`](../../internal/releaseaudit/audit_test.go) |
| `LAW-SUPPLY-01` | Release trust, SBOM schema & CAVP invariants | [`internal/releasesbom/generate.go`](../../internal/releasesbom/generate.go), [`formal/conformance/sha256_cavp.json`](../../formal/conformance/sha256_cavp.json) | `task coverage-unit`, `task test-rust`, `task vuln-rust` |
