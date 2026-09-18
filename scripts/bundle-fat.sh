#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'

# Helper to resolve variant path with retry/wait for concurrent GoReleaser builds
resolve_variant_with_wait() {
    local timeout_seconds=60
    local poll_interval=0.5
    local max_iterations=$(( timeout_seconds * 2 ))
    local count=0
    while [[ ${count} -lt ${max_iterations} ]]; do
        for p in "$@"; do
            if [[ -f "${p}" ]]; then
                echo "${p}"
                return 0
            fi
        done
        sleep "${poll_interval}"
        count=$((count + 1))
    done
    echo "ERROR: Timed out waiting for build artifact. None of the following paths exist: ${*}" >&2
    return 1
}

# 1. AMD64 Fat Binary Assembly
V1=$(resolve_variant_with_wait "dist/microfat-amd64_linux_amd64_v1/microfat" "dist/microfat-amd64_linux_amd64/microfat")
V2=$(resolve_variant_with_wait "dist/microfat-amd64_linux_amd64_v2/microfat" "dist/microfat-amd64_linux_amd64/microfat")
V3=$(resolve_variant_with_wait "dist/microfat-amd64_linux_amd64_v3/microfat" "dist/microfat-amd64_linux_amd64/microfat")
V4=$(resolve_variant_with_wait "dist/microfat-amd64_linux_amd64_v4/microfat" "dist/microfat-amd64_linux_amd64/microfat")
STUB_AMD64=$(resolve_variant_with_wait "dist/microfat-stub-amd64_linux_amd64_v1/microfat-stub" "dist/microfat-stub-amd64_linux_amd64/microfat-stub")
OUT_AMD64="dist/microfat_linux_amd64_fat"

if [[ -n "${V1}" && -n "${V2}" && -n "${V3}" && -n "${V4}" && -n "${STUB_AMD64}" ]]; then
    echo "==> Bundling microfat AMD64 universal fat binary using GoReleaser-built variants..."
    go run ./cmd/microfat pack --stub "${STUB_AMD64}" --arch amd64 -v v1="${V1}" -v v2="${V2}" -v v3="${V3}" -v v4="${V4}" -o "${OUT_AMD64}"
    
    host_arch="$(uname -m)"
    if [[ "${host_arch}" == "x86_64" ]]; then
        echo "==> Running smoke test suite on self-bundled AMD64 fat binary..."
        "${OUT_AMD64}" --help > /dev/null
        "${OUT_AMD64}" detect
        "${OUT_AMD64}" inspect "${OUT_AMD64}"
        "${OUT_AMD64}" verify "${OUT_AMD64}"
    fi
    echo "✔ Microfat AMD64 universal fat binary successfully bundled: ${OUT_AMD64}"
else
    echo "ERROR: Missing required AMD64 build artifacts" >&2
    exit 1
fi

# 2. ARM64 Fat Binary Assembly
ARM_V80=$(resolve_variant_with_wait "dist/microfat-arm64-v8.0_linux_arm64_v8.0/microfat" "dist/microfat-arm64-v8.0_linux_arm64/microfat")
ARM_V82=$(resolve_variant_with_wait "dist/microfat-arm64-v8.2_linux_arm64_v8.2/microfat" "dist/microfat-arm64-v8.2_linux_arm64/microfat")
ARM_V90=$(resolve_variant_with_wait "dist/microfat-arm64-v9.0_linux_arm64_v9.0/microfat" "dist/microfat-arm64-v9.0_linux_arm64/microfat")
STUB_ARM64=$(resolve_variant_with_wait "dist/microfat-stub-arm64_linux_arm64_v8.0/microfat-stub" "dist/microfat-stub-arm64_linux_arm64/microfat-stub")
OUT_ARM64="dist/microfat_linux_arm64_fat"

if [[ -n "${ARM_V80}" && -n "${ARM_V82}" && -n "${ARM_V90}" && -n "${STUB_ARM64}" ]]; then
    echo "==> Bundling microfat ARM64 universal fat binary using GoReleaser-built variants..."
    go run ./cmd/microfat pack --stub "${STUB_ARM64}" --arch arm64 -v v8.0="${ARM_V80}" -v v8.2="${ARM_V82}" -v v9.0="${ARM_V90}" -o "${OUT_ARM64}"
    
    host_arch="$(uname -m)"
    if [[ "${host_arch}" == "aarch64" ]]; then
        echo "==> Running smoke test suite on self-bundled ARM64 fat binary..."
        "${OUT_ARM64}" --help > /dev/null
        "${OUT_ARM64}" detect
        "${OUT_ARM64}" inspect "${OUT_ARM64}"
        "${OUT_ARM64}" verify "${OUT_ARM64}"
    fi
    echo "✔ Microfat ARM64 universal fat binary successfully bundled: ${OUT_ARM64}"
else
    echo "ERROR: Missing required ARM64 build artifacts" >&2
    exit 1
fi
