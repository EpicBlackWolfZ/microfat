#!/usr/bin/env bash
# Common utilities for pprof profiling and leak detection scripts.
set -euo pipefail
IFS=$'\n\t'

# Determine color and symbol mode
COLOR="${COLOR:-1}"
if [ -n "${NO_COLOR:-}" ] || [ "${COLOR}" = "0" ] || [ "${COLOR}" = "false" ] || [ ! -t 1 ]; then
    COLOR=0
fi

if [ "${COLOR}" -eq 1 ]; then
    C_RESET="\033[0m"
    C_BOLD="\033[1m"
    C_CYAN="\033[36m"
    C_GREEN="\033[32m"
    C_YELLOW="\033[33m"
    C_BLUE="\033[34m"
    C_RED="\033[31m"
    SYM_OK="✔"
    SYM_FAIL="✖"
    SYM_WARN="⚠"
    SYM_ARROW="==>"
else
    C_RESET=""
    C_BOLD=""
    C_CYAN=""
    C_GREEN=""
    C_YELLOW=""
    C_BLUE=""
    C_RED=""
    SYM_OK="[OK]"
    SYM_FAIL="[FAIL]"
    SYM_WARN="[WARN]"
    SYM_ARROW="==>"
fi
export C_RESET C_BOLD C_CYAN C_GREEN C_YELLOW C_BLUE C_RED SYM_OK SYM_FAIL SYM_WARN SYM_ARROW

is_port_in_use() {
    local port="${1}"
    local state
    if ! state="$("${GO:-go}" run ./internal/cmd/dx-net probe-port "${port}")"; then
        echo "Unable to determine port availability: ${port}" >&2
        return 2
    fi
    case "${state}" in
        occupied) return 0 ;;
        free) return 1 ;;
        *) echo "Invalid port probe result" >&2; return 2 ;;
    esac
}

terminate_pid() {
    local pid="${1:-}"
    if [ -n "${pid}" ] && kill -0 "${pid}" 2>/dev/null; then
        kill -TERM "${pid}" 2>/dev/null || true
        for _ in {1..20}; do
            if ! kill -0 "${pid}" 2>/dev/null; then
                break
            fi
            sleep 0.1
        done
        if kill -0 "${pid}" 2>/dev/null; then
            kill -KILL "${pid}" 2>/dev/null || true
        fi
        wait "${pid}" 2>/dev/null || true
    fi
}
