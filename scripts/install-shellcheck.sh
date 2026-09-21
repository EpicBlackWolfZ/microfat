#!/usr/bin/env bash
# Install the reviewed Linux x86-64 ShellCheck build for CI lint/release gates.
set -euo pipefail
IFS=$'\n\t'
SHELLCHECK_VERSION=0.11.0
SHELLCHECK_SHA256=8c3be12b05d5c177a04c29e3c78ce89ac86f1595681cab149b65b97c4e227198
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
    "https://github.com/koalaman/shellcheck/releases/download/v${SHELLCHECK_VERSION}/shellcheck-v${SHELLCHECK_VERSION}.linux.x86_64.tar.xz" \
    -o "${SHELLCHECK_TEMP}/shellcheck.tar.xz"
printf '%s  %s\n' "${SHELLCHECK_SHA256}" "${SHELLCHECK_TEMP}/shellcheck.tar.xz" | sha256sum --check --status
tar -xJf "${SHELLCHECK_TEMP}/shellcheck.tar.xz" -C "${SHELLCHECK_TEMP}" "shellcheck-v${SHELLCHECK_VERSION}/shellcheck"
install -m 0755 "${SHELLCHECK_TEMP}/shellcheck-v${SHELLCHECK_VERSION}/shellcheck" "${SHELLCHECK_DEST}/shellcheck"
"${SHELLCHECK_DEST}/shellcheck" --version
