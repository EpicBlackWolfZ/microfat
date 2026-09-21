#!/usr/bin/env bash
# Runs microfat benchmark under pprof and inspects Go 1.27 goroutine leak endpoint.
set -euo pipefail
IFS=$'\n\t'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=profile-common.sh
source "${SCRIPT_DIR}/profile-common.sh"
C_RESET="${C_RESET:-}"
C_BLUE="${C_BLUE:-}"
C_GREEN="${C_GREEN:-}"
C_RED="${C_RED:-}"
SYM_OK="${SYM_OK:-}"
SYM_FAIL="${SYM_FAIL:-}"
SYM_ARROW="${SYM_ARROW:-}"

PORT="${PORT:-6060}"
DURATION="${DURATION:-2s}"
TRIALS="${TRIALS:-3}"
BIN_DIR="${BIN_DIR:-bin}"

set +e
is_port_in_use "${PORT}"
port_status=$?
set -e
if [ "${port_status}" -gt 1 ]; then
    exit 1
fi
if [ "${port_status}" -eq 0 ]; then
    printf "%b\n" "${C_RED}${SYM_FAIL}${C_RESET} pprof port ${PORT} is already in use."
    printf "%b\n" "Choose another port with PORT=<port>."
    exit 1
fi

printf "%b\n" "${C_BLUE}${SYM_ARROW}${C_RESET} Launching microfat benchmark with pprof server on port ${PORT}..."

LOG="$(mktemp)"
PID=""

cleanup() {
    trap - EXIT INT TERM HUP
    rm -f "${LOG}" 2>/dev/null || true
    if [ -n "${PID}" ]; then
        terminate_pid "${PID}"
    fi
}
trap cleanup EXIT INT TERM HUP

MICROFAT_PPROF_PORT="${PORT}" "${BIN_DIR}/microfat" benchmark --trials "${TRIALS}" --trial-time "${DURATION}" --warmup 200ms >"${LOG}" 2>&1 &
PID=$!

set +e
wait_for_pprof "${PID}" "${PORT}"
readiness_status=$?
set -e
if [ "${readiness_status}" -ne 0 ]; then
    printf "%b\n" "${C_RED}${SYM_FAIL}${C_RESET} Failed to connect to pprof server on port ${PORT}"
    if [ -s "${LOG}" ]; then
        cat "${LOG}"
    fi
    cleanup
    exit 1
fi

printf "%b\n" "${C_BLUE}${SYM_ARROW}${C_RESET} Querying Go 1.27 /debug/pprof/goroutineleak endpoint..."
LEAK_REPORT="$(curl -s "http://localhost:${PORT}/debug/pprof/goroutineleak?debug=1")"
cleanup

printf "%b\n" "${LEAK_REPORT}"
if echo "${LEAK_REPORT}" | grep -q "total 0"; then
    printf "%b\n" "${C_GREEN}${SYM_OK}${C_RESET} Goroutine leak check passed: zero leaked goroutines detected"
    exit 0
else
    printf "%b\n" "${C_RED}${SYM_FAIL}${C_RESET} Goroutine leak check failed: leaked goroutines detected!"
    exit 1
fi
