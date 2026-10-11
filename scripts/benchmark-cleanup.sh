#!/usr/bin/env bash
# Run outside the delegation; preserve the measurement step's status and evidence.
set -euo pipefail
IFS=$'\n\t'

if [[ -z "${MICROFAT_BENCH_CGROUP_ROOT:-}" ]]; then
    exit 0
fi
sudo "${MICROFAT_BENCH_CLEANUP_BINARY:?prebuilt cleanup helper required}" cleanup \
    "${MICROFAT_BENCH_CGROUP_ROOT}" "${GITHUB_RUN_ID:-${MICROFAT_BENCH_RUN_ID:?run identity required}}" \
    "${GITHUB_RUN_ATTEMPT:-${MICROFAT_BENCH_RUN_ATTEMPT:?run attempt required}}"
