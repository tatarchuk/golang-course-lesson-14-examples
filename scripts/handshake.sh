#!/usr/bin/env bash
# Slide 8 by hand: the WebSocket handshake with curl against a running examples server
# (go run ./cmd/libratrack). The request carries the slide's sample Sec-WebSocket-Key, so
# the 101 answer must carry the slide's Sec-WebSocket-Accept; the same request from a
# foreign page must be refused by CheckOrigin (slide 13).
#
# Usage: scripts/handshake.sh [BASE_URL]      (default http://127.0.0.1:8080)
#   ORIGIN  the origin the server allows (default http://localhost:8080, as cmd/libratrack)
set -u

BASE=${1:-http://127.0.0.1:8080}
ORIGIN=${ORIGIN:-http://localhost:8080}
TOKEN=$(curl -sS -X POST -d '{"username":"anna"}' "$BASE/api/login" | sed -E 's/.*"token":"([^"]+)".*/\1/')

handshake() {
  curl -sS -i -N --max-time 2 \
    -H 'Connection: Upgrade' \
    -H 'Upgrade: websocket' \
    -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
    -H 'Sec-WebSocket-Version: 13' \
    -H "Origin: $1" \
    "$BASE/ws/dashboard?token=$TOKEN" 2> /dev/null | tr -d '\r'
}

echo "== 1. Slide 8 request, Origin $ORIGIN: expect 101 and Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
handshake "$ORIGIN"
echo "(curl stops after 2 s: after the 101 the connection speaks WebSocket frames, not HTTP)"
echo
echo "== 2. The same request from a foreign page, Origin http://evil.example: expect 403"
handshake http://evil.example
echo
