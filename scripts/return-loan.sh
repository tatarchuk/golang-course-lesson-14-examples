#!/usr/bin/env bash
# Slide 41's REST call: return a loan as the demo user bohdan. Every connected dashboard
# (WebSocket and SSE) receives {"event":"loan_returned","loan_id":"..."} at once.
#
# Usage: scripts/return-loan.sh LOAN_ID [BASE_URL]     (default http://127.0.0.1:8080)
#   204 = returned, 409 = returned before, 404 = no such loan; seeded loans: L-1 to L-5
set -u

ID=${1:?usage: scripts/return-loan.sh LOAN_ID [BASE_URL]}
BASE=${2:-http://127.0.0.1:8080}
TOKEN=$(curl -sS -X POST -d '{"username":"bohdan"}' "$BASE/api/login" | sed -E 's/.*"token":"([^"]+)".*/\1/')
code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $TOKEN" "$BASE/api/loans/$ID/return")
echo "POST /api/loans/$ID/return as bohdan -> $code"
