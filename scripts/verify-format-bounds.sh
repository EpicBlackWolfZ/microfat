#!/usr/bin/env bash
# Check the Format v2 bounds laws and mutant sensitivity against the same source file built by Go.
set -euo pipefail
IFS=$'\n\t'

script_dir="$(dirname -- "${BASH_SOURCE[0]}")"
bash "${script_dir}/verify-gobra.sh" 'format bounds' internal/format/bounds.go
bash "${script_dir}/verify-gobra-mutant.sh" 'format segment bounds' internal/format/bounds.go 's/return offset + compressedSize, nil/return offset + compressedSize - 1, nil/'
bash "${script_dir}/verify-gobra-mutant.sh" 'format trailer bounds' internal/format/bounds.go 's/indexSize > maxIndexSize/indexSize < 0/'
