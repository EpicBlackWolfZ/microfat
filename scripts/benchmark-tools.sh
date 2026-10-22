#!/usr/bin/env bash
# Fetch tools only during explicit setup; experiments never install dependencies.
set -euo pipefail
IFS=$'\n\t'

: "${GO:=go}"
# This is a public tool: always authenticate it with the public checksum database,
# including when the caller normally bypasses checksum checks for private modules.
export GOSUMDB=sum.golang.org
export GOPRIVATE=
export GONOSUMDB=
benchmark_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
benchmark_tool_dir="${benchmark_root}/.work/benchmark-tools"
mkdir -p "${benchmark_tool_dir}"
benchmark_go_version="$("${GO}" version)"
benchmark_version="$("${GO}" run ./internal/cmd/benchmark-tools fortio-version benchmarks/tools.lock.json "${benchmark_go_version}")"
# Build external tools with the reviewed compiler, without automatic upgrades.
export GOTOOLCHAIN=local
"${GO}" mod download -json "fortio.org/fortio@${benchmark_version}" > "${benchmark_tool_dir}/module.json"
"${GO}" run ./internal/cmd/benchmark-tools verify-fortio benchmarks/tools.lock.json "${benchmark_tool_dir}/module.json"
GOBIN="${benchmark_tool_dir}" "${GO}" install "fortio.org/fortio@${benchmark_version}"
"${benchmark_tool_dir}/fortio" version
sha256sum "${benchmark_tool_dir}/fortio" > "${benchmark_tool_dir}/SHA256SUMS"
