#!/usr/bin/env bash
# One package/profile contract for local tests and CI. GO is supplied by Makefile.
set -euo pipefail
: "${GO:=go}" "${TEST_ARTIFACT_DIR:=.work/tests}" "${COVERAGE_FILE:=coverage.out}"
mkdir -p "$TEST_ARTIFACT_DIR"
run_tests() {
    local profile="$1"
    shift
    if command -v gotestsum >/dev/null 2>&1; then
        PATH="$(dirname "$(command -v "$GO")"):$PATH" gotestsum \
            --junitfile "$TEST_ARTIFACT_DIR/junit-$profile.xml" --format pkgname -- "$@"
    else
        "$GO" test "$@"
    fi
}
if [[ "${1:-test}" == coverage ]]; then
    python3 scripts/coverage_test.py
    # Source blocks shared between profiles are counted once by coverage.py.
    run_tests default -race -covermode=atomic -coverprofile="$TEST_ARTIFACT_DIR/default.out" \
        -coverpkg=./cmd/...,./internal/...,./runtimeinit/...,./benchmarks/... ./...
    run_tests minimal -race -tags minimal -covermode=atomic -coverprofile="$TEST_ARTIFACT_DIR/minimal.out" \
        -coverpkg=./cmd/...,./internal/...,./runtimeinit/...,./benchmarks/... ./...
    python3 scripts/coverage.py "$COVERAGE_FILE" "$TEST_ARTIFACT_DIR/default.out" "$TEST_ARTIFACT_DIR/minimal.out"
else
    run_tests default -race ./...
    run_tests minimal -race -tags minimal ./...
fi
