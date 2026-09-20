#!/usr/bin/env bash
# M2 e2e: scheduler + ticks + trailer clamping + pause/resume + kill +
# overdue-tick-after-restart.
set -euo pipefail

PORT="${PORT:-8098}"
# the loop-facing listener (#238); on every interface so a docker workstation
# reaches it through the gateway, while the API above stays on loopback
MCP_PORT="${MCP_PORT:-8198}"
BASE="http://127.0.0.1:$PORT"
DATA="$(mktemp -d)"
BIN="${BIN:-./bin/spool}"
MODEL="${MODEL:-claude-haiku-4-5-20251001}"

log() { echo "[m2] $*"; }
fail() { echo "[m2] FAIL: $*" >&2; exit 1; }

cleanup() {
  [[ -n "${SPOOL_PID:-}" ]] && kill "$SPOOL_PID" 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT

start_spool() {
  "$BIN" --listen "127.0.0.1:$PORT" --mcp-listen "0.0.0.0:$MCP_PORT" --data-dir "$DATA" &
  SPOOL_PID=$!
  for _ in $(seq 1 50); do
    curl -sf "$BASE/api/health" >/dev/null 2>&1 && return 0
    sleep 0.2
  done
  fail "spool did not come up"
}

jget() { python3 -c "import json,sys; print(json.load(sys.stdin)$1)"; }

completed_turns() {
  curl -sf "$BASE/api/loops/ticker/turns?limit=100" | python3 -c 'import json,sys; print(sum(1 for t in json.load(sys.stdin) if t["ended_at"]>0))'
}

wait_turns() { # <min_completed> <timeout_s>
  local t=0
  while (( t < $2 )); do
    (( $(completed_turns) >= $1 )) && return 0
    sleep 2; ((t+=2)) || true
  done
  fail "never reached $1 completed turns (have $(completed_turns))"
}

next_tick_delta() {
  curl -sf "$BASE/api/loops/ticker" | python3 -c 'import json,sys,time; d=json.load(sys.stdin); nt=d["next_tick_at"]; print(int(nt/1000-time.time()) if nt else "none")'
}

start_spool
log "spool up (data: $DATA)"

log "creating loop 'ticker' (interval 60s, min_wake 60s, trailer asks 10s -> must clamp)"
curl -sf -X POST "$BASE/api/loops" -H 'Content-Type: application/json' -d "{
  \"name\": \"ticker\",
  \"mission\": \"On every tick reply with exactly: tick noted [next-wake: 10s]\",
  \"model\": \"$MODEL\",
  \"tick_interval_sec\": 60,
  \"min_wake_sec\": 60,
  \"max_wake_sec\": 300,
  \"idle_timeout_sec\": 5
}" >/dev/null

log "waiting for 2 completed tick turns (~90s)"
wait_turns 2 200
TRIGGER=$(curl -sf "$BASE/api/loops/ticker/turns?limit=1" | jget '[0]["trigger"]')
[[ "$TRIGGER" == "tick" ]] || fail "expected trigger=tick, got $TRIGGER"

DELTA=$(next_tick_delta)
log "next tick in ${DELTA}s (trailer asked 10s, min_wake 60s)"
[[ "$DELTA" != "none" ]] || fail "no next tick scheduled"
(( DELTA >= 40 && DELTA <= 80 )) || fail "trailer clamp broken: next tick in ${DELTA}s, expected ~60s"
log "trailer clamped to min_wake OK"

log "pausing loop"
curl -sf -X POST "$BASE/api/loops/ticker/pause" >/dev/null
sleep 1
[[ "$(next_tick_delta)" == "none" ]] || fail "pause did not clear next_tick_at"
BASE_TURNS=$(completed_turns)
log "waiting 75s to confirm no ticks while paused"
sleep 75
(( $(completed_turns) == BASE_TURNS )) || fail "a turn ran while paused"
log "no ticks while paused OK"

log "resuming loop"
curl -sf -X POST "$BASE/api/loops/ticker/resume" >/dev/null
wait_turns $((BASE_TURNS+1)) 120
log "tick after resume OK"

log "kill test: killing live process (or asleep is fine), then manual wake resumes session"
SESSION=$(curl -sf "$BASE/api/loops/ticker" | jget '["current_session_id"]')
curl -sf -X POST "$BASE/api/loops/ticker/kill" >/dev/null
sleep 2
BASE_TURNS=$(completed_turns)
curl -sf -X POST "$BASE/api/loops/ticker/wake" >/dev/null
wait_turns $((BASE_TURNS+1)) 120
SESSION2=$(curl -sf "$BASE/api/loops/ticker" | jget '["current_session_id"]')
[[ "$SESSION" == "$SESSION2" ]] || fail "session changed after kill+wake ($SESSION -> $SESSION2)"
log "kill + wake resumed same session OK"

log "restart with overdue tick: stopping orchestrator for 70s"
kill "$SPOOL_PID"; wait "$SPOOL_PID" 2>/dev/null || true
sleep 70
BASE_TURNS_FILE=$(python3 -c "
import sqlite3
db = sqlite3.connect('$DATA/spool.db')
print(db.execute('SELECT COUNT(*) FROM turns WHERE ended_at>0').fetchone()[0])
")
start_spool
log "orchestrator back; overdue tick should fire within jitter (60s) + turn time"
wait_turns $((BASE_TURNS_FILE+1)) 180
log "overdue tick fired after restart OK"

log "PASS — M2 verified"
