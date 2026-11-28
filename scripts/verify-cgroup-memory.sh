#!/usr/bin/env bash
# Check the cgroup memory laws and mutant sensitivity against the same source file built by Go.
set -euo pipefail
IFS=$'\n\t'

script_dir="$(dirname -- "${BASH_SOURCE[0]}")"
bash "${script_dir}/verify-gobra.sh" 'cgroup memory' internal/cgroup/memory.go
bash "${script_dir}/verify-gobra-mutant.sh" 'cgroup memory' internal/cgroup/memory.go 's/return limit - retained/return limit/'
