#!/usr/bin/env bash
# Produce an isolated PATH preserving tool precedence while excluding retired runtimes.
set -euo pipefail
IFS=$'\n\t'
mkdir -p .work
TOOLING_PATH="$(mktemp -d "${PWD}/.work/tooling-path.XXXXXX")"
IFS=: read -r -a tool_directories <<< "${PATH}"
for tool_directory in "${tool_directories[@]}"; do
    if [ -z "${tool_directory}" ] || [ ! -d "${tool_directory}" ]; then
        continue
    fi
    tool_directory="$(cd -- "${tool_directory}" && pwd -P)"
    for candidate in "${tool_directory}"/*; do
        if [ ! -f "${candidate}" ] || [ ! -x "${candidate}" ]; then
            continue
        fi
        candidate_name="${candidate##*/}"
        case "${candidate_name}" in python*|pypy*|make|gmake|blint|blint-*|blint_*) continue ;; esac
        if [ ! -e "${TOOLING_PATH}/${candidate_name}" ] && [ ! -L "${TOOLING_PATH}/${candidate_name}" ]; then
            ln -s -- "${candidate}" "${TOOLING_PATH}/${candidate_name}"
        fi
    done
done
printf '%s\n' "${TOOLING_PATH}"
