#!/usr/bin/env bash
# Regression tests for Makefile developer workflows, port safety, formatting, and TTY behavior.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT_DIR"

echo "==> Running DX regression test suite..."

# -----------------------------------------------------------------------------
# 1. TTY and ANSI Escape Behavior
# -----------------------------------------------------------------------------
echo "--> Test 1: Piped output disables ANSI escape codes"
PIPED_HELP="$(make help | cat)"
if printf "%s\n" "$PIPED_HELP" | grep -F $'\033' >/dev/null; then
    echo "FAIL: make help | cat contained ANSI escape codes"
    exit 1
fi
echo "    PASS: Piped make help output contains no ANSI escape codes"

echo "--> Test 2: NO_COLOR and COLOR=0 disable ANSI escape codes"
NO_COLOR_HELP="$(NO_COLOR=1 make help)"
if printf "%s\n" "$NO_COLOR_HELP" | grep -F $'\033' >/dev/null; then
    echo "FAIL: NO_COLOR=1 make help contained ANSI escape codes"
    exit 1
fi
COLOR_ZERO_HELP="$(COLOR=0 make help)"
if printf "%s\n" "$COLOR_ZERO_HELP" | grep -F $'\033' >/dev/null; then
    echo "FAIL: COLOR=0 make help contained ANSI escape codes"
    exit 1
fi
echo "    PASS: NO_COLOR=1 and COLOR=0 strip all ANSI escape codes"

# -----------------------------------------------------------------------------
# 2. Formatting & Verification Enforcement
# -----------------------------------------------------------------------------
echo "--> Test 3: fmt-check and all fail on misformatted code without mutating it"
FIXTURE_FILE="cmd/microfat/dx_regression_fixture.go"
cat << 'EOF' > "$FIXTURE_FILE"
package main

func badFormatFixture()   {
    return
}
EOF

# Ensure fmt-check fails
FMT_FAIL=0
make fmt-check COLOR=0 >/dev/null 2>&1 || FMT_FAIL=1
if [ "$FMT_FAIL" -ne 1 ]; then
    echo "FAIL: make fmt-check did not fail on misformatted code"
    rm -f "$FIXTURE_FILE"
    exit 1
fi

# Ensure make all fails and does not silently mutate
ALL_FAIL=0
make all COLOR=0 >/dev/null 2>&1 || ALL_FAIL=1
if [ "$ALL_FAIL" -ne 1 ]; then
    echo "FAIL: make all did not fail on misformatted code"
    rm -f "$FIXTURE_FILE"
    exit 1
fi

if ! grep -q "func badFormatFixture()   {" "$FIXTURE_FILE"; then
    echo "FAIL: make all silently mutated the misformatted file!"
    rm -f "$FIXTURE_FILE"
    exit 1
fi

# Ensure make fmt fixes it
make fmt COLOR=0 >/dev/null 2>&1
if grep -q "func badFormatFixture()   {" "$FIXTURE_FILE"; then
    echo "FAIL: make fmt failed to format the fixture file"
    rm -f "$FIXTURE_FILE"
    exit 1
fi

rm -f "$FIXTURE_FILE"
make fmt-check COLOR=0 >/dev/null 2>&1
echo "    PASS: Formatting verification pipeline is strictly non-mutating"

# -----------------------------------------------------------------------------
# 3. Port Safety & Non-Destructive Occupant Preservation
# -----------------------------------------------------------------------------
echo "--> Test 4: Occupied pprof port fails cleanly without killing occupant"
DUMMY_PORT=6065
python3 -m http.server "$DUMMY_PORT" --bind 127.0.0.1 >/dev/null 2>&1 &
DUMMY_PID=$!
sleep 0.2

CHECK_FAIL=0
make check-leaks PORT="$DUMMY_PORT" COLOR=0 >/dev/null 2>&1 || CHECK_FAIL=1

if [ "$CHECK_FAIL" -ne 1 ]; then
    echo "FAIL: make check-leaks succeeded despite occupied port $DUMMY_PORT"
    kill -KILL "$DUMMY_PID" 2>/dev/null || true
    exit 1
fi

if ! kill -0 "$DUMMY_PID" 2>/dev/null; then
    echo "FAIL: make check-leaks killed unrelated dummy process on port $DUMMY_PORT!"
    exit 1
fi

kill -TERM "$DUMMY_PID" 2>/dev/null || true
wait "$DUMMY_PID" 2>/dev/null || true
echo "    PASS: Port collision on check-leaks preserved occupant process"

echo "--> Test 5: Occupied HTTP_PORT on pprof fails cleanly without killing occupant"
DUMMY_HTTP_PORT=8085
python3 -m http.server "$DUMMY_HTTP_PORT" --bind 127.0.0.1 >/dev/null 2>&1 &
DUMMY_PID=$!
sleep 0.2

PPROF_FAIL=0
make pprof HTTP_PORT="$DUMMY_HTTP_PORT" COLOR=0 >/dev/null 2>&1 || PPROF_FAIL=1

if [ "$PPROF_FAIL" -ne 1 ]; then
    echo "FAIL: make pprof succeeded despite occupied HTTP_PORT $DUMMY_HTTP_PORT"
    kill -KILL "$DUMMY_PID" 2>/dev/null || true
    exit 1
fi

if ! kill -0 "$DUMMY_PID" 2>/dev/null; then
    echo "FAIL: make pprof killed unrelated dummy process on port $DUMMY_HTTP_PORT!"
    exit 1
fi

kill -TERM "$DUMMY_PID" 2>/dev/null || true
wait "$DUMMY_PID" 2>/dev/null || true
echo "    PASS: Port collision on pprof preserved occupant process"

# -----------------------------------------------------------------------------
# 4. Profile Validation & Exit Status Propagation
# -----------------------------------------------------------------------------
echo "--> Test 6: Unsupported PROFILE name fails immediately with non-zero exit code"
INVALID_PROFILE_FAIL=0
make pprof PROFILE="nonexistent_profile_xyz" COLOR=0 >/dev/null 2>&1 || INVALID_PROFILE_FAIL=1
if [ "$INVALID_PROFILE_FAIL" -ne 1 ]; then
    echo "FAIL: make pprof succeeded with invalid profile name"
    exit 1
fi
echo "    PASS: Unsupported profile name rejected cleanly"

echo "==> All DX regression tests passed successfully!"
