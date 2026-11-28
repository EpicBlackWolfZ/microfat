#!/usr/bin/env bash
# Check microarch policy ranking and bitmask laws and mutant sensitivity against the production Go source.
set -euo pipefail
IFS=$'\n\t'

script_dir="$(dirname -- "${BASH_SOURCE[0]}")"
bash "${script_dir}/verify-gobra.sh" 'microarch policy' internal/microarch/policy_math.go
bash "${script_dir}/verify-gobra-mutant.sh" 'microarch policy' internal/microarch/policy_math.go 's/rank > effectiveMaxRank/false/'
