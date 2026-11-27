# ADR-0008: Verified Generation-Based Installer, Updater & Linux Homebrew Trust Model

- **Status**: Accepted (Retroactive — v0.3)
- **Date**: 2026-11-27
- **Deciders**: Project Maintainer (@EpicBlackWolfZ)
- **Related Issues**: [#203](https://github.com/EpicBlackWolfZ/microfat/issues/203), [#204](https://github.com/EpicBlackWolfZ/microfat/issues/204), [#205](https://github.com/EpicBlackWolfZ/microfat/issues/205), [#361](https://github.com/EpicBlackWolfZ/microfat/issues/361), [#520](https://github.com/EpicBlackWolfZ/microfat/issues/520), [#523](https://github.com/EpicBlackWolfZ/microfat/issues/523), [#544](https://github.com/EpicBlackWolfZ/microfat/issues/544), [#526](https://github.com/EpicBlackWolfZ/microfat/issues/526)
- **Related Specs**: [SPEC Index](../specs/README.md) (`SPEC-0007`), [Installation Ownership & Discovery Contract](../design/installation-discovery-contract.md), [Installation Guide](../installation.md), [Homebrew Maintenance](../homebrew.md)
- **Supersedes**: None
- **Superseded By**: None

## Context

Installing multi-component toolchains (`microfat`, `microfat-stub`, `microfat-stub-minimal`) into `$HOME/.local/bin` or system paths exposes subtle failure modes: mid-upgrade version skew between CLI and companion stubs (especially when the CLI itself runs dispatched from `memfd` or disk cache), silent adoption/overwriting of package-manager files, `curl | bash` trust bootstrapping without Go or Cosign preinstalled on the destination host, and downgrade/TLS MITM exposure.

## Decision

`microfat` implements a generation-based verified installer, explicit updater, and bounded Linux Homebrew distribution model (Schema 1 in [Installation Contract](../design/installation-discovery-contract.md)):

1. **Bootstrap Trust Without Target Prerequisites:** `scripts/install.sh` fetches a versioned, pinned Cosign binary + pinned baseline native `microfat-install` helper archive (`checksums.txt` verified via tag-bound keyless Cosign OIDC identity/issuer) so clean Linux hosts require no Go, preinstalled Cosign, jq, or Python. Versioned installers pin their own helper release (`v0.3.0`, `v0.3.1`, etc.).
2. **Atomic Generation Store & Physical Discovery Binding:**
   - Publishes `microfat`, `microfat-stub`, and `microfat-stub-minimal` together inside `store/generations/<32-hex-id>/` alongside `generation.json` (`0644`), `owner.json` (`0644`), and a retained `.lock` (`0600`), activating atomically by renaming `current`.
   - `internal/builder/` binds running managed processes (across native, `memfd`, and `cache` dispatch) to their physical running generation (`MICROFAT_ORIGINAL_EXE` + payload hash + `generation.json` product inventory verification), preventing an already-running G1 CLI from silently loading G2 stubs after a concurrent G2 activation (`v0.3.0`).
   - Refuses manual or package-manager installs (`--force-adopt` does not exist); requires explicit `--system` for root installs and never invokes `sudo`.
3. **Explicit Self-Update & Linux Homebrew Separation:**
   - `microfat update` runs only explicitly (`--check` offline/HTTPS check without auto-install; `--version --allow-downgrade` for downgrades; `--repair` for corrupted managed stores). Semantic CLI usage errors exit `2` (`#361`).
   - External Linux Homebrew Caskroom installs write `{"schema":1,"owner":"homebrew"}` in `microfat-distribution.json` beside unmodified executables, instructing `microfat update` to direct users cleanly to `brew upgrade microfat` without touching Caskroom files.
4. **Minimum TLS 1.3 Network Hardening (`#544`):** Outbound HTTPS clients (`internal/tlsclient`) and download scripts enforce `TLS >= 1.3` and HTTPS-only redirects.

### Alternatives Rejected

- **In-place overwrite of three binaries in `/usr/local/bin`:** Rejected because an in-flight `microfat pack` during update mixes CLI and stub versions or fails mid-copy.
- **Adding `--force-adopt` to overwrite arbitrary existing binaries:** Rejected to uphold strict non-destructive package-manager ownership boundaries.

## Consequences & Tradeoffs

- **Reliability & Rollback:** Old generations stay complete on disk until pruned explicitly by an operator, eliminating partial-upgrade breakage and supporting instant rollback of `current`.
- **Disk Footprint:** Retaining previous generations trades minor disk space for concurrent process safety (`v0.5.0` CLI continues Schema 1 discovery without breaking existing `v0.3`/`v0.4` generations).

## Rollback

Operators can manually switch `store/current` back to any prior valid `generations/<ID>` directory or uninstall conservatively via `microfat-install uninstall`, which removes only symlinks still pointing to the managed store while leaving generations, lock files, and workload caches untouched.
