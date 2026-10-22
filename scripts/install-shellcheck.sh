#!/usr/bin/env bash
# Install the reviewed Linux x86-64 ShellCheck build for CI lint/release gates.
set -euo pipefail
IFS=$'\n\t'
# shellcheck source=.config/tool-versions.env
source "$(dirname -- "${BASH_SOURCE[0]}")/../.config/tool-versions.env"
SHELLCHECK_VERSION=${SHELLCHECK_LINUX_AMD64%@*}
SHELLCHECK_SHA256=${SHELLCHECK_LINUX_AMD64#*@}
SHELLCHECK_DEST="${1:?pass an existing writable executable directory}"
SHELLCHECK_TEMP="$(mktemp -d)"
trap 'rm -rf "${SHELLCHECK_TEMP}"' EXIT
SHELLCHECK_ARCH="$(uname -m)"
SHELLCHECK_OS="$(uname -s)"
if [ "${SHELLCHECK_ARCH}" != x86_64 ] || [ "${SHELLCHECK_OS}" != Linux ]; then
    echo 'This pinned CI ShellCheck installer requires Linux x86-64' >&2
    exit 1
fi
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-time 120 \
    "https://github.com/koalaman/shellcheck/releases/download/${SHELLCHECK_VERSION}/shellcheck-${SHELLCHECK_VERSION}.linux.x86_64.tar.xz" \
    -o "${SHELLCHECK_TEMP}/shellcheck.tar.xz"
printf '%s  %s\n' "${SHELLCHECK_SHA256}" "${SHELLCHECK_TEMP}/shellcheck.tar.xz" | sha256sum --check --status
tar -xJf "${SHELLCHECK_TEMP}/shellcheck.tar.xz" -C "${SHELLCHECK_TEMP}" "shellcheck-${SHELLCHECK_VERSION}/shellcheck"
install -m 0755 "${SHELLCHECK_TEMP}/shellcheck-${SHELLCHECK_VERSION}/shellcheck" "${SHELLCHECK_DEST}/shellcheck"
"${SHELLCHECK_DEST}/shellcheck" --version
