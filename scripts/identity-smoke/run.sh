#!/usr/bin/env bash
# Identity smoke test (ADR-0004): runs real um-api and pharmacy-api binaries
# against MongoDB and Redis and checks that UM session changes reach pharmacy
# within the 30-second cache, and that a Redis outage refuses protected
# operations while catalog reads degrade. Takes about 75 seconds.
#
# Required env:
#   UM_BIN, PHARMACY_BIN   built binaries
# Optional env:
#   MONGO_URI              default mongodb://127.0.0.1:27017
#   REDIS_ADDR             default 127.0.0.1:6379
#   REDIS_CONTAINER        docker container to pause for the outage; when unset
#                          the local redis-server process is SIGSTOPped
#   UM_PORT, PHARMACY_PORT default 18686, 18188
set -uo pipefail

: "${UM_BIN:?UM_BIN is required}" "${PHARMACY_BIN:?PHARMACY_BIN is required}"
MONGO_URI=${MONGO_URI:-mongodb://127.0.0.1:27017}
REDIS_ADDR=${REDIS_ADDR:-127.0.0.1:6379}
UM_PORT=${UM_PORT:-18686}
PHARMACY_PORT=${PHARMACY_PORT:-18188}
SECRET=smoke-secret
PASSWORD=smoke-password-1
BOB_ID=66aaaaaaaaaaaaaaaaaaaaaa
UM=http://127.0.0.1:$UM_PORT/api/um/v1
PH=http://127.0.0.1:$PHARMACY_PORT/api/pharmacy/v1
HERE="$(cd "$(dirname "$0")" && pwd)"
WORK="$(mktemp -d)"
FAILURES=0

pause_redis() {
  if [ -n "${REDIS_CONTAINER:-}" ]; then docker pause "$REDIS_CONTAINER" >/dev/null
  else kill -STOP "${REDIS_PID:?}"; fi
}
resume_redis() {
  if [ -n "${REDIS_CONTAINER:-}" ]; then docker unpause "$REDIS_CONTAINER" >/dev/null 2>&1
  elif [ -n "${REDIS_PID:-}" ]; then kill -CONT "$REDIS_PID" 2>/dev/null; fi
}
cleanup() {
  resume_redis
  kill "${UM_PID:-}" "${PH_PID:-}" 2>/dev/null
  wait 2>/dev/null
  if [ "$FAILURES" -ne 0 ]; then
    echo "--- um-api log";       tail -40 "$WORK/um.log"
    echo "--- pharmacy-api log"; tail -40 "$WORK/pharmacy.log"
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

wait_port() {
  for _ in $(seq 1 150); do (echo >"/dev/tcp/127.0.0.1/$1") 2>/dev/null && return 0; sleep 0.2; done
  echo "port $1 did not open"; exit 1
}
start_um() {
  # Run from $WORK so neither repo's .env is loaded.
  (cd "$WORK" && exec env -i PATH="$PATH" HOME="$HOME" PORT="$UM_PORT" MONGO_HOST="$MONGO_URI" \
    MONGO_UM_DB_NAME=um_smoke REDIS_HOST="$REDIS_ADDR" SECRET_KEY="$SECRET" \
    "$UM_BIN" >>"$WORK/um.log" 2>&1) & UM_PID=$!
  wait_port "$UM_PORT"
}
start_pharmacy() {
  (cd "$WORK" && exec env -i PATH="$PATH" HOME="$HOME" PORT="$PHARMACY_PORT" MONGO_URI="$MONGO_URI" \
    DB_PREFIX=pharmacy_smoke SECRET_KEY="$SECRET" SYSTEM=PHARMACY UM_REDIS_HOST="$REDIS_ADDR" \
    "$PHARMACY_BIN" >>"$WORK/pharmacy.log" 2>&1) & PH_PID=$!
  wait_port "$PHARMACY_PORT"
}
login() {
  curl -s -X POST "$UM/auth/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$1\",\"password\":\"$PASSWORD\",\"system\":\"PHARMACY\"}" |
    sed -nE 's/.*"accessToken":"([^"]+)".*/\1/p'
}
# check NAME PATH TOKEN WANT_STATUS [MAX_SECONDS]
check() {
  local out got secs
  out=$(curl -s -o /dev/null -w '%{http_code} %{time_total}' -H "Authorization: Bearer $3" "$PH$2")
  got=${out% *}; secs=${out#* }
  if [ "$got" != "$4" ]; then
    echo "FAIL  $1 → got $got, want $4"; FAILURES=$((FAILURES + 1))
  elif [ -n "${5:-}" ] && awk "BEGIN{exit !($secs > $5)}"; then
    echo "FAIL  $1 → $got after ${secs}s, want within ${5}s"; FAILURES=$((FAILURES + 1))
  else
    echo "PASS  $1 → $got (${secs}s)"
  fi
}

(cd "$HERE/../.." && MONGO_URI="$MONGO_URI" UM_DB=um_smoke go run ./scripts/identity-smoke/seed) || exit 1
if [ -z "${REDIS_CONTAINER:-}" ]; then
  REDIS_PID=$(redis-cli -h "${REDIS_ADDR%:*}" -p "${REDIS_ADDR##*:}" info server | awk -F: '/process_id/{print $2}' | tr -d '\r')
fi
start_um
start_pharmacy

A=$(login alice); B=$(login bob)
if [ -z "$A" ] || [ -z "$B" ]; then echo "FAIL  login returned no token"; FAILURES=1; exit 1; fi

check "live session: sensitive read"          /settings   "$A" 200
check "live session: ADMIN report"            /report/eod "$A" 200
check "USER on ADMIN report"                  /report/eod "$B" 403
curl -s -o /dev/null -X POST -H "Authorization: Bearer $A" "$UM/auth/logout"
deleted=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE -H "Authorization: Bearer $(login alice)" "$UM/user/$BOB_ID")
[ "$deleted" = 200 ] || { echo "FAIL  UM delete bob → $deleted"; FAILURES=$((FAILURES + 1)); }
check "right after logout (cached ≤30s)"      /settings   "$A" 200
sleep 31
check "logged-out session after cache expiry" /settings   "$A" 401
check "logged-out session, catalog read"      /drugs      "$A" 401
check "deleted user after cache expiry"       /settings   "$B" 401

T=$(login alice)
check "new session"                           /settings   "$T" 200
pause_redis
check "Redis down, cached answer"             /settings   "$T" 200
sleep 31
# An outage must answer promptly, not hang each request on Redis timeouts.
check "Redis down: sensitive read"            /settings   "$T" 503 3
check "Redis down: ADMIN report"              /report/eod "$T" 503 3
check "Redis down: catalog read degrades"     /drugs      "$T" 200 3
resume_redis
check "Redis back"                            /settings   "$T" 200

[ "$FAILURES" -eq 0 ] && echo "identity smoke: all checks passed" || echo "identity smoke: $FAILURES failure(s)"
exit "$FAILURES"
