# SPEC-0003: Launcher Execution, Privilege Refusal, Sealed `memfd` & Cache Verification

- **Spec ID**: SPEC-0003
- **Status**: Normative
- **Date**: 2026-11-28
- **Related ADRs**: [`ADR-0004`](../adr/ADR-0004-sealed-memfd-cache-first-dispatch-and-privilege-refusal.md), [`ADR-0011`](../adr/ADR-0011-observability-runtimeinfo-and-benchmark-methodology.md), [`ADR-0013`](../adr/ADR-0013-native-rust-launcher-stubs-and-go-stub-retirement.md)
- **Related Issues**: [#516](https://github.com/EpicBlackWolfZ/microfat/issues/516), [#517](https://github.com/EpicBlackWolfZ/microfat/issues/517), [#520](https://github.com/EpicBlackWolfZ/microfat/issues/520), [#527](https://github.com/EpicBlackWolfZ/microfat/issues/527), [#528](https://github.com/EpicBlackWolfZ/microfat/issues/528)
- **Verified By**: [`internal/cache`](../../internal/cache/cache_test.go), [`cmd/microfat-stub`](../../cmd/microfat-stub/cache_first_linux_test.go), [`internal/runtimequalify`](../../internal/runtimequalify/validate.go), [`tests/e2e/cache_first_test.go`](../../tests/e2e/cache_first_test.go), `LAW-CACHE-01`, `LAW-LEAN-05`

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in RFC 2119.

## Scope & Terminology

This specification defines runtime self-dispatch semantics across Go ([`cmd/microfat-stub`](../../cmd/microfat-stub/exec_linux.go), [`internal/cache`](../../internal/cache/cache_unix.go), [`internal/memfd`](../../internal/memfd/memfd_linux.go)) and Rust ([`experiments/v3lab/rust-stub/src/main.rs`](../../experiments/v3lab/rust-stub/src/main.rs)) launchers, covering security boundaries, `auto`/`memfd`/`cache` state transitions, `memfd` sealing, descriptor-bound cache verification, and minimal vs full stub profiles.

## Normative Requirements

- `REQ-0003-01`: Before reading environment configuration or accessing cache storage, the launcher SHALL refuse execution fail-closed when `getuid() != geteuid()`, `getgid() != getegid()`, ELF auxiliary vector `AT_SECURE != 0`, or `/proc/self/exe` carries `security.capability` extended attributes (or capability inspection is inconclusive).
- `REQ-0003-02`: In default `MICROFAT_EXEC_MODE=auto` (or unset), the launcher SHALL execute the deterministic state machine:
  1. Perform read-only warm cache discovery and descriptor-bound verification without creating directories, repairing modes, extracting payloads, or unlinking corrupt safe regular entries.
  2. On a verified warm hit, `execve` `/proc/self/fd/<fd>`. If warm `execve` fails, try sealed `memfd` once without retrying or rewriting cache.
  3. On a miss, attempt in-memory extraction to `memfd_create("microfat", MFD_CLOEXEC|MFD_EXEC)` (retrying without `MFD_EXEC` strictly on `EINVAL`, never on `EPERM`/`EACCES`), verify uncompressed SHA-256 and exact length, apply `F_SEAL_WRITE|F_SEAL_SHRINK|F_SEAL_GROW|F_SEAL_SEAL`, and `execve` `/proc/self/fd/<fd>`.
  4. On an eligible `memfd` creation, sealing, or `execve` failure, fall back to atomic verified cache materialization (`.exec-*.tmp` + `fsync` + `renameat` + verified descriptor `execve`). Source/dictionary corruption, decompression failures, or memory-budget exhaustion SHALL terminate immediately without cache materialization.
- `REQ-0003-03`: Explicit `MICROFAT_EXEC_MODE=memfd` SHALL bypass cache discovery and fail closed on `memfd` failure; explicit `MICROFAT_EXEC_MODE=cache` SHALL bypass `memfd` and materialize or replace only safe regular entries. `MICROFAT_VERIFY_CACHE` SHALL ALWAYS be ignored and SHALL NOT disable SHA-256 or permission checks.
- `REQ-0003-04`: Cache directories SHALL be opened with `O_RDONLY|O_DIRECTORY|O_NOFOLLOW|O_CLOEXEC` and verified via `fstat` (`S_ISDIR`, `uid == geteuid()`, `(mode & 0o022) == 0`) and Linux `fstatfs`/`fstatvfs` (`ST_NOEXEC` clear, [#517](https://github.com/EpicBlackWolfZ/microfat/issues/517)). Automatic discovery SHALL skip `noexec` mounts prior to `fchmod(0o700)` tightening without mutating permissions; explicit `MICROFAT_CACHE_DIR` / `--cache-dir` paths on a `noexec` mount SHALL fail fast (`ErrCacheDirNoExec`) prior to payload extraction.
- `REQ-0003-05`: Cache entries SHALL be opened relative to the validated directory FD via `openat` with `O_RDONLY|O_NOFOLLOW|O_NONBLOCK|O_CLOEXEC`, verified on the opened descriptor (`S_ISREG`, `uid == geteuid()`, `(mode & 0o6022) == 0`, exact `st_size`, owner execute bit on warm lookup, and streaming SHA-256), and executed via `/proc/self/fd/<fd>`. Symlinks, FIFOs/devices, foreign-owned entries, or unsafe permissions SHALL terminate dispatch without repair, deletion, or fallback.
- `REQ-0003-06`: Launchers SHALL preserve caller `MICROFAT_EXEC_MODE` untouched (or unset) across `execve`, strip spoofed internal `MICROFAT_*` child environment variables on entry, and emit `MICROFAT_DISPATCH_MODE` (`memfd` or `cache`) strictly as an output-only realized-mechanism telemetry variable ([#516](https://github.com/EpicBlackWolfZ/microfat/issues/516)).
- `REQ-0003-07`: Both full and minimal stub profiles SHALL enforce identical dispatch, integrity, and security guarantees; minimal stubs SHALL reject `--microfat:*` meta-commands fail-closed with an actionable `--microfat:info` / `microfat inspect` / `--stub-profile=full` remediation hint ([#520](https://github.com/EpicBlackWolfZ/microfat/issues/520)).

## Formal Laws & Invariants

- `LAW-CACHE-01`: Safe cache metadata predicates and descriptor validation invariants ([`checkCacheMetadataFields`](../../internal/cache/metadata.go), [`checkCacheEntryFields`](../../internal/cache/metadata.go), called by [`internal/cache/cache_unix.go`](../../internal/cache/cache_unix.go)) reject non-regular files, symlinks, foreign UIDs, and `0o6022` mode bits prior to execution (`task verify-cache-metadata`).
- `LAW-LEAN-05`: Formal dispatch state machine ([`formal/lean/Microfat/DispatchState.lean`](../../formal/lean/Microfat/DispatchState.lean)) proving mode separation (`auto`/`memfd`/`cache`), 4-seal monotonicity, unsafe-entry fail-closed termination, single-retry bound, and corruption/budget non-materialization (`task verify-lean`).

## Traceability Matrix

| ID | Description | Implementation | Verification (Tests / Proofs) |
| :--- | :--- | :--- | :--- |
| `REQ-0003-01` | Privilege & capability refusal | [`cmd/microfat-stub/privilege_linux.go`](../../cmd/microfat-stub/privilege_linux.go), [`experiments/v3lab/rust-stub/src/main.rs`](../../experiments/v3lab/rust-stub/src/main.rs) | [`cmd/microfat-stub/privilege_linux_test.go`](../../cmd/microfat-stub/privilege_linux_test.go), [`tests/e2e/privilege_test.go`](../../tests/e2e/privilege_test.go) |
| `REQ-0003-02` | Cache-first `auto` state machine & sealed `memfd` | [`cmd/microfat-stub/exec_linux.go`](../../cmd/microfat-stub/exec_linux.go), [`internal/memfd/memfd_linux.go`](../../internal/memfd/memfd_linux.go) | [`cmd/microfat-stub/cache_first_linux_test.go`](../../cmd/microfat-stub/cache_first_linux_test.go), [`tests/e2e/cache_first_test.go`](../../tests/e2e/cache_first_test.go) |
| `REQ-0003-03` | Explicit `memfd`/`cache` & ignored `MICROFAT_VERIFY_CACHE` | [`cmd/microfat-stub/exec_linux.go`](../../cmd/microfat-stub/exec_linux.go) | [`tests/e2e/memfd_cache_test.go`](../../tests/e2e/memfd_cache_test.go), [`tests/e2e/cache_security_test.go`](../../tests/e2e/cache_security_test.go) |
| `REQ-0003-04` | Pinned cache dir validation & `ST_NOEXEC` detection | [`internal/format/format.go`](../../internal/format/format.go), [`internal/format/cache_unix.go`](../../internal/format/cache_unix.go), [`internal/runtimequalify/validate.go`](../../internal/runtimequalify/validate.go) | [`internal/format/format_test.go`](../../internal/format/format_test.go), [`tests/e2e/mount_layout_test.go`](../../tests/e2e/mount_layout_test.go) |
| `REQ-0003-05` | Descriptor-bound `openat` & unsafe entry refusal | [`internal/cache/cache_unix.go`](../../internal/cache/cache_unix.go), [`experiments/v3lab/rust-stub/src/main.rs`](../../experiments/v3lab/rust-stub/src/main.rs) | [`internal/cache/safety_test.go`](../../internal/cache/safety_test.go), [`tests/e2e/cache_fifo_test.go`](../../tests/e2e/cache_fifo_test.go) |
| `REQ-0003-06` | `MICROFAT_EXEC_MODE` vs `MICROFAT_DISPATCH_MODE` isolation | [`cmd/microfat-stub/exec_linux.go`](../../cmd/microfat-stub/exec_linux.go), [`experiments/v3lab/rust-stub/src/main.rs`](../../experiments/v3lab/rust-stub/src/main.rs) | [`tests/e2e/rust_stub_parity_test.go`](../../tests/e2e/rust_stub_parity_test.go), [`tests/e2e/running_image_test.go`](../../tests/e2e/running_image_test.go) |
| `REQ-0003-07` | Full vs minimal meta-command profile routing & hint | [`cmd/microfat-stub/meta_minimal.go`](../../cmd/microfat-stub/meta_minimal.go), [`cmd/microfat-stub/meta_full.go`](../../cmd/microfat-stub/meta_full.go) | [`cmd/microfat-stub/minimal_test.go`](../../cmd/microfat-stub/minimal_test.go), [`tests/e2e/stub_profile_e2e_test.go`](../../tests/e2e/stub_profile_e2e_test.go) |
| `LAW-CACHE-01` | Cache descriptor metadata safety | [`internal/cache/metadata.go`](../../internal/cache/metadata.go) | `task verify-cache-metadata` |
| `LAW-LEAN-05` | `DispatchState.lean` transition model | [`formal/lean/Microfat/DispatchState.lean`](../../formal/lean/Microfat/DispatchState.lean) | `task verify-lean` |
