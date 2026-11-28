# SPEC-0009: CLI Interface, Exit Codes, Telemetry & Diagnostics Contract

- **Spec ID**: SPEC-0009
- **Status**: Normative
- **Date**: 2026-11-28
- **Related ADRs**: [`ADR-0004`](../adr/ADR-0004-sealed-memfd-cache-first-dispatch-and-privilege-refusal.md), [`ADR-0011`](../adr/ADR-0011-observability-runtimeinfo-and-benchmark-methodology.md), [`ADR-0012`](../adr/ADR-0012-engineering-governance-and-docs-as-code.md)
- **Related Issues**: [#182](https://github.com/EpicBlackWolfZ/microfat/issues/182), [#472](https://github.com/EpicBlackWolfZ/microfat/issues/472), [#516](https://github.com/EpicBlackWolfZ/microfat/issues/516), [#517](https://github.com/EpicBlackWolfZ/microfat/issues/517), [#520](https://github.com/EpicBlackWolfZ/microfat/issues/520), [#527](https://github.com/EpicBlackWolfZ/microfat/issues/527)
- **Verified By**: [`cmd/microfat`](../../cmd/microfat/doctor_test.go), [`internal/format`](../../internal/format/diagnostics_test.go), [`runtimeinfo`](../../runtimeinfo/runtimeinfo_test.go), [`tests/e2e`](../../tests/e2e/runtimeinfo_test.go), `LAW-DIAG-01`

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in RFC 2119.

## Scope & Terminology

This specification defines CLI subcommand contracts (`detect`, `pack`, `inspect`/`info`, `verify`, `trim`, `optimize`, `prewarm`, `doctor`, `update`, `version`), exit codes, structured telemetry schemas (`DispatchTelemetry`, `ErrorTelemetry`, `PrewarmTelemetry`), `DiagnoseError` remediation hints, and `NO_COLOR`/non-TTY output behavior.

## Normative Requirements

- `REQ-0009-01`: `microfat` CLI subcommands SHALL validate arguments/flags fail-closed, support deterministic `--json` output on diagnostic and metadata commands (`detect`, `inspect`/`info`, `verify`, `doctor`, `version`), return exit code `0` solely on verified success (`microfat verify --json` MUST emit JSON before exiting nonzero on corruption), and fall back cleanly to `runtime/debug.ReadBuildInfo` module version when ldflags `Version` is `dev` (`#520`).
- `REQ-0009-02`: `microfat doctor` SHALL separate host prerequisites from process execution (`Scope = "prerequisites"`, `Execution.Status = "not_tested"`), accurately preserve distinct kernel errors (`EPERM` vs `EACCES` vs `ENOSYS`), evaluate `memfd_create` + `F_ADD_SEALS` (`TargetSeals`) + disk cache writability + Linux `noexec` mount status (`#517`), fail `--strict` cleanly whenever any prerequisite fails, and never pass false-green checks.
- `REQ-0009-03`: `DiagnoseError` and launcher error logging SHALL classify failures by execution stage (`StageMemfdCreate`, `StageMemfdSeal`, `StageCacheDirInit`, `StageCacheCreateTemp`, `StageCacheExtract`, `StageCacheExec`, etc.) and emit actionable hints including `HintCacheDirNoExec`, `HintInsecureCacheDir`, `HintMemfdEPERM`, and `HintTextBusy`.
- `REQ-0009-04`: When both `MICROFAT_LOG` and `MICROFAT_DEBUG` are unset or disabled (`MICROFAT_DEBUG=0`/`false` with empty `MICROFAT_LOG`), launcher telemetry SHALL emit zero stderr bytes and incur zero formatting/JSON allocation overhead; when enabled (`MICROFAT_LOG=json` or `MICROFAT_DEBUG=1`), events SHALL conform strictly to `[microfat] <json>` or `[microfat:debug] ...` schemas.
- `REQ-0009-05`: Telemetry and `runtimeinfo` outputs SHALL faithfully reflect runtime behavior: `MICROFAT_EXEC_MODE` records preserved caller policy, while `MICROFAT_DISPATCH_MODE` and JSON `exec_mode` record the realized mechanism (`memfd` or `cache`, `#516`).
- `REQ-0009-06`: CLI, Task, and developer workflow outputs SHALL respect `NO_COLOR=1`, `COLOR=0`, and non-TTY `[ -t 1 ]` detection cleanly (`internal/lifecycle`, `task test-dx`), keeping [`docs/cli-reference.md`](../cli-reference.md) synchronized via `task lint-docs`.

## Formal Laws & Invariants

- `LAW-DIAG-01`: Prerequisite truth-table verdict consistency (`EvaluatePrerequisites` in [`internal/lifecycle/doctor.go`](../../internal/lifecycle/doctor.go)) and zero-byte disabled telemetry emission invariant ([`tests/e2e/telemetry_timing_test.go`](../../tests/e2e/telemetry_timing_test.go)).

## Traceability Matrix

| ID | Description | Implementation | Verification (Tests / Proofs) |
| :--- | :--- | :--- | :--- |
| `REQ-0009-01` | CLI commands, exit codes & `go install` version fallback | [`cmd/microfat/main.go`](../../cmd/microfat/main.go), [`internal/version/version.go`](../../internal/version/version.go) | [`cmd/microfat/main_test.go`](../../cmd/microfat/main_test.go), [`internal/version/version_test.go`](../../internal/version/version_test.go) |
| `REQ-0009-02` | `microfat doctor` prerequisite report & `--strict` gate | [`cmd/microfat/doctor.go`](../../cmd/microfat/doctor.go), [`internal/lifecycle/doctor.go`](../../internal/lifecycle/doctor.go) | [`cmd/microfat/doctor_remediation_test.go`](../../cmd/microfat/doctor_remediation_test.go), [`cmd/microfat/doctor_test.go`](../../cmd/microfat/doctor_test.go) |
| `REQ-0009-03` | Stage-scoped `DiagnoseError` classification & hints | [`internal/format/diagnostics.go`](../../internal/format/diagnostics.go) | [`internal/format/diagnostics_test.go`](../../internal/format/diagnostics_test.go), [`cmd/microfat-stub/decompression_errno_test.go`](../../cmd/microfat-stub/decompression_errno_test.go) |
| `REQ-0009-04` | Zero disabled telemetry & structured JSON/debug schemas | [`cmd/microfat-stub/telemetry.go`](../../cmd/microfat-stub/telemetry.go), [`experiments/v3lab/rust-stub/src/main.rs`](../../experiments/v3lab/rust-stub/src/main.rs) | [`tests/e2e/telemetry_timing_test.go`](../../tests/e2e/telemetry_timing_test.go), [`cmd/microfat-stub/diagnostic_timer_linux_test.go`](../../cmd/microfat-stub/diagnostic_timer_linux_test.go) |
| `REQ-0009-05` | `runtimeinfo` dispatch vs policy variable parsing | [`runtimeinfo/runtimeinfo.go`](../../runtimeinfo/runtimeinfo.go), [`runtimeinfo/parse.go`](../../runtimeinfo/parse.go) | [`runtimeinfo/runtimeinfo_test.go`](../../runtimeinfo/runtimeinfo_test.go), [`tests/e2e/runtimeinfo_test.go`](../../tests/e2e/runtimeinfo_test.go) |
| `REQ-0009-06` | `NO_COLOR`, TTY detection & CLI reference parity | [`docs/cli-reference.md`](../cli-reference.md), [`internal/dxnet`](../../internal/dxnet/ports_test.go) | `task test-dx`, `task lint-docs` |
| `LAW-DIAG-01` | Prerequisite verdict truth table & telemetry isolation | [`internal/lifecycle/doctor.go`](../../internal/lifecycle/doctor.go) | [`internal/lifecycle/doctor_test.go`](../../internal/lifecycle/doctor_test.go), [`tests/e2e/telemetry_timing_test.go`](../../tests/e2e/telemetry_timing_test.go) |
