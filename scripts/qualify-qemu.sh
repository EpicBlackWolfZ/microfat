#!/usr/bin/env bash
# Source-built F-only emulation qualification; never alter host registrations.
set -euo pipefail
IFS=$'\n\t'

: "${GO:=go}" "${QEMU_TESTS:=auto}" "${QEMU_BACKEND:=userns}"
: "${QEMU_OUTPUT:=.work/qemu-qualification}" "${QEMU_AARCH64:=}"
case "${QEMU_TESTS}" in auto|required) ;; *) printf 'QEMU_TESTS must be auto or required.\n' >&2; exit 1 ;; esac
case "${QEMU_BACKEND}" in userns|sudo) ;; *) printf 'QEMU_BACKEND must be userns or sudo.\n' >&2; exit 1 ;; esac
mkdir -p -- "${QEMU_OUTPUT}"
output_root=$(cd -- "${QEMU_OUTPUT}" && pwd)
run_dir=$(mktemp -d "${output_root}/run-XXXXXX")
export MICROFAT_QEMU_TESTS="${QEMU_TESTS}"
export MICROFAT_QEMU_BACKEND="${QEMU_BACKEND}"
export MICROFAT_QEMU_AARCH64="${QEMU_AARCH64}"
export MICROFAT_QEMU_OUTPUT="${run_dir}"
# Preserve the actual source delta, including newly introduced fixture files.
git diff --binary HEAD > "${run_dir}/source.patch"
git ls-files --others --exclude-standard -z > "${run_dir}/untracked-files.txt"
if [[ -s "${run_dir}/untracked-files.txt" ]]; then
    tar --null -T "${run_dir}/untracked-files.txt" -czf "${run_dir}/untracked-source.tar.gz"
fi
printf 'QEMU qualification evidence: %s\n' "${run_dir}"
"${GO}" test -race -v ./tests/e2e -run '^TestQemu' -count=1 -timeout=25m 2>&1 | tee "${run_dir}/tests.log"
printf 'Review %s/summary.json; expected limitations are not successful payload launches.\n' "${run_dir}"
