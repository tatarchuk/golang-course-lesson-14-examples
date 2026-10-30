#!/usr/bin/env bash
# End-to-end check of the examples: builds bin/libratrack and bin/wsclient, starts the
# server on a spare port and walks the slides: pages and demo login, the slide 8
# handshake, CheckOrigin and the token check (slides 13, 39), a dashboard event from a
# REST call (slide 41), chat delivery (slide 40) and connect/disconnect cycles that end at
# the server's goroutine baseline (slide 43). Prints PASS/FAIL per check, exits 1 on any
# failure. CI runs it; locally: bash scripts/smoke.sh
set -u

PORT=${PORT:-18090}
BASE=http://127.0.0.1:$PORT
ORIGIN=http://localhost:$PORT
WORK=$(mktemp -d)
FAILED=0
pass() { echo "PASS: $1"; }
fail() { FAILED=1; echo "FAIL: $1${2:+ -- $2}"; }

go build -o bin/ ./cmd/libratrack ./cmd/wsclient || exit 1
bin/libratrack -addr "127.0.0.1:$PORT" -origin "$ORIGIN" > "$WORK/server.log" 2>&1 &
SRV=$!
LST=""
trap 'if [ -n "$LST" ]; then kill "$LST" 2> /dev/null; fi; kill "$SRV" 2> /dev/null; rm -rf "$WORK"' EXIT
for _ in 1 2 3 4 5 6 7 8 9 10; do
  curl -sS -o /dev/null "$BASE/" 2> /dev/null && break
  sleep 0.3
done

code() { curl -sS -o /dev/null -w '%{http_code}' "$@"; }
token() { curl -sS -X POST -d "{\"username\":\"$1\"}" "$BASE/api/login" | sed -E 's/.*"token":"([^"]+)".*/\1/'; }
handshake() {
  curl -sS -i -N --max-time 2 -H 'Connection: Upgrade' -H 'Upgrade: websocket' \
    -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' -H 'Sec-WebSocket-Version: 13' \
    -H "Origin: $1" "$BASE/ws/dashboard$2" 2> /dev/null | tr -d '\r'
}

# Pages and the demo login.
[ "$(code "$BASE/")" = 200 ] && pass "GET / serves the index page" || fail "GET / serves the index page"
[ "$(code "$BASE/chat.html")" = 200 ] && pass "GET /chat.html" || fail "GET /chat.html"
ANNA=$(token anna)
case $ANNA in eyJ*) pass "POST /api/login as anna returns a token" ;; *) fail "POST /api/login as anna returns a token" ;; esac
[ "$(code -X POST -d '{"username":"mallory"}' "$BASE/api/login")" = 401 ] &&
  pass "POST /api/login as an unknown user -> 401" || fail "POST /api/login as an unknown user -> 401"

# Slide 8 handshake, slide 13 CheckOrigin, slide 39 token.
out=$(handshake "$ORIGIN" "?token=$ANNA")
case $out in *"101 Switching Protocols"*"Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo="*)
  pass "slide 8 handshake -> 101 with the slide's Sec-WebSocket-Accept" ;;
*) fail "slide 8 handshake -> 101 with the slide's Sec-WebSocket-Accept" "$(echo "$out" | head -3)" ;; esac
case $(handshake http://evil.example "?token=$ANNA") in *"403 Forbidden"*) pass "foreign Origin -> 403" ;; *) fail "foreign Origin -> 403" ;; esac
case $(handshake "$ORIGIN" "") in *"401 Unauthorized"*) pass "no token -> 401" ;; *) fail "no token -> 401" ;; esac

# Slide 41: a REST call reaches a connected dashboard.
bin/wsclient -base "$BASE" -origin "$ORIGIN" -user olena listen > "$WORK/listen.out" 2>&1 &
LST=$!
sleep 1
BOHDAN=$(token bohdan)
[ "$(code -X POST -H "Authorization: Bearer $BOHDAN" "$BASE/api/loans/L-2/return")" = 204 ] &&
  pass "POST /api/loans/L-2/return -> 204" || fail "POST /api/loans/L-2/return -> 204"
[ "$(code -X POST -H "Authorization: Bearer $BOHDAN" "$BASE/api/loans/L-2/return")" = 409 ] &&
  pass "returning L-2 again -> 409" || fail "returning L-2 again -> 409"
[ "$(code -X POST -H "Authorization: Bearer $BOHDAN" "$BASE/api/loans/L-404/return")" = 404 ] &&
  pass "returning L-404 -> 404" || fail "returning L-404 -> 404"
[ "$(code -X POST "$BASE/api/loans/L-3/return")" = 401 ] &&
  pass "returning without a token -> 401" || fail "returning without a token -> 401"
sleep 1
grep -q '{"event":"loan_returned","loan_id":"L-2"}' "$WORK/listen.out" &&
  pass "the dashboard client received loan_returned L-2" || fail "the dashboard client received loan_returned L-2" "$(cat "$WORK/listen.out")"
kill -INT "$LST" 2> /dev/null
wait "$LST" 2> /dev/null
LST=""

# Slides 40 and 43: chat delivery and connect/disconnect cycles back to the baseline.
out=$(bin/wsclient -base "$BASE" -origin "$ORIGIN" -clients 3 -cycles 1 cycles 2>&1)
case $out in *"9 of 9 messages received"*"OK:"*) pass "chat: 3 clients each receive all 3 messages" ;;
*) fail "chat: 3 clients each receive all 3 messages" "$out" ;; esac
out=$(bin/wsclient -base "$BASE" -origin "$ORIGIN" -clients 50 -cycles 3 cycles 2>&1) &&
  pass "3 cycles of 50 clients (close frame): back to the goroutine baseline" ||
  fail "3 cycles of 50 clients (close frame): back to the goroutine baseline" "$out"
out=$(bin/wsclient -base "$BASE" -origin "$ORIGIN" -clients 50 -cycles 3 -abrupt cycles 2>&1) &&
  pass "3 cycles of 50 clients (abrupt): back to the goroutine baseline" ||
  fail "3 cycles of 50 clients (abrupt): back to the goroutine baseline" "$out"

if [ "$FAILED" -eq 0 ]; then echo "SMOKE OK"; else echo "SMOKE FAILED"; fi
exit $FAILED
