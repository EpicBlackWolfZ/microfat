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
# Write the complete case manifest before invoking Go, including unsupported hosts
# and toolchain failures. Its parity with the Go enumeration is regression tested.
expected=(controls/explicit-qemu controls/binfmt-raw)
for storage in disk memfd; do
    for policy in cloexec keep; do
        expected+=("controls/arm64/${storage}/${policy}")
    done
    expected+=("controls/native/${storage}/cloexec")
done
for version in 1 2; do
    for profile in full minimal; do
        for codec in none-dictfalse lz4-dictfalse zstd-dictfalse zstd-dicttrue; do
            for mode in memfd cache-cold cache-warm auto lifecycle; do
                expected+=("v${version}-${profile}-${codec}/${mode}")
            done
        done
    done
done
write_initial_summary() {
    local reason=${1} separator='' id
    printf '{"schema":1,"status":"incomplete","reason":"%s","expected":[' "${reason}"
    for id in "${expected[@]}"; do
        printf '%s"%s"' "${separator}" "${id}"
        separator=','
    done
    printf '],"results":[],"outcomes":{}}\n'
}
write_initial_summary 'qualification has not started' > "${run_dir}/summary.json"
host_os=$(uname -s)
host_arch=$(uname -m)
if [[ "${host_os}" != Linux || "${host_arch}" != x86_64 ]]; then
    write_initial_summary 'prerequisite unavailable: requires native Linux amd64' > "${run_dir}/summary.json"
    printf 'Incomplete QEMU qualification: host %s/%s requires native Linux amd64. Evidence: %s\n' \
        "${host_os}" "${host_arch}" "${run_dir}" | tee "${run_dir}/tests.log"
    if [[ "${QEMU_TESTS}" == required ]]; then
        exit 1
    fi
    exit 0
fi
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
