#!/usr/bin/env bash
# CI-only fixture: mount noexec in a private namespace and run as the original
# ordinary user. This never changes the host's mounts or installer policy.
set -euo pipefail
IFS=$'\n\t'

(( EUID != 0 )) || { printf 'Run qualification as an ordinary user.\n' >&2; exit 1; }
go_bin=$(command -v "${GO:-go}")
owner=$(id -un)
parent=$(mktemp -d)
trap 'rmdir -- "${parent}"' EXIT
sudo -n unshare --mount --propagation private -- bash -s -- "${parent}" "${owner}" "${go_bin}" "${PATH}" <<'NAMESPACE'
set -euo pipefail
IFS=$'\n\t'
mount -t tmpfs -o noexec,nodev,nosuid,mode=1777 tmpfs "$1"
runuser -u "$2" -- env "PATH=$4" "MICROFAT_TEST_NOEXEC_PARENT=$1" "$3" test -race -v \
    ./cmd/microfat-install -run '^TestBootstrapTrustBoundary/noexec-staging$' -count=1
NAMESPACE
