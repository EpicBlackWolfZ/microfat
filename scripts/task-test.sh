#!/usr/bin/env bash
# Exercise override precedence and mandatory-tool failures through the real Task entry point.
set -euo pipefail
IFS=$'\n\t'
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
TASK_TEST_DIR="$(mktemp -d)"
trap 'rm -rf "${TASK_TEST_DIR}"' EXIT
mkdir -p "${TASK_TEST_DIR}/path"
TASK_PATH="$(command -v task)"
GO_PATH="$(command -v go)"
TASK_GOFMT="$("${GO_PATH}" env GOROOT)/bin/gofmt"
for task_tool in bash env git date dirname; do
    task_tool_path="$(command -v "${task_tool}")"
    ln -s "${task_tool_path}" "${TASK_TEST_DIR}/path/${task_tool}"
done
ln -s "${GO_PATH}" "${TASK_TEST_DIR}/path/go"
ln -s "${TASK_PATH}" "${TASK_TEST_DIR}/path/task"

# Required tools must fail, before an apparent success or mutating fallback.
for task_check in lint-go lint-shell vuln fix snapshot; do
    if PATH="${TASK_TEST_DIR}/path" task --taskfile "${ROOT}/Taskfile.yml" "${task_check}" > "${TASK_TEST_DIR}/${task_check}.log" 2>&1; then
        echo "FAIL: ${task_check} silently accepted a missing required tool" >&2
        exit 1
    fi
    if ! grep -q 'Install ' "${TASK_TEST_DIR}/${task_check}.log"; then
        cat "${TASK_TEST_DIR}/${task_check}.log" >&2
        exit 1
    fi
done

# The CI path filter removes Make and Python without hiding the Go/Task tools.
CI_TOOL_PATH="$(bash "${ROOT}/scripts/tooling-path.sh")"
PATH="${CI_TOOL_PATH}" bash -c '
    for retired in python python3 python3.14 pypy pypy3 make gmake blint blint-cli; do
        if command -v "${retired}"; then exit 1; fi
    done
    command -v go >/dev/null
    command -v task >/dev/null
'
rm -rf "${CI_TOOL_PATH}"

# A formatter path with spaces is data; a command-line override must beat a
# conflicting inherited environment value, including the nested Task case.
cat > "${TASK_TEST_DIR}/formatter with spaces" <<'FORMATTER'
#!/usr/bin/env bash
set -euo pipefail
IFS=$'\n\t'
printf 'formatter called\n' >> "${TASK_TEST_MARKER}"
exec "${TASK_TEST_GOFMT}" "$@"
FORMATTER
chmod +x "${TASK_TEST_DIR}/formatter with spaces"
export TASK_TEST_MARKER="${TASK_TEST_DIR}/formatter.log"
export TASK_TEST_GOFMT="${TASK_GOFMT}"
GOFMT=/nonexistent/microfat-gofmt task --taskfile "${ROOT}/Taskfile.yml" fmt-check "GOFMT=${TASK_TEST_DIR}/formatter with spaces"
test -s "${TASK_TEST_MARKER}"

# Fail before removing the repository when an output override names an ancestor.
if BIN_DIR=bin/.. bash "${ROOT}/scripts/clean.sh" > "${TASK_TEST_DIR}/clean.log" 2>&1; then
    echo 'FAIL: clean accepted the repository as a build output' >&2
    exit 1
fi
grep -q 'Unsafe BIN_DIR' "${TASK_TEST_DIR}/clean.log"
echo 'Task override and required-tool regressions passed.'
