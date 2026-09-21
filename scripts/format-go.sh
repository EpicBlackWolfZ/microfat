#!/usr/bin/env bash
# Share the exact source selection between formatting and its read-only check.
set -euo pipefail
IFS=$'\n\t'

MODE="${1:?expected check or write}"
GOFMT="${2:?expected gofmt executable}"
case "${MODE}" in
    check|write) ;;
    *) echo 'Error: expected check or write' >&2; exit 1 ;;
esac

# Materialize Git's result first so discovery failures cannot become an empty pass.
SOURCE_LIST="$(mktemp)"
trap 'rm -f "${SOURCE_LIST}"' EXIT
git ls-files --cached --others --exclude-standard -z -- '*.go' > "${SOURCE_LIST}"
FILES=()
while IFS= read -r -d '' source; do
    # Deleted tracked files are absent; never follow a source symlink elsewhere.
    if [[ -f "${source}" && ! -L "${source}" ]]; then
        FILES+=("./${source}")
    fi
done < "${SOURCE_LIST}"

if [[ "${#FILES[@]}" -eq 0 ]]; then
    exit 0
fi
if [[ "${MODE}" == write ]]; then
    "${GOFMT}" -s -w "${FILES[@]}"
else
    UNFORMATTED="$("${GOFMT}" -s -l "${FILES[@]}")"
    if [[ -n "${UNFORMATTED}" ]]; then
        printf 'Go files need formatting:\n%s\n' "${UNFORMATTED}" >&2
        exit 1
    fi
fi
