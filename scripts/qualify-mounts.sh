#!/usr/bin/env bash
# Source-built native qualification; no release authentication or host policy changes.
set -euo pipefail
IFS=$'\n\t'

: "${GO:=go}" "${MOUNT_TESTS:=auto}" "${MOUNT_BACKEND:=userns}" "${MOUNT_OUTPUT:=.work/mount-qualification}"
case "${MOUNT_TESTS}" in auto|required) ;; *) printf 'MOUNT_TESTS must be auto or required.\n' >&2; exit 1 ;; esac
case "${MOUNT_BACKEND}" in userns|sudo) ;; *) printf 'MOUNT_BACKEND must be userns or sudo.\n' >&2; exit 1 ;; esac
mkdir -p -- "${MOUNT_OUTPUT}"
output_root=$(cd -- "${MOUNT_OUTPUT}" && pwd)
run_dir=$(mktemp -d "${output_root}/run-XXXXXX")
export MICROFAT_MOUNT_TESTS="${MOUNT_TESTS}"
export MICROFAT_MOUNT_BACKEND="${MOUNT_BACKEND}"
export MICROFAT_MOUNT_OUTPUT="${run_dir}"
printf 'Mount qualification evidence: %s\n' "${run_dir}"
"${GO}" test -race -v ./tests/e2e -run '^TestMount(Qualification|HarnessContracts|EvidenceCompleteness)$' -count=1 \
    2>&1 | tee "${run_dir}/tests.log"
printf 'Review %s/summary.json; auto-mode skips mean incomplete qualification.\n' "${run_dir}"
