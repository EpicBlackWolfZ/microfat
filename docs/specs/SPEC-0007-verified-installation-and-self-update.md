# SPEC-0007: Verified Installation, Companion Discovery & Self-Update

- **Spec ID**: SPEC-0007
- **Status**: Normative
- **Date**: 2026-11-28
- **Related ADRs**: [`ADR-0008`](../adr/ADR-0008-verified-generation-installer-updater-and-homebrew-trust.md), [`ADR-0009`](../adr/ADR-0009-release-supply-chain-trust-cosign-and-modern-sboms.md)
- **Related Issues**: [#203](https://github.com/EpicBlackWolfZ/microfat/issues/203), [#204](https://github.com/EpicBlackWolfZ/microfat/issues/204), [#205](https://github.com/EpicBlackWolfZ/microfat/issues/205), [#527](https://github.com/EpicBlackWolfZ/microfat/issues/527), [#528](https://github.com/EpicBlackWolfZ/microfat/issues/528), [#544](https://github.com/EpicBlackWolfZ/microfat/issues/544)
- **Verified By**: [`internal/install`](../../internal/install/transaction_test.go), [`internal/update`](../../internal/update/update_test.go), [`internal/tlsclient`](../../internal/tlsclient/transport_test.go), [`tests/e2e/managed_generation_test.go`](../../tests/e2e/managed_generation_test.go), `LAW-INSTALL-01`, `LAW-LEAN-06`

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in RFC 2119.

## Scope & Terminology

This specification elevates [`docs/design/installation-discovery-contract.md`](../design/installation-discovery-contract.md) into normative requirements covering [`cmd/microfat-install`](../../cmd/microfat-install/main.go), [`internal/install`](../../internal/install/transaction.go), [`internal/installrelease`](../../internal/installrelease/verifier.go), [`internal/update`](../../internal/update/update.go), [`internal/tlsclient`](../../internal/tlsclient/transport.go), and [`internal/homebrew`](../../internal/homebrew/recipe.go).

## Normative Requirements

- `REQ-0007-01`: All remote release artifact, checksum, certificate, and signature downloads across Go installers/updaters (`internal/tlsclient`) and shell scripts (`install.sh`, `scripts/install-*.sh`) SHALL enforce `HTTPS`-only URLs (`--proto '=https' --proto-redir '=https'`) and minimum TLS 1.3 (`--tlsv1.3`, `tls.VersionTLS13`, `#544`).
- `REQ-0007-02`: Release bundle verification SHALL validate Cosign keyless bundles / signatures against pinned GitHub OIDC issuer & workflow identity AND verify every archive's SHA-256 digest before extracting any payload executable.
- `REQ-0007-03`: Installed generation transactions SHALL extract payloads into a unique temporary directory under an executable filesystem (honoring `--staging-dir` when `/tmp` is `noexec`), verify extracted regular files via SHA-256 and architecture checks, commit atomically to a versioned generation directory (`versions/<version>/`), and switch active symlinks with automatic rollback to the previous verified generation if post-commit validation fails.
- `REQ-0007-04`: Companion lookup (`microfat-stub`, `microfat-stub-minimal`) SHALL resolve strictly according to [`docs/design/installation-discovery-contract.md`](../design/installation-discovery-contract.md) precedence and SHALL refuse symlinked, writable-by-others, or untrusted companion binaries.
- `REQ-0007-05`: `microfat update` and `microfat-install` SHALL verify newly downloaded binaries in an isolated executable temporary staging area before replacing installed symlinks/binaries and SHALL support `MICROFAT_TEST_NOEXEC_PARENT` qualification on real `noexec` mounts.
- `REQ-0007-06`: Uninstall SHALL remove managed generation state and symlinks cleanly without deleting user-owned unrelated binaries or mutating directories outside managed prefixes.
- `REQ-0007-07`: Homebrew formula generation and bottle/tarball metadata in [`internal/homebrew`](../../internal/homebrew/recipe.go) SHALL record exact SHA-256 checksums matching published GitHub Release assets and validate on native Linux Homebrew runners.

## Formal Laws & Invariants

- `LAW-INSTALL-01`: Ownership match, `0o6022` special/writable mode refusal, and `<= MaxFileBytes` size bounds ([`checkInstallEntryMetadata`](../../internal/install/metadata.go), [`checkInstallFileSize`](../../internal/install/metadata.go), called by [`platform_linux.go`](../../internal/install/platform_linux.go) & [`types.go`](../../internal/install/types.go), `task verify-install-metadata`).
- `LAW-LEAN-06`: Formal generation install/rollback state machine ([`formal/lean/Microfat/InstallTx.lean`](../../formal/lean/Microfat/InstallTx.lean)) proving every failure before switch leaves active links unchanged and post-switch validation failure restores the previous valid generation (`task verify-lean`).

## Traceability Matrix

| ID | Description | Implementation | Verification (Tests / Proofs) |
| :--- | :--- | :--- | :--- |
| `REQ-0007-01` | Strict HTTPS + TLS 1.3 transport policy | [`internal/tlsclient/transport.go`](../../internal/tlsclient/transport.go), [`scripts/install.sh`](../../scripts/install.sh) | [`internal/tlsclient/transport_test.go`](../../internal/tlsclient/transport_test.go), [`cmd/microfat-install/bootstrap_test.go`](../../cmd/microfat-install/bootstrap_test.go) |
| `REQ-0007-02` | Cosign OIDC & SHA-256 release verification | [`internal/installrelease/verifier.go`](../../internal/installrelease/verifier.go) | [`tests/e2e/release_signature_test.go`](../../tests/e2e/release_signature_test.go) |
| `REQ-0007-03` | Versioned generation atomic switch & rollback | [`internal/install/transaction.go`](../../internal/install/transaction.go) | [`internal/install/recovery_test.go`](../../internal/install/recovery_test.go), [`tests/e2e/managed_generation_test.go`](../../tests/e2e/managed_generation_test.go) |
| `REQ-0007-04` | Companion binary discovery & trust boundary | [`internal/install/discovery.go`](../../internal/install/discovery.go) | [`internal/install/discovery_test.go`](../../internal/install/discovery_test.go), [`tests/e2e/mount_companions_test.go`](../../tests/e2e/mount_companions_test.go) |
| `REQ-0007-05` | Self-update verification & `--staging-dir` `noexec` safety | [`internal/update/update.go`](../../internal/update/update.go) | [`internal/update/update_test.go`](../../internal/update/update_test.go), [`tests/e2e/update_test.go`](../../tests/e2e/update_test.go) |
| `REQ-0007-06` | Clean generation & prefix uninstall | [`internal/install/uninstall.go`](../../internal/install/uninstall.go) | [`internal/install/transaction_test.go`](../../internal/install/transaction_test.go) |
| `REQ-0007-07` | Linux Homebrew formula checksum & native check | [`internal/homebrew/recipe.go`](../../internal/homebrew/recipe.go) | [`tests/e2e/homebrew_test.go`](../../tests/e2e/homebrew_test.go) |
| `LAW-INSTALL-01` | Installer entry safety & size bounds | [`internal/install/metadata.go`](../../internal/install/metadata.go) | `task verify-install-metadata` |
| `LAW-LEAN-06` | Lean 4 `InstallTx.lean` rollback model | [`formal/lean/Microfat/InstallTx.lean`](../../formal/lean/Microfat/InstallTx.lean) | `task verify-lean` |
