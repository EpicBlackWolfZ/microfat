#!/usr/bin/env bash
# Build the established demo variant sets; callers run from examples/demo.
set -euo pipefail
IFS=$'\n\t'
GO="${GO:-go}"
BIN_DIR="${BIN_DIR:-bin}"
BINARY_NAME="${BINARY_NAME:-demo}"
MICROFAT="${MICROFAT:?select the built or installed microfat CLI}"
ARCH="${1:?expected amd64 or arm64}"
SUFFIX=""
PREFIX=""
STUB="${MICROFAT_STUB:?select the built or installed baseline launcher}"
case "${ARCH}" in
    amd64) LEVELS=(v1 v2 v3 v4); LEVEL_KEY=GOAMD64 ;;
    arm64)
        LEVELS=(v8.0 v8.2 v9.0)
        LEVEL_KEY=GOARM64
        SUFFIX=-arm64
        PREFIX=arm64_
        STUB="${BIN_DIR}/microfat-stub-arm64"
        ;;
    *) echo "Unsupported demo architecture: ${ARCH}" >&2; exit 1 ;;
esac
mkdir -p "${BIN_DIR}"
ARGS=(pack --arch "${ARCH}" --stub "${STUB}" --name "${BINARY_NAME}" -o "${BIN_DIR}/${BINARY_NAME}-fat${SUFFIX}")
for level in "${LEVELS[@]}"; do
    target="${BIN_DIR}/${BINARY_NAME}_${PREFIX}${level}"
    env GOOS=linux GOARCH="${ARCH}" "${LEVEL_KEY}=${level}" "${GO}" build -ldflags='-s -w' -o "${target}" main.go
    ARGS+=(-v "${level}=${target}")
done
if [ "${ARCH}" = arm64 ]; then
    GOOS=linux GOARCH=arm64 GOARM64=v8.0 "${GO}" build -ldflags='-s -w' -o "${STUB}" ../../cmd/microfat-stub
fi
"${MICROFAT}" "${ARGS[@]}"
