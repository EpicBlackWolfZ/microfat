#!/usr/bin/env bash
# Verify Z3 QF_BV SMT bit-vector models against the pinned Z3 theorem prover.
set -euo pipefail
IFS=$'\n\t'

cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
# shellcheck source=../.config/tool-versions.env
source .config/tool-versions.env
: "${Z3_LINUX_AMD64:?missing shared Z3 pin}"

command -v z3 >/dev/null || { echo 'Install pinned Z3 before running verify-z3; SMT verification cannot be skipped' >&2; exit 1; }

z3_tag="${Z3_LINUX_AMD64%@*}"
z3_ver="${z3_tag#v}"
actual_ver="$(z3 --version)"
if ! grep -Fq "Z3 version ${z3_ver}" <<< "${actual_ver}"; then
    echo "Expected Z3 version ${z3_ver}, got: ${actual_ver}" >&2
    exit 1
fi

fmt_out="$(z3 formal/smt/format_v2_bounds.smt2)"
expected_fmt=$'sat\nunsat\nunsat\nunsat'
if [[ "${fmt_out}" != "${expected_fmt}" ]]; then
    printf 'Unexpected Z3 output for formal/smt/format_v2_bounds.smt2:\n%s\n' "${fmt_out}" >&2
    exit 1
fi

cg_out="$(z3 formal/smt/cgroup_memory.smt2)"
expected_cg=$'sat\nunsat\nsat\nunsat\nsat\nunsat\nsat\nunsat'
if [[ "${cg_out}" != "${expected_cg}" ]]; then
    printf 'Unexpected Z3 output for formal/smt/cgroup_memory.smt2:\n%s\n' "${cg_out}" >&2
    exit 1
fi

printf 'Z3 %s verified Format v2 bounds (4 checks) and cgroup memory (8 checks).\n' "${z3_ver}"
