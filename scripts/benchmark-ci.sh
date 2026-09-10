#!/usr/bin/env bash
# Shared local/CI experiment entry point; interpretation remains in Go.
set -euo pipefail
: "${BENCHMARK_CONFIG:=benchmarks/config/smoke.json}"
: "${BENCHMARK_OUTPUT:=.work/benchmark-ci}"
: "${MICROFAT_BENCH_FORTIO:=$PWD/.work/benchmark-tools/fortio}"
: "${BENCHMARK_BINARY:=$PWD/bin/microfat}"
mkdir -p "$BENCHMARK_OUTPUT"
benchmark_args=(benchmark run --config "$BENCHMARK_CONFIG" --output-dir "$BENCHMARK_OUTPUT"
  --fortio "$MICROFAT_BENCH_FORTIO")
if [[ -n "${BENCHMARK_BASE:-}" ]]; then
  benchmark_args+=(--base-repository "$BENCHMARK_BASE")
fi
benchmark_status=0
"$BENCHMARK_BINARY" "${benchmark_args[@]}" | tee "$BENCHMARK_OUTPUT/latest-bundle.txt" || benchmark_status=$?
benchmark_bundle="$(tail -n 1 "$BENCHMARK_OUTPUT/latest-bundle.txt")"
if [[ -d "$benchmark_bundle" ]]; then
  "$BENCHMARK_BINARY" benchmark verify "$benchmark_bundle"
  if [[ -n "${BENCHMARK_BASE:-}" ]]; then
    benchmark_gate_args=(benchmark gate --input "$benchmark_bundle")
    if [[ -n "${BENCHMARK_CALIBRATION:-}" ]]; then
      benchmark_gate_args+=(--calibration "$BENCHMARK_CALIBRATION")
    fi
    gate_status=0
    "$BENCHMARK_BINARY" "${benchmark_gate_args[@]}" > "$BENCHMARK_OUTPUT/summary.md" || gate_status=$?
    if [[ "$gate_status" != 0 && "${BENCHMARK_HOSTED_RELEASE:-0}" != 1 ]]; then
      benchmark_status="$gate_status"
    fi
  else
    "$BENCHMARK_BINARY" benchmark report --input "$benchmark_bundle" --format markdown > "$BENCHMARK_OUTPUT/summary.md"
  fi
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    cat "$BENCHMARK_OUTPUT/summary.md" >> "$GITHUB_STEP_SUMMARY"
  fi
fi
if [[ "${BENCHMARK_HOSTED_RELEASE:-0}" == 1 && -d "$benchmark_bundle" ]]; then
  "$BENCHMARK_BINARY" benchmark qualify --policy hosted-release --input "$benchmark_bundle" \
    > "$BENCHMARK_OUTPUT/qualification.json" || benchmark_status=$?
fi
exit "$benchmark_status"
