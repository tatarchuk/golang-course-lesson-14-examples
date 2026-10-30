#!/usr/bin/env bash
# Slide 43, last row, by hand: start a server binary on a spare port, run 3 cycles of 100
# clients that leave with a close frame and 3 cycles of 100 that drop the TCP connection,
# and compare the server's goroutines before and after each run (GET /debug/stats).
#
# Usage: scripts/cycles.sh [SERVER_BINARY]      (default bin/libratrack, from make build)
#   bin/libratrack-as-on-slides (make build-as-on-slides) shows the leak of the slide code.
set -u

BIN=${1:-bin/libratrack}
PORT=${PORT:-18080}
BASE=http://127.0.0.1:$PORT
ORIGIN=http://localhost:$PORT
LOG=$(mktemp)
"$BIN" -addr "127.0.0.1:$PORT" -origin "$ORIGIN" > "$LOG" 2>&1 &
SRV=$!
trap 'kill "$SRV" 2> /dev/null; rm -f "$LOG"' EXIT
sleep 1

echo "== server: $BIN"
rc=0
bin/wsclient -base "$BASE" -origin "$ORIGIN" -clients 100 -cycles 3 cycles || rc=1
bin/wsclient -base "$BASE" -origin "$ORIGIN" -clients 100 -cycles 3 -abrupt cycles || rc=1
exit $rc
