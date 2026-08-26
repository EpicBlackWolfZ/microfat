#!/usr/bin/env bash
set -euo pipefail

# Helper to resolve variant path with retry/wait for concurrent GoReleaser builds
resolve_variant_with_wait() {
    local timeout=60
    local elapsed=0
    while [[ $elapsed -lt $timeout ]]; do
        for p in "$@"; do
            if [[ -f "$p" ]]; then
                echo "$p"
                return 0
            fi
        done
        sleep 0.5
        elapsed=$((elapsed + 1))
    done
    echo ""
}

# Helper to package fat binary into tar.gz with docs and generate SBOMs
package_fat_archive() {
    local fat_bin="$1"
    local arch_name="$2"
    local out_tar="dist/microfat_linux_${arch_name}_fat.tar.gz"
    local stage_dir="dist/.stage_microfat_linux_${arch_name}_fat"

    echo "==> Packaging ${arch_name} fat binary into ${out_tar}..."
    rm -rf "$stage_dir"
    mkdir -p "$stage_dir"
    cp "$fat_bin" "$stage_dir/microfat"
    cp README.md LICENSE "$stage_dir/"
    if [[ -d docs ]]; then
        cp -r docs "$stage_dir/"
    fi

    tar -czf "$out_tar" -C "$stage_dir" .
    rm -rf "$stage_dir"
    echo "✔ Created fat archive: ${out_tar}"

    if command -v syft &> /dev/null; then
        echo "==> Generating SBOMs for ${out_tar}..."
        syft "$out_tar" -o spdx-json > "${out_tar}.spdx.json" 2>/dev/null || true
        syft "$out_tar" -o cyclonedx-json > "${out_tar}.cyclonedx.json" 2>/dev/null || true
        echo "✔ Generated SBOMs for ${out_tar}"
    fi
}

# 1. AMD64 Fat Binary Assembly
V1=$(resolve_variant_with_wait "dist/microfat-amd64_linux_amd64_v1/microfat" "dist/microfat-amd64_linux_amd64/microfat")
V2=$(resolve_variant_with_wait "dist/microfat-amd64_linux_amd64_v2/microfat" "dist/microfat-amd64_linux_amd64/microfat")
V3=$(resolve_variant_with_wait "dist/microfat-amd64_linux_amd64_v3/microfat" "dist/microfat-amd64_linux_amd64/microfat")
V4=$(resolve_variant_with_wait "dist/microfat-amd64_linux_amd64_v4/microfat" "dist/microfat-amd64_linux_amd64/microfat")
STUB_AMD64=$(resolve_variant_with_wait "dist/microfat-stub-amd64_linux_amd64_v1/microfat-stub" "dist/microfat-stub-amd64_linux_amd64/microfat-stub")
OUT_AMD64="dist/microfat_linux_amd64_fat"

if [[ -n "$V1" && -n "$V2" && -n "$V3" && -n "$V4" && -n "$STUB_AMD64" ]]; then
    echo "==> Bundling microfat AMD64 universal fat binary using GoReleaser-built variants..."
    go run ./cmd/microfat pack --stub "$STUB_AMD64" --arch amd64 -v v1="$V1" -v v2="$V2" -v v3="$V3" -v v4="$V4" -o "$OUT_AMD64"
    
    if [[ "$(uname -m)" == "x86_64" ]]; then
        echo "==> Running smoke test suite on self-bundled AMD64 fat binary..."
        "$OUT_AMD64" --help > /dev/null
        "$OUT_AMD64" detect
        "$OUT_AMD64" inspect "$OUT_AMD64"
        "$OUT_AMD64" verify "$OUT_AMD64"
    fi
    echo "✔ Microfat AMD64 universal fat binary successfully bundled: $OUT_AMD64"

    package_fat_archive "$OUT_AMD64" "amd64"
else
    echo "WARN: Skipping AMD64 bundling (one or more variants not found)"
fi

# 2. ARM64 Fat Binary Assembly
ARM_V80=$(resolve_variant_with_wait "dist/microfat-arm64-v8.0_linux_arm64_v8.0/microfat" "dist/microfat-arm64-v8.0_linux_arm64/microfat")
ARM_V82=$(resolve_variant_with_wait "dist/microfat-arm64-v8.2_linux_arm64_v8.0/microfat" "dist/microfat-arm64-v8.2_linux_arm64/microfat")
ARM_V90=$(resolve_variant_with_wait "dist/microfat-arm64-v9.0_linux_arm64_v8.0/microfat" "dist/microfat-arm64-v9.0_linux_arm64/microfat")
STUB_ARM64=$(resolve_variant_with_wait "dist/microfat-stub-arm64_linux_arm64_v8.0/microfat-stub" "dist/microfat-stub-arm64_linux_arm64/microfat-stub")
OUT_ARM64="dist/microfat_linux_arm64_fat"

if [[ -n "$ARM_V80" && -n "$ARM_V82" && -n "$ARM_V90" && -n "$STUB_ARM64" ]]; then
    echo "==> Bundling microfat ARM64 universal fat binary using GoReleaser-built variants..."
    go run ./cmd/microfat pack --stub "$STUB_ARM64" --arch arm64 -v v8.0="$ARM_V80" -v v8.2="$ARM_V82" -v v9.0="$ARM_V90" -o "$OUT_ARM64"
    
    if [[ "$(uname -m)" == "aarch64" ]]; then
        echo "==> Running smoke test suite on self-bundled ARM64 fat binary..."
        "$OUT_ARM64" --help > /dev/null
        "$OUT_ARM64" detect
        "$OUT_ARM64" inspect "$OUT_ARM64"
        "$OUT_ARM64" verify "$OUT_ARM64"
    fi
    echo "✔ Microfat ARM64 universal fat binary successfully bundled: $OUT_ARM64"

    package_fat_archive "$OUT_ARM64" "arm64"
else
    echo "WARN: Skipping ARM64 bundling (one or more variants not found)"
fi
