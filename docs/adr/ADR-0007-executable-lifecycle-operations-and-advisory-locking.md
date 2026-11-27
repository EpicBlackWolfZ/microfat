# ADR-0007: Executable Lifecycle Operations & Advisory Locking

- **Status**: Accepted (Retroactive — v0.2–v0.4)
- **Date**: 2026-11-27
- **Deciders**: Project Maintainer (@EpicBlackWolfZ)
- **Related Issues**: [#362](https://github.com/EpicBlackWolfZ/microfat/issues/362), [#365](https://github.com/EpicBlackWolfZ/microfat/issues/365), [#520](https://github.com/EpicBlackWolfZ/microfat/issues/520), [#523](https://github.com/EpicBlackWolfZ/microfat/issues/526), [#526](https://github.com/EpicBlackWolfZ/microfat/issues/526)
- **Related Specs**: [SPEC Index](../specs/README.md) (`SPEC-0006`), [Lifecycle Modes](../lifecycle-modes.md)
- **Supersedes**: None
- **Superseded By**: None

## Context

Operators frequently deploy a universal fat binary cleanly at build time, then specialize or prune unused architecture variants (`trim`), unwrap into a native bare ELF (`optimize`), extract variants (`specialize`), or pre-populate node caches (`prewarm`) — either from the CLI (`microfat trim|optimize|specialize|prewarm`) or self-hosted full stub meta-commands (`--microfat:trim`, `--microfat:optimize`, etc.). In-place mutation carries acute hazards: hardlink aliasing, concurrent writer corruption, ownership/mode escalation on `strip`/`strict` vs out-of-place targets, and self-modification after a running file was already replaced.

## Decision

Lifecycle transformations use explicit transactional snapshots, same-filesystem atomic staging, and advisory inode locks in `internal/lifecycle/` (mirrored cleanly by `cmd/microfat` and full stubs):

1. **Snapshot-Before-Lock Ordering (`#365`):** Resolves deployment target, validates `/proc/self/exe` binding against the running image when self-transforming, captures ownership/permissions/hardlink-count snapshots, acquires a bounded advisory `flock` on the target descriptor, and re-verifies snapshot consistency and `nlink` before atomic same-directory temporary staging and rename publication. (Independent un-locked external readers/modifiers remain outside advisory lock guarantees.)
2. **Hardlink & Special-Bit Policy:** Refuses multi-hardlink in-place mutations by default unless explicit `--break-hardlinks` consent is provided (or out-of-place `*-to` targets are used). Refuses `setuid`/`setgid` sources (`ErrUnsafeFile`), preserves accepted sticky bits only under explicit `strict` policy (`verifyAppliedMode`), clears special mode bits under `strip`, and defaults out-of-place `create-only` creation (`*-to`) to invoking-user default permissions (`umask`) unless `strict` is explicitly requested ([#520](https://github.com/EpicBlackWolfZ/microfat/issues/520)).
3. **Cache Prewarm Planning & Safety:** `microfat prewarm` supports `--plan` (dry-run reporting without touching cache), `--max-payload-bytes` preflight checks before lookup or extraction, `--variants`, and JSON output while sharing the stub's read-only/atomic cache invariants.
4. **Canonical Flag Namespaces:** CLI subcommands accept clean flags (`--keep`, `--strict`, etc.), whereas stub meta-commands require explicit `--microfat:`-prefixed lifecycle hints ([#520](https://github.com/EpicBlackWolfZ/microfat/issues/520)) so application flags never collide.

### Alternatives Rejected

- **In-place truncate-and-rewrite without staging:** Rejected because crash or power loss mid-write leaves a corrupt unreadable ELF binary.
- **Implicitly copying source special/group-write bits to newly created `optimize-to` targets:** Rejected in `#520` because non-explicit out-of-place creation should respect the invoking user's creation policy rather than silently amplifying permissions.

## Consequences & Tradeoffs

- **Operational Safety:** Failed staging leaves original binaries intact without `.tmp` leaks, and permission errors yield transformation-specific recovery messages rather than unrelated cache hints (`#362`).
- **Concurrency Scope:** Operators deploying concurrent upgrades over shared paths must serialize at the orchestration layer when replacing whole files via unrelated processes.

## Rollback

Every in-place transformation (`trim`, `optimize`, `specialize`) has an explicit non-destructive `*-to <dest>` counterpart, allowing operators to stage and verify transformed outputs side-by-side before swapping symlinks or deployment paths.
