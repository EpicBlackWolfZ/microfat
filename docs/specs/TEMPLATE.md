# SPEC-NNNN: <Title>

- **Spec ID**: SPEC-NNNN
- **Status**: Draft | Normative
- **Date**: YYYY-MM-DD
- **Related ADRs**: `docs/adr/ADR-NNNN-slug.md`
- **Related Issues**: [#NNNN](https://github.com/EpicBlackWolfZ/microfat/issues/NNNN)
- **Verified By**: Unit tests, E2E suites, Formal laws (`LAW-*`)

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in RFC 2119.

## Scope & Terminology

Define the exact subsystem boundary, binary/API/CLI interfaces, threat model assumptions, and terms used below.

## Normative Requirements

- **`REQ-NNNN-01`**: State each observable, testable requirement clearly using RFC 2119 keywords.
- **`REQ-NNNN-02`**: State failure modes and fail-closed guarantees explicitly.

## Formal Laws & Invariants

- **`LAW-NNNN-01`**: State exact mathematical, bounds, or state-machine invariant and link it in [the formal verification ledger](../verification/README.md).

## Traceability Matrix

| ID | Description | Implementation | Verification (Tests / Proofs) |
| :--- | :--- | :--- | :--- |
| `REQ-NNNN-01` | Summary of requirement | `path/to/code.go` | `TestName` / `tests/e2e/...` |
| `LAW-NNNN-01` | Summary of invariant | `path/to/code.go` | Verifier (`task verify-...`) |
