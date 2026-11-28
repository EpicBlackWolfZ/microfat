# SPEC-0006: Executable Lifecycle Mutations & Advisory Locking

- **Spec ID**: SPEC-0006
- **Status**: Normative
- **Date**: 2026-11-28
- **Related ADRs**: [`ADR-0004`](../adr/ADR-0004-sealed-memfd-cache-first-dispatch-and-privilege-refusal.md), [`ADR-0007`](../adr/ADR-0007-executable-lifecycle-operations-and-advisory-locking.md)
- **Related Issues**: [#365](https://github.com/EpicBlackWolfZ/microfat/issues/365), [#520](https://github.com/EpicBlackWolfZ/microfat/issues/520), [#527](https://github.com/EpicBlackWolfZ/microfat/issues/527), [#528](https://github.com/EpicBlackWolfZ/microfat/issues/528)
- **Verified By**: [`internal/lifecycle`](../../internal/lifecycle/transaction_test.go), [`cmd/microfat-stub`](../../cmd/microfat-stub/trim.go), [`tests/e2e/lifecycle_test.go`](../../tests/e2e/lifecycle_test.go), `LAW-LIFECYCLE-01`

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in RFC 2119.

## Scope & Terminology

This specification defines in-place and out-of-place executable mutation operations (`trim`, `optimize`, `optimize-to`, `specialize`) and cache population (`prewarm`) across [`internal/lifecycle`](../../internal/lifecycle/transaction.go), [`cmd/microfat`](../../cmd/microfat/main.go), and full launcher stubs ([`cmd/microfat-stub`](../../cmd/microfat-stub/optimize.go)).

## Normative Requirements

- `REQ-0006-01`: In-place mutations (`trim`, `optimize`, `specialize`) SHALL serialize concurrent writers via non-blocking or bounded advisory file locks (`flock` / `lockFile` on Unix) prior to reading or transforming containers and SHALL clean up lock descriptors and temporary files on exit or error.
- `REQ-0006-02`: All mutations SHALL write the transformed container or raw ELF payload completely to a same-directory private staging file, call `fsync`, re-verify integrity and target format constraints, and replace the target atomically via `rename`. A failed transformation SHALL leave the original executable untouched.
- `REQ-0006-03`: Out-of-place destination publication (`--output` / `--microfat:optimize-to`) SHALL default to `IntentCreateOnly` so destination files receive deterministic `0o755` ownership-safe permissions rather than copying source-specific ACLs or setuid/setgid bits, whereas in-place replacements (`IntentReplaceExisting`) SHALL preserve non-special existing modes cleanly (`#520`).
- `REQ-0006-04`: `trim` SHALL preserve at least one executable variant matching requested constraints (re-packing remaining variants and shared dictionaries cleanly when supported) and SHALL reject empty or host-incompatible selections before modifying any file.
- `REQ-0006-05`: `optimize` / `--microfat:optimize` SHALL extract the host-optimal verified variant ELF payload directly as a plain native standalone ELF after SHA-256 verification and decompression, stripping stub and unused variant segments cleanly.
- `REQ-0006-06`: `prewarm` SHALL support host-default and cross-target explicit variant materialization cleanly (`--all`, `--level`), SHALL resolve secure writable cache storage (honoring `ST_NOEXEC` rejection on explicit targets), and SHALL publish verified `0o700` cached variants atomically while closing returned file descriptors.

## Formal Laws & Invariants

- `LAW-LIFECYCLE-01`: (*Scheduled [#528](https://github.com/EpicBlackWolfZ/microfat/issues/528)*) Transaction staging, advisory lock release, and `IntentCreateOnly` vs `IntentReplaceExisting` mode-bit stripping invariants in [`internal/lifecycle/transaction.go`](../../internal/lifecycle/transaction.go) (Gobra backfill target).

## Traceability Matrix

| ID | Description | Implementation | Verification (Tests / Proofs) |
| :--- | :--- | :--- | :--- |
| `REQ-0006-01` | Advisory lock acquisition & cleanup | [`internal/lifecycle/transaction_linux.go`](../../internal/lifecycle/transaction_linux.go) | [`internal/lifecycle/transaction_linux_test.go`](../../internal/lifecycle/transaction_linux_test.go) |
| `REQ-0006-02` | Atomic staging, `fsync` & rollback safety | [`internal/lifecycle/transaction.go`](../../internal/lifecycle/transaction.go) | [`internal/lifecycle/transaction_test.go`](../../internal/lifecycle/transaction_test.go), [`tests/e2e/transformation_path_test.go`](../../tests/e2e/transformation_path_test.go) |
| `REQ-0006-03` | `IntentCreateOnly` vs `IntentReplaceExisting` mode rules | [`internal/lifecycle/policy.go`](../../internal/lifecycle/policy.go) | [`internal/lifecycle/policy_test.go`](../../internal/lifecycle/policy_test.go), [`internal/lifecycle/sticky_mode_test.go`](../../internal/lifecycle/sticky_mode_test.go) |
| `REQ-0006-04` | `trim` variant retention & validation | [`cmd/microfat-stub/trim.go`](../../cmd/microfat-stub/trim.go), [`cmd/microfat/main.go`](../../cmd/microfat/main.go) | [`cmd/microfat/trim_path_test.go`](../../cmd/microfat/trim_path_test.go), [`tests/e2e/lifecycle_routing_test.go`](../../tests/e2e/lifecycle_routing_test.go) |
| `REQ-0006-05` | `optimize` plain native ELF specialization | [`cmd/microfat-stub/optimize.go`](../../cmd/microfat-stub/optimize.go), [`cmd/microfat/main.go`](../../cmd/microfat/main.go) | [`tests/e2e/lifecycle_test.go`](../../tests/e2e/lifecycle_test.go), [`tests/e2e/stub_profile_e2e_test.go`](../../tests/e2e/stub_profile_e2e_test.go) |
| `REQ-0006-06` | `prewarm` cache materialization matrix | [`cmd/microfat/prewarm.go`](../../cmd/microfat/prewarm.go) | [`cmd/microfat/prewarm_test.go`](../../cmd/microfat/prewarm_test.go), [`tests/e2e/prewarm_plan_matrix_test.go`](../../tests/e2e/prewarm_plan_matrix_test.go) |
| `LAW-LIFECYCLE-01` | Lifecycle transaction & mode invariants (*#528 backfill*) | [`internal/lifecycle/transaction.go`](../../internal/lifecycle/transaction.go) | Tracked in [`#528`](https://github.com/EpicBlackWolfZ/microfat/issues/528) |
