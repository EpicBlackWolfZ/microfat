#!/usr/bin/env bash
# One package/profile contract for local tests and CI. GO is supplied by Taskfile.yml.
set -euo pipefail
IFS=$'\n\t'

: "${GO:=go}" "${TEST_ARTIFACT_DIR:=.work/tests}" "${COVERAGE_FILE:=coverage.out}"
mkdir -p "${TEST_ARTIFACT_DIR}"
case "${1:-test}" in
    test|coverage|coverage-unit) ;;
    *) echo 'usage: test-profiles.sh test|coverage|coverage-unit' >&2; exit 1 ;;
esac
test_packages=(./...)
if [[ "${1:-test}" == coverage-unit ]]; then
    # Keep the full production statement universe while staging black-box tests later.
    test_packages=(./cmd/... ./internal/... ./runtimeinit/... ./benchmarks/...)
fi
run_tests() {
    local profile="${1}"
    shift
    if command -v gotestsum >/dev/null 2>&1; then
        local go_bin
        local go_dir
        go_bin="$(command -v "${GO}")"
        go_dir="$(dirname "${go_bin}")"
        PATH="${go_dir}:${PATH}" gotestsum \
            --junitfile "${TEST_ARTIFACT_DIR}/junit-${profile}.xml" --format pkgname -- "$@"
    else
        "${GO}" test "$@"
    fi
}
if [[ "${1:-test}" == coverage || "${1:-test}" == coverage-unit ]]; then
    "${GO}" test ./internal/coveragegate ./internal/cmd/coverage
    # Source blocks shared between profiles are counted once by the Go merger.
    run_tests default -race -covermode=atomic -coverprofile="${TEST_ARTIFACT_DIR}/default.out" \
        -coverpkg=./cmd/...,./internal/...,./runtimeinit/...,./benchmarks/... "${test_packages[@]}"
    run_tests minimal -race -tags minimal -covermode=atomic -coverprofile="${TEST_ARTIFACT_DIR}/minimal.out" \
        -coverpkg=./cmd/...,./internal/...,./runtimeinit/...,./benchmarks/... "${test_packages[@]}"
    "${GO}" run ./internal/cmd/coverage "${COVERAGE_FILE}" "${TEST_ARTIFACT_DIR}/default.out" "${TEST_ARTIFACT_DIR}/minimal.out"
else
    run_tests default -race ./...
    run_tests minimal -race -tags minimal ./...
fi
