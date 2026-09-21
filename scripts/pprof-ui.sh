#!/usr/bin/env bash
# Runs workload under pprof and opens interactive browser UI.
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
HTTP_PORT="${HTTP_PORT:-8080}"
PROFILE="${PROFILE:-heap}"
DURATION="${DURATION:-2s}"
BIN_DIR="${BIN_DIR:-bin}"
GO="${GO:-go}"

# Validate port conflict between PORT and HTTP_PORT
if [ "${PORT}" = "${HTTP_PORT}" ]; then
    printf "%b\n" "${C_RED}${SYM_FAIL}${C_RESET} PORT (${PORT}) and HTTP_PORT (${HTTP_PORT}) cannot be the same"
    exit 1
fi

# Detect occupied ports
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

set +e
is_port_in_use "${HTTP_PORT}"
http_port_status=$?
set -e
if [ "${http_port_status}" -gt 1 ]; then
    exit 1
fi
if [ "${http_port_status}" -eq 0 ]; then
    printf "%b\n" "${C_RED}${SYM_FAIL}${C_RESET} pprof http port ${HTTP_PORT} is already in use."
    printf "%b\n" "Choose another port with HTTP_PORT=<port>."
    exit 1
fi

# Normalize profile name
PNAME="$(echo "${PROFILE}" | tr "[:upper:]" "[:lower:]")"
case "${PNAME}" in
    heap|cpu|goroutine|allocs|mutex|block|goroutineleak)
        ;;
    profile)
        PNAME="cpu"
        ;;
    *)
        printf "%b\n" "${C_RED}${SYM_FAIL}${C_RESET} Unsupported PROFILE=${PROFILE}."
        printf "%b\n" "Supported profiles: heap cpu goroutine allocs mutex block goroutineleak"
        exit 1
        ;;
esac

# Endpoint mapping
ENDPOINT="${PNAME}"
if [ "${PNAME}" = "cpu" ]; then
    ENDPOINT="profile"
fi

SEC="${DURATION%s}"
case "${SEC}" in
    ""|*[!0-9]*) SEC=2 ;;
esac

EXTRA_PARAM=""
if [ "${ENDPOINT}" = "profile" ]; then
    EXTRA_PARAM="?seconds=${SEC}"
fi

# Opt-in sampling configuration for mutex and block profiles
BLOCK_RATE=""
MUTEX_FRACTION=""
if [ "${PNAME}" = "block" ]; then
    BLOCK_RATE="1"
fi
if [ "${PNAME}" = "mutex" ]; then
    MUTEX_FRACTION="1"
fi

printf "%b\n" "${C_BLUE}${SYM_ARROW}${C_RESET} Starting workload with pprof server on port ${PORT} [Profile: ${PROFILE}]..."

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

# Launch workload
env MICROFAT_PPROF_PORT="${PORT}" \
    MICROFAT_PPROF_BLOCK_RATE="${BLOCK_RATE:-}" \
    MICROFAT_PPROF_MUTEX_FRACTION="${MUTEX_FRACTION:-}" \
    "${BIN_DIR}/microfat" benchmark --trials 50 --trial-time 1s --warmup 200ms >"${LOG}" 2>&1 &
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

printf "%b\n" "${C_GREEN}${SYM_OK}${C_RESET} Pprof server listening at http://localhost:${PORT}/debug/pprof/"
printf "%b\n" "${C_BLUE}${SYM_ARROW}${C_RESET} Launching interactive pprof web UI at http://localhost:${HTTP_PORT}..."

STATUS=0
"${GO}" tool pprof -http=":${HTTP_PORT}" "http://localhost:${PORT}/debug/pprof/${ENDPOINT}${EXTRA_PARAM}" || STATUS=$?
cleanup
exit "${STATUS}"
