# ADR-0004: Sealed `memfd_create`, Cache-First Descriptor-Bound Execution & Privilege Refusal

- **Status**: Accepted (Retroactive — v0.1–v0.4)
- **Date**: 2026-11-27
- **Deciders**: Project Maintainer (@EpicBlackWolfZ)
- **Related Issues**: [#417](https://github.com/EpicBlackWolfZ/microfat/pull/417), [#469](https://github.com/EpicBlackWolfZ/microfat/issues/469), [#516](https://github.com/EpicBlackWolfZ/microfat/issues/516), [#517](https://github.com/EpicBlackWolfZ/microfat/issues/517), [#523](https://github.com/EpicBlackWolfZ/microfat/issues/523), [#526](https://github.com/EpicBlackWolfZ/microfat/issues/526)
- **Related Specs**: [SPEC Index](../specs/README.md) (`SPEC-0003`), [Architecture Specification](../architecture.md#7-cache-first-auto-dispatch--descriptor-bound-execution), [Security Policy](../../SECURITY.md)
- **Supersedes**: None
- **Superseded By**: None

## Context

A self-dispatching Linux launcher stub must extract and `execve` a selected ELF payload without leaking writable executable memory, blocking on FIFOs, falling victim to pathname TOCTOU/symlink swaps in cache directories, or accidentally escalating privileges under `setuid`/`setgid`/file capabilities (`AT_SECURE`). Prior `v0.1` memfd-always dispatch incurred decompression latency on every cold start when prewarmed cache entries were already present on disk.

## Decision

`microfat` enforces privilege-safe, descriptor-bound execution across both `full` and `minimal` launcher stubs (`cmd/microfat-stub/` and `internal/cache/`):

1. **Privilege & Capability Refusal:** Before opening containers, probing caches, or unpacking payloads, the stub checks Linux `AT_SECURE`, `uid != euid`, `gid != egid`, and file capability xattrs (`security.capability`), failing closed before any application side effect.
2. **Cache-First Auto-Dispatch (`MICROFAT_EXEC_MODE=auto`, default):**
   - Step 1 (Read-Only Lookup): Checks existing candidate cache directories (`$MICROFAT_CACHE_DIR` or `$XDG_CACHE_HOME/microfat`, `~/.cache/microfat`, `/tmp/.microfat-<uid>`). A hit must pass private ownership and mode checks (`0700` dir, regular file, same EUID, no group/other write or special bits) and exact size + SHA-256 verification on the opened `O_RDONLY | O_CLOEXEC | O_NOFOLLOW | O_NONBLOCK` descriptor, then `execve("/proc/self/fd/<fd>")`. Absent or corrupt safe regular entries remain untouched; unsafe entries (symlinks, FIFOs, sockets, wrong owner/mode) fail closed immediately.
   - Step 2 (Sealed Anonymous RAM `memfd_create`): On cache miss (or failed warm exec), streams and hashes the payload into `memfd_create(..., MFD_EXEC | MFD_CLOEXEC | MFD_ALLOW_SEALING)` (retrying without `MFD_EXEC` only on `EINVAL`), applies four mandatory seals (`F_SEAL_WRITE | F_SEAL_SHRINK | F_SEAL_GROW | F_SEAL_SEAL`), closes unsealed descriptors on failure (`ErrMemfdSealingFailed`), and executes `/proc/self/fd/<fd>`.
   - Step 3 (Cold Fallback Materialization): Only on eligible cold `memfd` denial/execution failure (never after extraction corruption or budget refusal), atomically stages `.exec-*.tmp`, fsyncs, sets `0700`, renames, verifies once on a canonical read-only descriptor, and hands that verified descriptor directly to `execve`.
3. **Explicit Modes & Environment Separation:** Explicit `MICROFAT_EXEC_MODE=memfd` bypasses cache discovery and fails closed; explicit `cache` materializes/repairs only safe regular entries. `MICROFAT_VERIFY_CACHE` is always ignored. In `v0.5.0`, `MICROFAT_EXEC_MODE` remains caller input policy untouched across `execve` while `MICROFAT_DISPATCH_MODE` (`memfd`/`cache`) is output-only telemetry ([#516](https://github.com/EpicBlackWolfZ/microfat/issues/516)), and `noexec` cache mounts are detected via `fstatfs` `ST_NOEXEC` ([#517](https://github.com/EpicBlackWolfZ/microfat/issues/517)).

### Alternatives Rejected

- **Executing `/tmp` extracted files by pathname after hashing:** Rejected due to pathname-replacement TOCTOU races between verification and `execve` and leftover temp clutter when `memfd` suffices.
- **Unconditional `memfd` without warm-cache lookup:** Rejected because prewarmed or cold-fallback workloads should avoid repeated decompression while preserving sealed `memfd` for clean cold runs.

## Consequences & Tradeoffs

- **Security Boundary:** Sealed `memfd` ensures backing RAM immutability, while descriptor-bound cache execution pins `/proc/self/fd/<fd>` to the verified inode (trusted same-UID writers remain in scope for disk caches per [Security Policy](../../SECURITY.md)).
- **Compatibility:** Supports restrictive seccomp environments by falling back cleanly in `auto` mode and older Linux kernels lacking `MFD_EXEC`.

## Rollback

Operators can switch immediately between dispatch mechanisms via `MICROFAT_EXEC_MODE=memfd` (strict zero-disk execution) or `MICROFAT_EXEC_MODE=cache` (bypass `memfd_create`), or supply a dedicated private directory via `MICROFAT_CACHE_DIR` without altering binaries.
