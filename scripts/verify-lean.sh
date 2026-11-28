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

dispatch_mutant="${tmp_dir}/DispatchStateMutant.lean"
sed 's/sealLock := true }/sealLock := false }/' formal/lean/Microfat/DispatchState.lean > "${dispatch_mutant}"
if cmp -s formal/lean/Microfat/DispatchState.lean "${dispatch_mutant}"; then
    echo 'Lean mutant sed did not modify DispatchState.lean' >&2
    exit 1
fi
dispatch_rc=0
(
    cd formal/lean
    lake env lean "${dispatch_mutant}" >/dev/null 2>&1
) || dispatch_rc=$?
if [[ "${dispatch_rc}" -eq 0 ]]; then
    echo 'Lean unexpectedly verified broken DispatchState mutant' >&2
    exit 1
fi

install_mutant="${tmp_dir}/InstallTxMutant.lean"
sed 's/{ outcome.state with activeGen := outcome.prevActive, stagedTemp := none }/{ outcome.state with stagedTemp := none }/' formal/lean/Microfat/InstallTx.lean > "${install_mutant}"
if cmp -s formal/lean/Microfat/InstallTx.lean "${install_mutant}"; then
    echo 'Lean mutant sed did not modify InstallTx.lean' >&2
    exit 1
fi
install_rc=0
(
    cd formal/lean
    lake env lean "${install_mutant}" >/dev/null 2>&1
) || install_rc=$?
if [[ "${install_rc}" -eq 0 ]]; then
    echo 'Lean unexpectedly verified broken InstallTx mutant' >&2
    exit 1
fi

printf 'Lean 4 proofs cleanly verified (zero sorryAx), state-machine mutants rejected, and conformance vectors match.\n'
