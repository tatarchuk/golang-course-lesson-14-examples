#!/usr/bin/env bash
# Slides 15 and 43: a client that vanishes without closing (a crashed tab, a closed laptop).
# Start the server with a short pong wait, connect one dashboard client, then freeze the
# client process (kill -STOP): it no longer answers pings, the server's read deadline
# fires and the hub drops it. Last, a handshake from a foreign origin (slide 13).
#
# Usage: scripts/dead-peer.sh [SERVER_BINARY]   (default bin/libratrack, from make build)
#   WAIT  the server's pong wait (default 3s)
set -u

BIN=${1:-bin/libratrack}
PORT=${PORT:-18081}
WAIT=${WAIT:-3s}
BASE=http://127.0.0.1:$PORT
ORIGIN=http://localhost:$PORT
LOG=$(mktemp)
OUT=$(mktemp)
CLI=""
"$BIN" -addr "127.0.0.1:$PORT" -origin "$ORIGIN" -pong-wait "$WAIT" > "$LOG" 2>&1 &
SRV=$!
trap 'if [ -n "$CLI" ]; then kill -CONT "$CLI" 2> /dev/null; kill "$CLI" 2> /dev/null; fi; kill "$SRV" 2> /dev/null; rm -f "$LOG" "$OUT"' EXIT
sleep 1

bin/wsclient -base "$BASE" -origin "$ORIGIN" -user olena listen > "$OUT" 2>&1 &
CLI=$!
sleep 1
echo "== $(date +%T) olena's dashboard client is connected: $(curl -sS "$BASE/debug/stats")"
kill -STOP "$CLI"
echo "== $(date +%T) kill -STOP: the client is frozen and answers no pings (server pong wait $WAIT)"
for _ in 1 2 3 4 5 6 7 8 9 10; do
  sleep 1
  stats=$(curl -sS "$BASE/debug/stats")
  echo "   $(date +%T) $stats"
  case $stats in *'"clients":0'*) break ;; esac
done
echo "== server log:"
grep -E 'register|disconnected' "$LOG" | sed 's/^/   /'
kill -CONT "$CLI"
sleep 1
echo "== kill -CONT: the client wakes up and finds its connection gone:"
sed 's/^/   /' "$OUT"
echo "== a handshake from a foreign origin is refused (slide 13):"
bin/wsclient -base "$BASE" -origin http://evil.example listen 2>&1 | sed 's/^/   /'
