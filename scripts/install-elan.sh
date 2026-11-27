#!/usr/bin/env bash
# Install pinned elan toolchain manager for Lean 4 formal specification verification.
set -euo pipefail
IFS=$'\n\t'

# shellcheck source=../.config/tool-versions.env
source "$(dirname -- "${BASH_SOURCE[0]}")/../.config/tool-versions.env"
: "${ELAN_LINUX_AMD64:?missing shared elan pin}"

host_platform="$(uname -sm)"
if [[ "${host_platform}" != 'Linux x86_64' ]]; then
    echo 'This pinned elan installer requires Linux x86-64' >&2
    exit 1
fi

elan_ver="${ELAN_LINUX_AMD64%@*}"
elan_sha="${ELAN_LINUX_AMD64#*@}"

tmp_dir="$(mktemp -d)"
trap 'rm -rf -- "${tmp_dir}"' EXIT

curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.3 --max-time 180 \
    "https://github.com/leanprover/elan/releases/download/${elan_ver}/elan-x86_64-unknown-linux-gnu.tar.gz" \
    -o "${tmp_dir}/elan.tar.gz"
printf '%s  %s\n' "${elan_sha}" "${tmp_dir}/elan.tar.gz" | sha256sum --check --status
tar -xzf "${tmp_dir}/elan.tar.gz" -C "${tmp_dir}"
"${tmp_dir}/elan-init" -y --default-toolchain none --no-modify-path
"${HOME}/.elan/bin/elan" --version
