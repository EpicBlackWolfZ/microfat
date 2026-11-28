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

ma_out="$(z3 formal/smt/microarch_dag.smt2)"
expected_ma=$'sat\nsat\nunsat\nunsat\nunsat'
if [[ "${ma_out}" != "${expected_ma}" ]]; then
    printf 'Unexpected Z3 output for formal/smt/microarch_dag.smt2:\n%s\n' "${ma_out}" >&2
    exit 1
fi

codec_out="$(z3 formal/smt/lz4_zstd_bounds.smt2)"
expected_codec=$'sat\nunsat\nunsat\nsat\nunsat'
if [[ "${codec_out}" != "${expected_codec}" ]]; then
    printf 'Unexpected Z3 output for formal/smt/lz4_zstd_bounds.smt2:\n%s\n' "${codec_out}" >&2
    exit 1
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf -- "${tmp_dir}"' EXIT

ma_mutant="${tmp_dir}/microarch_dag_mutant.smt2"
sed 's/(and (sat_v9_0 m) (sat_v8_6 m))/(sat_v9_0 m)/' formal/smt/microarch_dag.smt2 > "${ma_mutant}"
if cmp -s formal/smt/microarch_dag.smt2 "${ma_mutant}"; then
    printf 'Mutant sed did not modify microarch_dag.smt2\n' >&2
    exit 1
fi
ma_mut_out="$(z3 "${ma_mutant}")"
if [[ "${ma_mut_out}" == "${expected_ma}" ]]; then
    printf 'Z3 mutant unexpectedly matched expected microarch_dag.smt2 output\n' >&2
    exit 1
fi

codec_mutant="${tmp_dir}/lz4_zstd_bounds_mutant.smt2"
sed 's/(define-fun MAX_ZSTD_WIN () (_ BitVec 64) #x0000000008000000)/(define-fun MAX_ZSTD_WIN () (_ BitVec 64) #x0000000020000000)/' formal/smt/lz4_zstd_bounds.smt2 > "${codec_mutant}"
if cmp -s formal/smt/lz4_zstd_bounds.smt2 "${codec_mutant}"; then
    printf 'Mutant sed did not modify lz4_zstd_bounds.smt2\n' >&2
    exit 1
fi
codec_mut_out="$(z3 "${codec_mutant}")"
if [[ "${codec_mut_out}" == "${expected_codec}" ]]; then
    printf 'Z3 mutant unexpectedly matched expected lz4_zstd_bounds.smt2 output\n' >&2
    exit 1
fi

printf 'Z3 %s verified Format v2 (4), cgroup (8), microarch DAG (5), and codec bounds (5) + mutants.\n' "${z3_ver}"
