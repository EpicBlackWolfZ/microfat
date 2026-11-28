#!/usr/bin/env bash
# Check pack segment layout laws and mutant sensitivity against the production Go source.
set -euo pipefail
IFS=$'\n\t'

script_dir="$(dirname -- "${BASH_SOURCE[0]}")"
bash "${script_dir}/verify-gobra.sh" 'pack layout' internal/pack/layout.go
bash "${script_dir}/verify-gobra-mutant.sh" 'pack layout' internal/pack/layout.go 's/endOffset <= startOffset/endOffset < startOffset/'
