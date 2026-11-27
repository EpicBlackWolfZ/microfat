#!/usr/bin/env bash
# Run Kani bounded model-checking proofs against the verified Rust core.
set -euo pipefail
IFS=$'\n\t'

cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
unset RUSTUP_TOOLCHAIN

if command -v kani-driver >/dev/null; then
    kani_out="$(kani-driver experiments/v3lab/rust-stub/src/lib.rs --jobs 4 --output-format=terse)"
elif command -v kani >/dev/null; then
    kani_out="$(kani experiments/v3lab/rust-stub/src/lib.rs --jobs 4 --output-format=terse)"
else
    echo 'Install pinned Kani before running verify-kani; Kani verification cannot be skipped' >&2
    exit 1
fi

printf '%s\n' "${kani_out}"
if ! grep -Fq 'Complete - 17 successfully verified harnesses, 0 failures, 17 total.' <<< "${kani_out}"; then
    echo 'Kani verification did not report all 17 harnesses verified with 0 failures' >&2
    exit 1
fi
