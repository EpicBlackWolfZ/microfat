#!/usr/bin/env bash
# Build host or cross-architecture products with explicit metadata and baseline stubs.
set -euo pipefail
IFS=$'\n\t'

GO="${GO:-go}"
BIN_DIR="${BIN_DIR:-bin}"
HOST_ARCH="${HOST_ARCH:-$("${GO}" env GOARCH)}"
ARCH="${1:?expected host, amd64 or arm64}"
SUFFIX=""
if [ "${ARCH}" = host ]; then
    ARCH="${HOST_ARCH}"
else
    SUFFIX="-${ARCH}"
    export GOOS=linux GOARCH="${ARCH}"
fi
case "${ARCH}" in
    amd64|arm64) ;;
    *) echo "Unsupported build architecture: ${ARCH}" >&2; exit 1 ;;
esac
LDFLAGS="${LDFLAGS:--s -w -X github.com/EpicBlackWolfZ/microfat/internal/version.Version=${VERSION:-dev} -X github.com/EpicBlackWolfZ/microfat/internal/version.Commit=${COMMIT:-none} -X github.com/EpicBlackWolfZ/microfat/internal/version.Date=${DATE:-unknown} -X github.com/EpicBlackWolfZ/microfat/internal/version.BuiltBy=task -X github.com/EpicBlackWolfZ/microfat/internal/version.Vendor=EpicBlackWolfZ}"
mkdir -p "${BIN_DIR}"
"${GO}" build -ldflags="${LDFLAGS}" -o "${BIN_DIR}/microfat${SUFFIX}" ./cmd/microfat

if [ "${ARCH}" = amd64 ]; then
    export GOAMD64=v1
else
    export GOARM64=v8.0
fi
"${GO}" build -ldflags="${LDFLAGS}" -o "${BIN_DIR}/microfat-stub${SUFFIX}" ./cmd/microfat-stub
"${GO}" build -tags minimal -ldflags="${LDFLAGS}" -o "${BIN_DIR}/microfat-stub-minimal${SUFFIX}" ./cmd/microfat-stub
printf 'Built %s CLI and full/minimal launchers in %s\n' "${ARCH}" "${BIN_DIR}"
