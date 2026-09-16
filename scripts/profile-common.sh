#!/usr/bin/env bash
# Common utilities for pprof profiling and leak detection scripts.

# Determine color and symbol mode
COLOR="${COLOR:-1}"
if [ -n "${NO_COLOR:-}" ] || [ "${COLOR}" = "0" ] || [ "${COLOR}" = "false" ] || [ ! -t 1 ]; then
    COLOR=0
fi

if [ "$COLOR" -eq 1 ]; then
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

is_port_in_use() {
    local port="$1"
    if command -v python3 >/dev/null 2>&1; then
        if python3 -c '
import socket, sys
port = int(sys.argv[1])
try:
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.bind(("127.0.0.1", port))
    s.close()
except OSError:
    sys.exit(1)
try:
    s = socket.create_connection(("localhost", port), timeout=0.1)
    s.close()
    sys.exit(1)
except OSError:
    pass
sys.exit(0)
' "$port" 2>/dev/null; then
            return 1 # Port is free
        else
            return 0 # Port is in use
        fi
    elif (echo > "/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
        return 0 # Port is in use
    else
        return 1 # Port is free
    fi
}

terminate_pid() {
    local pid="${1:-}"
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
        kill -TERM "$pid" 2>/dev/null || true
        for _ in $(seq 1 20); do
            if ! kill -0 "$pid" 2>/dev/null; then
                break
            fi
            sleep 0.1
        done
        if kill -0 "$pid" 2>/dev/null; then
            kill -KILL "$pid" 2>/dev/null || true
        fi
        wait "$pid" 2>/dev/null || true
    fi
}
