#!/usr/bin/env bash
# Check cache metadata validation laws and mutant sensitivity against the production Go source.
set -euo pipefail
IFS=$'\n\t'

script_dir="$(dirname -- "${BASH_SOURCE[0]}")"
bash "${script_dir}/verify-gobra.sh" 'cache metadata' internal/cache/metadata.go
bash "${script_dir}/verify-gobra-mutant.sh" 'cache metadata' internal/cache/metadata.go 's/|| unsafeMode//'
