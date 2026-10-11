#!/usr/bin/env bash
# Delegate only a transient, benchmark-owned subtree; leave the workflow runner outside it.
set -euo pipefail
IFS=$'\n\t'

output="${1:?output controls JSON required}"
run_id="${GITHUB_RUN_ID:-$$}"
run_attempt="${GITHUB_RUN_ATTEMPT:-1}"
[[ "${run_id}" =~ ^[1-9][0-9]*$ && "${run_attempt}" =~ ^[1-9][0-9]*$ ]]
root="/sys/fs/cgroup/microfat-ci-${run_id}-${run_attempt}-$$"
mkdir -p "$(dirname -- "${output}")"
cleanup_binary="$(cd -- "$(dirname -- "${output}")" && pwd)/benchmark-cleanup"
# Build before delegation; cancellation teardown never needs a compiler or download.
"${GO:-go}" build -o "${cleanup_binary}" ./internal/cmd/benchmark-cleanup
cgroup_fstype="$(stat -fc %T /sys/fs/cgroup)"
test "${cgroup_fstype}" = cgroup2fs
sudo mkdir "${root}"
registered=0
cleanup() {
    local status=$?
    trap - EXIT
    if [[ "${registered}" == 1 ]]; then
        sudo "${cleanup_binary}" cleanup "${root}" "${run_id}" "${run_attempt}" || true
    else
        sudo rmdir "${root}/manager" 2>/dev/null || true
        sudo rmdir "${root}" 2>/dev/null || true
    fi
    exit "${status}"
}
# Own rollback until both output and workflow environment publication succeed.
trap cleanup EXIT
printf '+cpu +memory\n' | sudo tee "${root}/cgroup.subtree_control" >/dev/null
current_uid="$(id -u)"
current_gid="$(id -g)"
sudo chown "${current_uid}:${current_gid}" "${root}" "${root}/cgroup.procs" "${root}/cgroup.subtree_control"
sudo mkdir "${root}/manager"
"${GO:-go}" run ./internal/cmd/benchmark-tools controls "${root}" > "${output}"
sudo "${cleanup_binary}" register "${root}" "${run_id}" "${run_attempt}" "${current_uid}"
registered=1
printf '%s\n' "${root}"
printf 'MICROFAT_BENCH_CGROUP_ROOT=%s\nMICROFAT_BENCH_CGROUP_VERSION=v2\nMICROFAT_BENCH_REQUIRE_CONTROLS=1\nMICROFAT_BENCH_CLEANUP_BINARY=%s\nMICROFAT_BENCH_RUN_ID=%s\nMICROFAT_BENCH_RUN_ATTEMPT=%s\n' \
    "${root}" "${cleanup_binary}" "${run_id}" "${run_attempt}" >> "${GITHUB_ENV:-/dev/null}"
# The workflow now owns removal of the successfully exported delegation.
trap - EXIT
