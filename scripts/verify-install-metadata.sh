#!/usr/bin/env bash
# Check install generation metadata laws and mutant sensitivity against the production Go source.
set -euo pipefail
IFS=$'\n\t'

script_dir="$(dirname -- "${BASH_SOURCE[0]}")"
bash "${script_dir}/verify-gobra.sh" 'install metadata' internal/install/metadata.go
bash "${script_dir}/verify-gobra-mutant.sh" 'install metadata' internal/install/metadata.go 's/nlink != 1/nlink == 0/'
