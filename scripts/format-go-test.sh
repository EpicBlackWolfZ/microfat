#!/usr/bin/env bash
# Test the public formatting commands without altering the caller's source tree.
set -euo pipefail
IFS=$'\n\t'

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf "${TEST_DIR}"' EXIT
mkdir -p "${TEST_DIR}/scripts" "${TEST_DIR}/.work" "${TEST_DIR}/new directory"
cp "${ROOT}/Makefile" "${TEST_DIR}/Makefile"
cp "${ROOT}/scripts/format-go.sh" "${TEST_DIR}/scripts/format-go.sh"
cd "${TEST_DIR}"
git init -q
printf '.work/\n' > .gitignore
printf 'package fixture\nfunc example(){ }\n' > .work/ignored.go
cp .work/ignored.go tracked.go
git add tracked.go
IGNORED_HASH="$(sha256sum .work/ignored.go)"

expect_failure() {
    if make --no-print-directory fmt-check COLOR=0 > output.log 2>&1; then
        echo 'format check unexpectedly succeeded' >&2
        exit 1
    fi
}

expect_failure
grep -F './tracked.go' output.log
cmp tracked.go .work/ignored.go
make --no-print-directory fmt COLOR=0
make --no-print-directory fmt-check COLOR=0

# Newly added, nonignored files have the same scope, including unusual names.
NEW_SOURCE=$'new directory/new\nsource.go'
cp .work/ignored.go "${NEW_SOURCE}"
expect_failure
make --no-print-directory fmt COLOR=0
make --no-print-directory fmt-check COLOR=0
IGNORED_AFTER="$(sha256sum .work/ignored.go)"
[[ "${IGNORED_AFTER}" == "${IGNORED_HASH}" ]]

# A deleted tracked source and a symlink into ignored evidence are not inputs.
rm tracked.go
ln -s .work/ignored.go linked.go
make --no-print-directory fmt-check COLOR=0

# Formatter parse errors must fail both operations, even without list output.
printf 'this is not Go\n' > invalid.go
expect_failure
if make --no-print-directory fmt COLOR=0 > output.log 2>&1; then
    echo 'format write unexpectedly accepted invalid Go' >&2
    exit 1
fi
rm invalid.go

# Git discovery failure must never turn into an empty successful check.
mv .git git-metadata
expect_failure
echo 'Formatting source selection regressions passed.'
