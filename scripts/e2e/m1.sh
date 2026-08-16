#!/usr/bin/env bash
# M1 e2e: process manager + store + one echo loop.
# Creates a loop, talks to it, verifies idle-reap, session resume across
# process death AND across an orchestrator restart.
set -euo pipefail

PORT="${PORT:-8099}"
BASE="http://127.0.0.1:$PORT"
DATA="$(mktemp -d)"
BIN="${BIN:-./bin/spool}"
MODEL="${MODEL:-claude-haiku-4-5-20251001}"

log() { echo "[m1] $*"; }
fail() { echo "[m1] FAIL: $*" >&2; exit 1; }

cleanup() {
  [[ -n "${SPOOL_PID:-}" ]] && kill "$SPOOL_PID" 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT

start_spool() {
  "$BIN" --listen "127.0.0.1:$PORT" --data-dir "$DATA" &
  SPOOL_PID=$!
  for _ in $(seq 1 50); do
    curl -sf "$BASE/api/health" >/dev/null 2>&1 && return 0
    sleep 0.2
  done
  fail "spool did not come up"
}

# wait_state <loop> <state> <timeout_s>
wait_state() {
  local want="$2" t=0
  while (( t < $3 )); do
    state=$(curl -sf "$BASE/api/loops/$1" | python3 -c 'import json,sys; print(json.load(sys.stdin)["state"])')
    [[ "$state" == "$want" ]] && return 0
    sleep 1; ((t++)) || true
  done
  fail "loop $1 never reached state $want (last: $state)"
}

last_reply() {
  curl -sf "$BASE/api/loops/$1/turns?limit=1" | python3 -c 'import json,sys; t=json.load(sys.stdin); print(t[0]["result_text"] if t else "")'
}

turn_count() {
  curl -sf "$BASE/api/loops/$1/turns?limit=100" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))'
}

completed_turns() {
  curl -sf "$BASE/api/loops/$1/turns?limit=100" | python3 -c 'import json,sys; print(sum(1 for t in json.load(sys.stdin) if t["ended_at"]>0))'
}

# wait_turns <loop> <min_completed> <timeout_s>
wait_turns() {
  local t=0
  while (( t < $3 )); do
    (( $(completed_turns "$1") >= $2 )) && return 0
    sleep 1; ((t++)) || true
  done
  fail "loop $1 never reached $2 completed turns"
}

session_of() {
  curl -sf "$BASE/api/loops/$1" | python3 -c 'import json,sys; print(json.load(sys.stdin)["current_session_id"])'
}

pid_of() {
  curl -sf "$BASE/api/loops/$1" | python3 -c 'import json,sys; print(json.load(sys.stdin)["current_pid"])'
}

start_spool
log "spool up (data: $DATA)"

log "creating loop 'echo' (interval 1h so ticks don't interfere)"
curl -sf -X POST "$BASE/api/loops" -H 'Content-Type: application/json' -d "{
  \"name\": \"echo\",
  \"mission\": \"You are a test loop. Answer questions directly and concisely. Never use the next-wake trailer.\",
  \"model\": \"$MODEL\",
  \"tick_interval_sec\": 3600,
  \"idle_timeout_sec\": 8
}" >/dev/null

# creation schedules an immediate first tick; wait for that turn to finish
wait_turns echo 1 90
BASELINE=$(completed_turns echo)
SESSION1=$(session_of echo)
log "first tick done (completed=$BASELINE, session=$SESSION1)"

log "sending message 1 (remember the word pineapple)"
curl -sf -X POST "$BASE/api/loops/echo/message" -H 'Content-Type: application/json' \
  -d '{"author":"tester","text":"Remember the word: pineapple. Just confirm briefly."}' >/dev/null
wait_turns echo $((BASELINE+1)) 90
log "reply 1: $(last_reply echo)"

log "waiting for idle reap (idle_timeout 8s)"
for _ in $(seq 1 30); do
  [[ "$(pid_of echo)" == "0" ]] && break
  sleep 1
done
[[ "$(pid_of echo)" == "0" ]] || fail "process was not reaped after idle timeout"
wait_state echo asleep 5
log "process reaped, loop asleep"

BASELINE=$(completed_turns echo)
log "sending message 2 (recall test across process death)"
curl -sf -X POST "$BASE/api/loops/echo/message" -H 'Content-Type: application/json' \
  -d '{"author":"tester","text":"What was the word I asked you to remember? Reply with just the word."}' >/dev/null
wait_turns echo $((BASELINE+1)) 120
REPLY=$(last_reply echo)
log "reply 2: $REPLY"
echo "$REPLY" | grep -qi pineapple || fail "loop forgot 'pineapple' after resume (got: $REPLY)"
[[ "$(session_of echo)" == "$SESSION1" ]] || fail "session id changed across resume"
log "resume across process death OK (same session)"

log "restarting orchestrator"
kill "$SPOOL_PID"; wait "$SPOOL_PID" 2>/dev/null || true
start_spool
BASELINE=$(completed_turns echo)
log "sending message 3 (recall across orchestrator restart)"
curl -sf -X POST "$BASE/api/loops/echo/message" -H 'Content-Type: application/json' \
  -d '{"author":"tester","text":"Once more: what was the word? Just the word."}' >/dev/null
wait_turns echo $((BASELINE+1)) 120
REPLY=$(last_reply echo)
log "reply 3: $REPLY"
echo "$REPLY" | grep -qi pineapple || fail "loop forgot 'pineapple' after orchestrator restart (got: $REPLY)"

COST=$(curl -sf "$BASE/api/loops/echo" | python3 -c 'import json,sys; print(json.load(sys.stdin)["cost_today_usd"])')
log "cost today: \$$COST"

log "PASS — M1 verified (data dir kept at $DATA for inspection)"
