#!/usr/bin/env bash
# Print false only for a nonempty diff consisting entirely of known documentation paths.
set -euo pipefail
IFS=$'\n\t'

if [[ "$#" -ne 2 ]]; then
    echo "usage: ci-code-changes.sh BASE HEAD" >&2
    exit 2
fi

base_sha=$(git rev-parse --verify --end-of-options "${1}^{commit}")
head_sha=$(git rev-parse --verify --end-of-options "${2}^{commit}")
paths_file=$(mktemp)
trap 'rm -f -- "${paths_file}"' EXIT

# A failed or incomplete Git command must not masquerade as an empty docs diff.
# Disabling rename detection examines both old and new paths for moved code.
git diff --no-ext-diff --no-textconv --no-renames --name-only -z \
    "${base_sha}" "${head_sha}" -- > "${paths_file}"

if [[ ! -s "${paths_file}" ]]; then
    echo true
    exit 0
fi

while IFS= read -r -d '' changed_path; do
    case "${changed_path}" in
        docs/*.md | LICENSE) ;;
        */*) echo true; exit 0 ;;
        *.md) ;;
        *) echo true; exit 0 ;;
    esac
done < "${paths_file}"

echo false
