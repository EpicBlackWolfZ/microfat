#!/usr/bin/env bash
# Verify Verus contracts, dual-compilation under RUST_VERSION, and mutant sensitivity.
set -euo pipefail
IFS=$'\n\t'

cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
# shellcheck source=../.config/tool-versions.env
source .config/tool-versions.env
: "${RUST_VERSION:?missing shared RUST_VERSION}"

case "$(uname -m)" in
    x86_64)
        verus_pin="${VERUS_LINUX_AMD64:?missing VERUS_LINUX_AMD64}"
        host_musl='x86_64-unknown-linux-musl'
        ;;
    aarch64)
        verus_pin="${VERUS_LINUX_ARM64:?missing VERUS_LINUX_ARM64}"
        host_musl='aarch64-unknown-linux-musl'
        ;;
    *)
        echo 'Unsupported host architecture for Verus verification' >&2
        exit 1
        ;;
esac

command -v verus >/dev/null || { echo 'Install pinned Verus before running verify-verus; Verus proofs cannot be skipped' >&2; exit 1; }
command -v cargo >/dev/null || { echo 'Install pinned Rust toolchain before running verify-verus' >&2; exit 1; }

verus_tag="${verus_pin%@*}"
verus_ver="${verus_tag#v}"
actual_ver="$(verus --version)"
if ! grep -Fq "Version: ${verus_ver}" <<< "${actual_ver}"; then
    echo "Expected Verus Version: ${verus_ver}, got: ${actual_ver}" >&2
    exit 1
fi

verus_out="$(verus --crate-type=lib experiments/v3lab/rust-stub/src/lib.rs 2>&1)"
printf '%s\n' "${verus_out}"
if ! grep -Fq 'verification results:: 4 verified, 0 errors' <<< "${verus_out}"; then
    echo 'Verus verification did not report 4 verified, 0 errors' >&2
    exit 1
fi

export RUSTUP_TOOLCHAIN="${RUST_VERSION}"
(
    cd experiments/v3lab/rust-stub
    cargo check --lib
    cargo build --release --target "${host_musl}"
)

# Mutant check: breaking retained_memory_limit subtraction must fail Verus postcondition.
tmp_dir="$(mktemp -d)"
trap 'rm -rf -- "${tmp_dir}"' EXIT
cp -r experiments/v3lab/rust-stub/src "${tmp_dir}/src"
sed -i 's/limit - retained/limit/' "${tmp_dir}/src/cgroup.rs"
if mutant_out="$(verus --crate-type=lib "${tmp_dir}/src/lib.rs" 2>&1)"; then
    printf '%s\n' "${mutant_out}" >&2
    echo 'Verus mutant regression check unexpectedly passed!' >&2
    exit 1
fi
if ! grep -Fq 'postcondition not satisfied' <<< "${mutant_out}"; then
    printf '%s\n' "${mutant_out}" >&2
    echo 'Verus mutant failed, but not on the expected postcondition violation' >&2
    exit 1
fi
printf 'Verus %s verified 4/4 contracts, confirmed %s dual-build, and rejected mutant.\n' "${verus_ver}" "${RUST_VERSION}"
