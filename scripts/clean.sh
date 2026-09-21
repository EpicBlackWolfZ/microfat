#!/usr/bin/env bash
# Remove only explicit generated outputs; reject repository/root-like destinations.
set -euo pipefail
IFS=$'\n\t'
BIN_DIR="${BIN_DIR:-bin}"
COVERAGE_FILE="${COVERAGE_FILE:-coverage.out}"
CLEAN_TARGET="$(realpath -m -- "${BIN_DIR}")"
CLEAN_ROOT="$(pwd -P)"
if [[ "${CLEAN_ROOT}/" == "${CLEAN_TARGET}/"* ]] || [ "${CLEAN_TARGET}" = / ]; then
    echo 'Unsafe BIN_DIR for clean: it contains the current directory' >&2
    exit 1
fi
case "${COVERAGE_FILE}" in ''|/|.|..|../|./) echo 'Unsafe COVERAGE_FILE for clean' >&2; exit 1 ;; esac
rm -rf -- "${BIN_DIR}" dist
rm -f -- "${COVERAGE_FILE}" coverage.html unit-tests.xml
