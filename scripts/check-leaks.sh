#!/usr/bin/env bash
# Runs microfat benchmark under pprof and inspects Go 1.27 goroutine leak endpoint.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/profile-common.sh"

PORT="${PORT:-6060}"
DURATION="${DURATION:-2s}"
TRIALS="${TRIALS:-3}"
BIN_DIR="${BIN_DIR:-bin}"

if is_port_in_use "$PORT"; then
    printf "%b\n" "${C_RED}${SYM_FAIL}${C_RESET} pprof port ${PORT} is already in use."
    printf "%b\n" "Choose another port with PORT=<port>."
    exit 1
fi

printf "%b\n" "${C_BLUE}${SYM_ARROW}${C_RESET} Launching microfat benchmark with pprof server on port ${PORT}..."

LOG="$(mktemp)"
PID=""

cleanup() {
    trap - EXIT INT TERM HUP
    rm -f "$LOG" 2>/dev/null || true
    if [ -n "$PID" ]; then
        terminate_pid "$PID"
    fi
}
trap cleanup EXIT INT TERM HUP

MICROFAT_PPROF_PORT="$PORT" "$BIN_DIR/microfat" benchmark --trials "$TRIALS" --trial-time "$DURATION" --warmup 200ms >"$LOG" 2>&1 &
PID=$!

READY=0
for _ in $(seq 1 30); do
    if curl -s "http://localhost:${PORT}/debug/pprof/" >/dev/null 2>&1; then
        READY=1
        break
    fi
    if ! kill -0 "$PID" 2>/dev/null; then
        break
    fi
    sleep 0.1
done

if [ "$READY" -ne 1 ]; then
    printf "%b\n" "${C_RED}${SYM_FAIL}${C_RESET} Failed to connect to pprof server on port ${PORT}"
    if [ -s "$LOG" ]; then
        cat "$LOG"
    fi
    cleanup
    exit 1
fi

printf "%b\n" "${C_BLUE}${SYM_ARROW}${C_RESET} Querying Go 1.27 /debug/pprof/goroutineleak endpoint..."
LEAK_REPORT="$(curl -s "http://localhost:${PORT}/debug/pprof/goroutineleak?debug=1")"
cleanup

printf "%b\n" "$LEAK_REPORT"
if echo "$LEAK_REPORT" | grep -q "total 0"; then
    printf "%b\n" "${C_GREEN}${SYM_OK}${C_RESET} Goroutine leak check passed: zero leaked goroutines detected"
    exit 0
else
    printf "%b\n" "${C_RED}${SYM_FAIL}${C_RESET} Goroutine leak check failed: leaked goroutines detected!"
    exit 1
fi
