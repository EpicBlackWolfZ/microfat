#!/usr/bin/env bash
# Install the reviewed Linux x86-64 Gitleaks engine for the secret detection job.
set -euo pipefail
IFS=$'\n\t'
# shellcheck source=../.config/tool-versions.env
source "$(dirname -- "${BASH_SOURCE[0]}")/../.config/tool-versions.env"
: "${GITLEAKS_LINUX_AMD64:?missing shared Gitleaks pin}"
version=${GITLEAKS_LINUX_AMD64%@*}
digest=${GITLEAKS_LINUX_AMD64#*@}
destination=${1:?pass an existing writable executable directory}
platform=$(uname -sm)
if [[ "${platform}" != 'Linux x86_64' ]]; then
    echo 'This pinned CI Gitleaks installer requires Linux x86-64.' >&2
    exit 1
fi
temporary=$(mktemp -d)
trap 'rm -rf -- "${temporary}"' EXIT
archive="${temporary}/gitleaks.tar.gz"
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-time 120 \
    "https://github.com/gitleaks/gitleaks/releases/download/${version}/gitleaks_${version#v}_linux_x64.tar.gz" \
    --output "${archive}"
if ! printf '%s  %s\n' "${digest}" "${archive}" | sha256sum --check --status; then
    printf 'Gitleaks %s archive failed pinned SHA-256 verification.\n' "${version}" >&2
    exit 1
fi
tar -xzf "${archive}" -C "${temporary}" gitleaks
install -m 0755 "${temporary}/gitleaks" "${destination}/gitleaks"
"${destination}/gitleaks" version
