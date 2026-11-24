#!/usr/bin/env bash
# Check the cgroup memory laws against the same source file built by Go.
set -euo pipefail
IFS=$'\n\t'

script_dir="$(dirname -- "${BASH_SOURCE[0]}")"
exec bash "${script_dir}/verify-gobra.sh" 'cgroup memory' internal/cgroup/memory.go
