#!/usr/bin/env bash
# Fetch tools only during explicit setup; experiments never install dependencies.
set -euo pipefail
IFS=$'\n\t'

: "${GO:=go}"
benchmark_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
benchmark_tool_dir="${benchmark_root}/.work/benchmark-tools"
mkdir -p "${benchmark_tool_dir}"
benchmark_go_version="$("${GO}" version)"
benchmark_version="$("${GO}" run ./internal/cmd/benchmark-tools fortio-version benchmarks/tools.lock.json "${benchmark_go_version}")"
"${GO}" mod download -json "fortio.org/fortio@${benchmark_version}" > "${benchmark_tool_dir}/module.json"
"${GO}" run ./internal/cmd/benchmark-tools verify-fortio benchmarks/tools.lock.json "${benchmark_tool_dir}/module.json"
GOBIN="${benchmark_tool_dir}" "${GO}" install "fortio.org/fortio@${benchmark_version}"
"${benchmark_tool_dir}/fortio" version
sha256sum "${benchmark_tool_dir}/fortio" > "${benchmark_tool_dir}/SHA256SUMS"
