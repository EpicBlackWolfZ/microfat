#!/usr/bin/env bash
# Install pinned Rust quality binaries (cargo-llvm-cov, cargo-audit, cargo-deny).
set -euo pipefail
IFS=$'\n\t'

# shellcheck source=../.config/tool-versions.env
source "$(dirname -- "${BASH_SOURCE[0]}")/../.config/tool-versions.env"
: "${CARGO_LLVM_COV_LINUX_AMD64:?missing cargo-llvm-cov amd64 pin}"
: "${CARGO_LLVM_COV_LINUX_ARM64:?missing cargo-llvm-cov arm64 pin}"
: "${CARGO_AUDIT_LINUX_AMD64:?missing cargo-audit amd64 pin}"
: "${CARGO_AUDIT_LINUX_ARM64:?missing cargo-audit arm64 pin}"
: "${CARGO_DENY_LINUX_AMD64:?missing cargo-deny amd64 pin}"
: "${CARGO_DENY_LINUX_ARM64:?missing cargo-deny arm64 pin}"

tools_dest="${1:?usage: install-rust-tools.sh DESTINATION_DIRECTORY}"
case "$(uname -sm)" in
    'Linux x86_64')
        arch_triple='x86_64-unknown-linux-musl'
        audit_triple='x86_64-unknown-linux-musl'
        cov_pin="${CARGO_LLVM_COV_LINUX_AMD64}"
        audit_pin="${CARGO_AUDIT_LINUX_AMD64}"
        deny_pin="${CARGO_DENY_LINUX_AMD64}"
        ;;
    'Linux aarch64')
        arch_triple='aarch64-unknown-linux-musl'
        audit_triple='aarch64-unknown-linux-gnu'
        cov_pin="${CARGO_LLVM_COV_LINUX_ARM64}"
        audit_pin="${CARGO_AUDIT_LINUX_ARM64}"
        deny_pin="${CARGO_DENY_LINUX_ARM64}"
        ;;
    *)
        echo 'This pinned Rust tools installer requires Linux x86_64 or aarch64' >&2
        exit 1
        ;;
esac

tmp_dir="$(mktemp -d)"
trap 'rm -rf -- "${tmp_dir}"' EXIT
mkdir -p -- "${tools_dest}"

cov_tag="${cov_pin%@*}"
cov_sha="${cov_pin#*@}"
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-time 180 \
    "https://github.com/taiki-e/cargo-llvm-cov/releases/download/${cov_tag}/cargo-llvm-cov-${arch_triple}.tar.gz" \
    -o "${tmp_dir}/cov.tar.gz"
printf '%s  %s\n' "${cov_sha}" "${tmp_dir}/cov.tar.gz" | sha256sum --check --status
tar -xzf "${tmp_dir}/cov.tar.gz" -C "${tmp_dir}"
install -m 0755 "${tmp_dir}/cargo-llvm-cov" "${tools_dest}/cargo-llvm-cov"

audit_tag="${audit_pin%@*}"
audit_sha="${audit_pin#*@}"
audit_dir="cargo-audit-${audit_triple}-${audit_tag}"
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-time 180 \
    "https://github.com/rustsec/rustsec/releases/download/cargo-audit/${audit_tag}/${audit_dir}.tgz" \
    -o "${tmp_dir}/audit.tgz"
printf '%s  %s\n' "${audit_sha}" "${tmp_dir}/audit.tgz" | sha256sum --check --status
tar -xzf "${tmp_dir}/audit.tgz" -C "${tmp_dir}" "${audit_dir}/cargo-audit"
install -m 0755 "${tmp_dir}/${audit_dir}/cargo-audit" "${tools_dest}/cargo-audit"

deny_tag="${deny_pin%@*}"
deny_ver="${deny_tag#v}"
deny_sha="${deny_pin#*@}"
deny_dir="cargo-deny-${deny_ver}-${arch_triple}"
curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --max-time 180 \
    "https://github.com/EmbarkStudios/cargo-deny/releases/download/${deny_ver}/${deny_dir}.tar.gz" \
    -o "${tmp_dir}/deny.tar.gz"
printf '%s  %s\n' "${deny_sha}" "${tmp_dir}/deny.tar.gz" | sha256sum --check --status
tar -xzf "${tmp_dir}/deny.tar.gz" -C "${tmp_dir}" "${deny_dir}/cargo-deny"
install -m 0755 "${tmp_dir}/${deny_dir}/cargo-deny" "${tools_dest}/cargo-deny"

"${tools_dest}/cargo-llvm-cov" llvm-cov --version
"${tools_dest}/cargo-audit" audit --version
"${tools_dest}/cargo-deny" --version
