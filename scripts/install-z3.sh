#!/usr/bin/env bash
# Install pinned Z3 theorem prover for CI and local formal SMT verification.
set -euo pipefail
IFS=$'\n\t'

# shellcheck source=../.config/tool-versions.env
source "$(dirname -- "${BASH_SOURCE[0]}")/../.config/tool-versions.env"
: "${Z3_LINUX_AMD64:?missing shared Z3 pin}"

z3_dest="${1:?usage: install-z3.sh DESTINATION_DIRECTORY}"
host_platform="$(uname -sm)"
if [[ "${host_platform}" != 'Linux x86_64' ]]; then
    echo 'This pinned Z3 installer requires Linux x86-64' >&2
    exit 1
fi

z3_tag="${Z3_LINUX_AMD64%@*}"
z3_ver="${z3_tag#v}"
z3_sha="${Z3_LINUX_AMD64#*@}"

tmp_dir="$(mktemp -d)"
trap 'rm -rf -- "${tmp_dir}"' EXIT

curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.3 --max-time 180 \
    "https://github.com/Z3Prover/z3/releases/download/z3-${z3_ver}/z3-${z3_ver}-x64-glibc-2.39.zip" \
    -o "${tmp_dir}/z3.zip"
printf '%s  %s\n' "${z3_sha}" "${tmp_dir}/z3.zip" | sha256sum --check --status
unzip -q "${tmp_dir}/z3.zip" -d "${tmp_dir}"

mkdir -p -- "${z3_dest}"
install -m 0755 "${tmp_dir}/z3-${z3_ver}-x64-glibc-2.39/bin/z3" "${z3_dest}/z3"
"${z3_dest}/z3" --version
