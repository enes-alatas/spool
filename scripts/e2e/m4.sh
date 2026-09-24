#!/usr/bin/env bash
# M4 e2e: @mention routing between loops. ping/pong relay a counter until the
# storm guard (12 deliveries/hour per ordered pair) halts the game.
set -euo pipefail

PORT="${PORT:-8096}"
# the loop-facing listener (#238); on every interface so a docker workstation
# reaches it through the gateway, while the API above stays on loopback
MCP_PORT="${MCP_PORT:-8196}"
BASE="http://127.0.0.1:$PORT"

DATA="$(mktemp -d)"

# Every /api request carries the operator token (ADR-0030); /api/health does
# not need it, and the wait loop below calls that with plain curl.
api() { curl -sf -H "Authorization: Bearer $(cat "$DATA/operator-token")" "$@"; }

BIN="${BIN:-./bin/spool}"
MODEL="${MODEL:-claude-haiku-4-5-20251001}"

log() { echo "[m4] $*"; }
fail() { echo "[m4] FAIL: $*" >&2; exit 1; }

cleanup() {
  [[ -n "${SPOOL_PID:-}" ]] && kill "$SPOOL_PID" 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT

"$BIN" --listen "127.0.0.1:$PORT" --mcp-listen "0.0.0.0:$MCP_PORT" --data-dir "$DATA" &
SPOOL_PID=$!
for _ in $(seq 1 50); do curl -sf "$BASE/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

mk_loop() {
  local me="$1" other="$2"
  api -X POST "$BASE/api/loops" -H 'Content-Type: application/json' -d "{
    \"name\": \"$me\",
    \"mission\": \"You are half of a ping-pong relay test. When a message you receive contains a number N, reply with exactly: @$other M   (where M = N + 1). Nothing else, no extra words. On tick turns reply exactly: standing by. Never use the next-wake trailer.\",
    \"model\": \"$MODEL\",
    \"tick_interval_sec\": 3600,
    \"idle_timeout_sec\": 300,
    \"in_fleet_channel\": true
  }" >/dev/null
}

loop_msgs() { # messages sent BY loops
  api "$BASE/api/activity?limit=200" | python3 -c 'import json,sys; print(sum(1 for m in json.load(sys.stdin) if m["origin"]=="loop" and any(x in m["text"] for x in ("@ping","@pong"))))'
}

storm_drops() {
  local a b
  a=$(api "$BASE/api/loops/ping/events?limit=500" | python3 -c 'import json,sys; print(sum(1 for e in json.load(sys.stdin) if e["subtype"]=="storm_drop"))')
  b=$(api "$BASE/api/loops/pong/events?limit=500" | python3 -c 'import json,sys; print(sum(1 for e in json.load(sys.stdin) if e["subtype"]=="storm_drop"))')
  echo $((a+b))
}

log "creating ping + pong"
mk_loop ping pong
mk_loop pong ping

log "waiting for initial tick turns to settle"
sleep 25

log "kicking off the relay: '@ping 1' via ping's composer, group destination"
api -X POST "$BASE/api/loops/ping/message" -H 'Content-Type: application/json' \
  -d '{"author":"tester","text":"@ping 1","destination":"group"}' >/dev/null

log "watching the relay (storm guard should halt it; max wait 10m)"
LAST=0; STABLE=0
for i in $(seq 1 60); do
  sleep 10
  N=$(loop_msgs)
  D=$(storm_drops)
  if (( N != LAST )); then LAST=$N; STABLE=0; else ((STABLE++)) || true; fi
  log "  loop messages: $N, storm drops: $D"
  if (( D > 0 && STABLE >= 3 )); then break; fi
done

N=$(loop_msgs)
D=$(storm_drops)
(( N >= 4 )) || fail "relay never got going (only $N loop messages)"
(( D >= 1 )) || fail "storm guard never triggered (loop messages: $N)"

TRIG=$(api "$BASE/api/loops/pong/turns?limit=1" | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["trigger"])')
[[ "$TRIG" == "message" ]] || fail "pong's last turn trigger was $TRIG, expected message"

# the storm drop must be visible as an event (UI surfaces these)
log "relay ran $N loop-messages, storm guard dropped $D deliveries, triggers recorded as 'message'"
log "PASS — M4 verified"
