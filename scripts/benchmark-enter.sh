#!/usr/bin/env bash
# Enter a private manager leaf before dropping privileges. Child migrations then share a delegated ancestor.
set -euo pipefail
IFS=$'\n\t'

if [[ "${EUID}" != 0 ]]; then
  exec sudo --preserve-env env "PATH=${PATH}" bash "$0" "$@"
fi
root="${1:?delegated root required}"
shift
[[ "${root}" == /sys/fs/cgroup/microfat-ci-* && "${root}" != *..* ]]
cgroup_fstype="$(stat -fc %T "${root}")"
test "${cgroup_fstype}" = cgroup2fs
printf '%s\n' "$$" > "${root}/manager/cgroup.procs"
exec setpriv --reuid="${SUDO_UID:?original uid required}" --regid="${SUDO_GID:?original gid required}" --init-groups "$@"
