#!/usr/bin/env bash
# Build Lean 4 specification proofs, reject sorryAx, and check canonical conformance vectors.
set -euo pipefail
IFS=$'\n\t'

cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
command -v lake >/dev/null || { echo 'Install pinned elan/Lean 4 before running verify-lean; Lean proofs cannot be skipped' >&2; exit 1; }

build_log="$(
    cd formal/lean
    lake build 2>&1
)"
printf '%s\n' "${build_log}"
if grep -Fq 'sorryAx' <<< "${build_log}"; then
    echo 'Lean proof uses forbidden sorryAx axiom' >&2
    exit 1
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf -- "${tmp_dir}"' EXIT
(
    cd formal/lean
    lake exe microfat-conformance > "${tmp_dir}/vectors.json"
)
diff -u formal/conformance/vectors.json "${tmp_dir}/vectors.json"
printf 'Lean 4 proofs cleanly verified (zero sorryAx) and conformance vectors match.\n'
