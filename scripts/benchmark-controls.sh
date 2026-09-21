#!/usr/bin/env bash
# Delegate only a transient, benchmark-owned subtree; leave the workflow runner outside it.
set -euo pipefail
IFS=$'\n\t'

output="${1:?output controls JSON required}"
root="/sys/fs/cgroup/microfat-ci-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}-$$"
cgroup_fstype="$(stat -fc %T /sys/fs/cgroup)"
test "${cgroup_fstype}" = cgroup2fs
sudo mkdir "${root}"
trap 'sudo rmdir "${root}" 2>/dev/null || true' ERR
printf '+cpu +memory\n' | sudo tee "${root}/cgroup.subtree_control" >/dev/null
current_uid="$(id -u)"
current_gid="$(id -g)"
sudo chown "${current_uid}:${current_gid}" "${root}" "${root}/cgroup.procs" "${root}/cgroup.subtree_control"
sudo mkdir "${root}/manager"
mkdir -p "$(dirname -- "${output}")"
"${GO:-go}" run ./internal/cmd/benchmark-tools controls "${root}" > "${output}"
printf 'MICROFAT_BENCH_CGROUP_ROOT=%s\nMICROFAT_BENCH_CGROUP_VERSION=v2\nMICROFAT_BENCH_REQUIRE_CONTROLS=1\n' \
    "${root}" >> "${GITHUB_ENV:-/dev/null}"
printf '%s\n' "${root}"
