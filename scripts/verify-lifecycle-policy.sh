#!/usr/bin/env bash
# Check lifecycle mode and ownership policy laws and mutant sensitivity against the production Go source.
set -euo pipefail
IFS=$'\n\t'

script_dir="$(dirname -- "${BASH_SOURCE[0]}")"
bash "${script_dir}/verify-gobra.sh" 'lifecycle policy' internal/lifecycle/mode_policy.go
bash "${script_dir}/verify-gobra-mutant.sh" 'lifecycle policy' internal/lifecycle/mode_policy.go 's/explicitPolicy/true/'
