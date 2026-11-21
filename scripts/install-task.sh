#!/usr/bin/env bash
# Install the pinned Task CLI via Go module checksum verification without
# relying on unauthenticated GitHub Releases downloads.
set -euo pipefail
IFS=$'\n\t'
# shellcheck source=../.config/tool-versions.env
source "$(dirname -- "${BASH_SOURCE[0]}")/../.config/tool-versions.env"
: "${TASK_VERSION:?missing shared Task pin}"
dest=${1:?usage: install-task.sh DESTINATION_DIRECTORY [EXPECTED_VERSION]}
expected=${2:-${TASK_VERSION}}
[[ ${TASK_VERSION} =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
    printf 'Expected an exact Task version, got %s\n' "${TASK_VERSION}" >&2
    exit 1
}
[[ ${expected} == "${TASK_VERSION}" ]] || {
    printf 'Requested Task version %s does not match shared pin %s\n' "${expected}" "${TASK_VERSION}" >&2
    exit 1
}
mkdir -p "${dest}"
dest=$(cd -- "${dest}" && pwd)
if [[ ! -x ${dest}/task ]]; then
    (
        cd -- "${dest}"
        CGO_ENABLED=0 GOBIN="${dest}" go install -trimpath -ldflags='-s -w' "github.com/go-task/task/v3/cmd/task@v${TASK_VERSION}"
    )
fi
observed=$("${dest}/task" --version)
[[ ${observed} == "${TASK_VERSION}" || ${observed} == "Task version: v${TASK_VERSION}"* || ${observed} == "v${TASK_VERSION}" ]] || {
    printf 'Expected Task %s, got %s\n' "${TASK_VERSION}" "${observed}" >&2
    exit 1
}
printf 'Installed Task %s in %s\n' "${TASK_VERSION}" "${dest}"
