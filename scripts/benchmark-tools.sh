#!/usr/bin/env bash
# Fetch tools only during explicit setup; experiments never install dependencies.
set -euo pipefail
: "${GO:=go}"
benchmark_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
benchmark_tool_dir="$benchmark_root/.work/benchmark-tools"
mkdir -p "$benchmark_tool_dir"
"$GO" version | python3 -c 'import sys; assert "go1.27.1 " in sys.stdin.read(), "Go 1.27.1 required"'
benchmark_version="$(python3 -c 'import json; print(json.load(open("benchmarks/tools.lock.json"))["fortio"]["version"])')"
"$GO" mod download -json "fortio.org/fortio@$benchmark_version" > "$benchmark_tool_dir/module.json"
python3 - "$benchmark_tool_dir/module.json" <<'PY'
import json
import sys
pin = json.load(open("benchmarks/tools.lock.json"))["fortio"]
module = json.load(open(sys.argv[1]))
assert module["Sum"] == pin["module_sum"], "Fortio module checksum mismatch"
assert module["GoModSum"] == pin["go_mod_sum"], "Fortio go.mod checksum mismatch"
assert module["Origin"]["Hash"] == pin["source_commit"], "Fortio source commit mismatch"
PY
GOBIN="$benchmark_tool_dir" "$GO" install "fortio.org/fortio@$benchmark_version"
"$benchmark_tool_dir/fortio" version
sha256sum "$benchmark_tool_dir/fortio" > "$benchmark_tool_dir/SHA256SUMS"
