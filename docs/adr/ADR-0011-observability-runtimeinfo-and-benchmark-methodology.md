# ADR-0011: Observability (`runtimeinfo`), Disabled-Telemetry Overhead Contract & Benchmark Methodology

- **Status**: Accepted (Retroactive — v0.2.3–v0.4)
- **Date**: 2026-11-27
- **Deciders**: Project Maintainer (@EpicBlackWolfZ)
- **Related Issues**: [#182](https://github.com/EpicBlackWolfZ/microfat/issues/182), [#472](https://github.com/EpicBlackWolfZ/microfat/issues/472), [#516](https://github.com/EpicBlackWolfZ/microfat/issues/516), [#523](https://github.com/EpicBlackWolfZ/microfat/issues/523), [#526](https://github.com/EpicBlackWolfZ/microfat/issues/526), [#538](https://github.com/EpicBlackWolfZ/microfat/issues/538)
- **Related Specs**: [SPEC Index](../specs/README.md) (`SPEC-0009`), [Runtime Metadata Guide](../runtimeinfo.md), [Benchmark Guide](../benchmarks/README.md)
- **Supersedes**: None
- **Superseded By**: None

## Context

In-process observability and performance benchmarking of self-dispatching binaries carry two classic hazards: (1) exposing launcher environment hints in a way that either bloats application dependency graphs or misleads operators into treating untrusted environment strings as verified runtime guarantees or cryptographic identity, and (2) conflating sub-microsecond parser microbenchmarks or noisy hosted VM comparisons with hardware-certified production speedups.

## Decision

`microfat` defines strict separation between read-only metadata inspection, zero-cost disabled telemetry, and reproducible comparative benchmarking:

1. **Standard-Library-Only `runtimeinfo` Accessor (`v0.4.0`):**
   - `runtimeinfo.Read()` depends exclusively on the Go standard library (enforced by `TestCoreDependsOnlyOnStandardLibrary`), performs no cgroup discovery or tuning mutation, and explicitly separates untrusted `Launcher` environment hints, verbatim `Configured` environment settings, and `Observed` `runtime/metrics` values (`GOMEMLIMIT`, `GOMAXPROCS`, `GOGC`), each tagged with explicit `Status` (`present`, `missing`, `invalid`, `unsupported`, `unavailable`) and `Source`.
   - Third-party Prometheus and OpenTelemetry examples reside in isolated nested Go modules (`examples/runtimeinfo/`), never leaking SDKs into the core library (`task test-runtimeinfo-examples`).
2. **Disabled-Telemetry Contract (`MICROFAT_LOG` / `MICROFAT_DEBUG`):** With telemetry unset, `0`, or `false`, launchers skip runtime diagnostic formatting/JSON encoding and emit zero diagnostic stderr bytes (except fatal errors), while `--microfat:info` and `MICROFAT_LOG=json` share the exact same resolved tuning logic as live execution.
3. **Paired Comparative Benchmark Methodology (`microfat benchmark`, `benchmarks/`):**
   - Evaluates end-to-end startup and sustained HTTP server workloads using pinned Fortio load (`v1.76.0`), isolated cgroups (`pidfd` + `cgroup.kill` teardown outside delegation), randomized trial schedules, paired-block bootstrap/sign statistics, and SHA-256-sealed evidence archives.
   - Distinguishes isolated parser microbenchmarks from full cold/warm process startup and marks shared hosted runner evidence cleanly as `release_eligible=false` (comparative gate only, never claiming dedicated-hardware certification without dedicated-host governor/topology evidence).

### Alternatives Rejected

- **Adding Prometheus/OTel dependencies directly to root `go.mod`:** Rejected to avoid inflating downstream user build graphs and vulnerability scan surface.
- **Uncontrolled single-run wall-clock scripts without warm/cold cgroup isolation or negative-result retention:** Rejected because filesystem page-cache and frequency drift produce misleading regression/speedup claims (`docs/hosted-p99-investigation-results.md`).

## Consequences & Tradeoffs

- **Trust Clarity:** Applications inspecting `runtimeinfo` know unambiguously that `Launcher` hints can be stale/forged whereas `Observed` reflects active Go runtime metrics, preventing unsafe trust decisions based on `MICROFAT_ORIGINAL_EXE` or digests.
- **Benchmark Discipline:** Publishing `release_eligible=false` alongside raw hosted bundles keeps performance claims honest while enabling reproducible CI smoke/regression gating.

## Rollback

Applications can ignore `runtimeinfo` with zero overhead (since it has no `init()` side effects), disable launcher telemetry via unset/`MICROFAT_DEBUG=0`, or omit `--bench` / `benchmarks` execution independently of packaged binary behavior.
