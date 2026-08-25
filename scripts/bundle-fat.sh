#!/usr/bin/env bash
set -euo pipefail

V1="dist/microfat_linux_amd64_v1/microfat"
V2="dist/microfat_linux_amd64_v2/microfat"
V3="dist/microfat_linux_amd64_v3/microfat"
V4="dist/microfat_linux_amd64_v4/microfat"
STUB="dist/microfat-stub_linux_amd64_v1/microfat-stub"
OUT="dist/microfat_linux_amd64_fat"

# Only bundle when all AMD64 microarchitecture variants and stub are present
if [[ -f "$V1" && -f "$V2" && -f "$V3" && -f "$V4" && -f "$STUB" ]]; then
    echo "==> Bundling microfat universal fat binary using GoReleaser-built microarchitecture variants..."
    "$V1" pack --stub "$STUB" -v v1="$V1" -v v2="$V2" -v v3="$V3" -v v4="$V4" -o "$OUT"
    
    echo "==> Running ultimate pre-release smoke test suite on self-bundled fat binary..."
    "$OUT" --help > /dev/null
    "$OUT" detect
    "$OUT" inspect "$OUT"
    "$OUT" verify "$OUT"
    
    echo "✔ Microfat universal fat binary successfully bundled and verified: $OUT"
fi
