#!/usr/bin/env bash
# Exercise finished demo launcher commands and clean only owned temporary outputs.
set -euo pipefail
IFS=$'\n\t'
BIN_DIR="${BIN_DIR:-bin}"
BINARY_NAME="${BINARY_NAME:-demo}"
PACKED="${BIN_DIR}/${BINARY_NAME}-fat"
CHECK_DIR="$(mktemp -d "${BIN_DIR}/.demo-check-XXXXXX")"
trap 'rm -rf "${CHECK_DIR}"' EXIT INT TERM HUP
"${PACKED}" --microfat:info > /dev/null
"${PACKED}" --microfat:info=json > /dev/null
MICROFAT_CACHE_DIR="${CHECK_DIR}/cache" "${PACKED}" --microfat:prewarm > /dev/null
"${PACKED}" "--microfat:optimize-to=${CHECK_DIR}/optimized" > /dev/null
"${CHECK_DIR}/optimized" --help > /dev/null
"${PACKED}" "--microfat:trim-to=${CHECK_DIR}/trimmed" > /dev/null
"${CHECK_DIR}/trimmed" --microfat:info > /dev/null
printf 'Finished demo info, prewarm, optimize and trim checks passed.\n'
