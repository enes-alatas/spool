#!/usr/bin/env bash
# Context probe (tier 3, real claude — spends the operator's plan tokens).
#
# Answers the two questions #47 could not answer against fakeclaude. Findings
# from the 2026-08-21 run, on claude 2.1.238 with claude-haiku-4-5 (200k):
#
#   1. Does `claude -p --resume` compact by itself in headless stream-json
#      mode?  YES. Context climbed to 180,932 of 200,000 (90%) and the next
#      turn came back at 22,670 — same session id, no error, reply normal.
#      Spool needs no compaction trigger of its own for context that fills
#      gradually.
#   2. What does an over-full window look like from outside?  NOT a crash and
#      NOT a failed resume: one message larger than the whole window comes
#      back as an ordinary result event with is_error true, zero usage, and
#      the text "Prompt is too long". The process exits cleanly and the
#      session survives.
#
# MODE=fill walks the window up in blocks (question 1); MODE=oversize sends a
# single over-window message (question 2). Run with an explicit go-ahead
# only:  make e2e-context
set -euo pipefail

PORT="${PORT:-8098}"
# the loop-facing listener (#238); on every interface so a docker workstation
# reaches it through the gateway, while the API above stays on loopback
MCP_PORT="${MCP_PORT:-8198}"
BASE="http://127.0.0.1:$PORT"
DATA="$(mktemp -d)"
BIN="${BIN:-./bin/spool}"
MODEL="${MODEL:-claude-haiku-4-5-20251001}"
# ~15k tokens of filler per message; 200k window / 15k ≈ 14 messages to fill.
FILLER_WORDS="${FILLER_WORDS:-11000}"
MAX_MESSAGES="${MAX_MESSAGES:-20}"
# MODE=fill walks the window up in blocks; MODE=oversize sends one message
# larger than the whole window in a single turn.
MODE="${MODE:-fill}"

log() { echo "[ctx] $*"; }
fail() { echo "[ctx] FAIL: $*" >&2; exit 1; }

cleanup() {
  [[ -n "${SPOOL_PID:-}" ]] && kill "$SPOOL_PID" 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT

start_spool() {
  "$BIN" --listen "127.0.0.1:$PORT" --mcp-listen "0.0.0.0:$MCP_PORT" --data-dir "$DATA" --runtime bare &
  SPOOL_PID=$!
  for _ in $(seq 1 50); do
    curl -sf "$BASE/api/health" >/dev/null 2>&1 && return 0
    sleep 0.2
  done
  fail "spool did not come up"
}

completed_turns() {
  curl -sf "$BASE/api/loops/$1/turns?limit=200" | python3 -c 'import json,sys; print(sum(1 for t in json.load(sys.stdin) if t["ended_at"]>0))'
}

wait_turns() {
  local t=0
  while (( t < $3 )); do
    (( $(completed_turns "$1") >= $2 )) && return 0
    sleep 2; ((t+=2)) || true
  done
  return 1
}

# context <loop> -> "tokens limit state session"
context() {
  curl -sf "$BASE/api/loops/$1" | python3 -c '
import json,sys
v=json.load(sys.stdin)
print(v["context_tokens"], v["context_limit_tokens"], v["state"], v["current_session_id"])'
}

last_turn() {
  curl -sf "$BASE/api/loops/$1/turns?limit=1" | python3 -c '
import json,sys
t=json.load(sys.stdin)
if not t: print("(none)"); raise SystemExit
t=t[0]
print("is_error=%s in=%s cache_read=%s out=%s text=%r" % (
    t["is_error"], t.get("input_tokens"), t.get("cache_read_tokens"),
    t.get("output_tokens"), (t.get("result_text") or "")[:120]))'
}

# every spool-origin event, which is where a crash or a failed resume lands
spool_events() {
  curl -sf "$BASE/api/loops/$1/events?limit=400" | python3 -c '
import json,sys
for e in json.load(sys.stdin):
    if e.get("type") == "spool":
        print("   ", e.get("subtype"), (e.get("payload") or "")[:600])'
}

filler() {
  python3 -c "
import sys
words = ('the quick brown fox jumps over the lazy dog while carrying a heavy '
         'wooden crate full of assorted mechanical parts and old letters ').split()
n = $FILLER_WORDS
sys.stdout.write(' '.join(words[i % len(words)] for i in range(n)))"
}

start_spool
log "spool up (data: $DATA, model: $MODEL)"

curl -sf -X POST "$BASE/api/loops" -H 'Content-Type: application/json' -d "{
  \"name\": \"ctxprobe\",
  \"mission\": \"You are a context-probe loop. Reply to every message with exactly one word: ok. Never explain. Never use a next-wake trailer.\",
  \"model\": \"$MODEL\",
  \"tick_interval_sec\": 86400,
  \"idle_timeout_sec\": 15
}" >/dev/null
log "loop created; waiting for its first tick"
wait_turns ctxprobe 1 120 || fail "loop never took its first turn"
log "baseline context: $(context ctxprobe)"

FILL="$(filler)"
if [[ "$MODE" == "oversize" ]]; then
  log "sending one message of ~$FILLER_WORDS words — larger than the window in one turn"
  BASELINE=$(completed_turns ctxprobe)
  printf '%s' "$FILL" > /tmp/ctxfill.txt
  python3 -c 'import json; print(json.dumps({"author":"probe","text":"Read this and reply ok.\n\n"+open("/tmp/ctxfill.txt").read()}))' > /tmp/ctxmsg.json
  curl -sf -X POST "$BASE/api/loops/ctxprobe/message" -H 'Content-Type: application/json' \
    --data-binary @/tmp/ctxmsg.json >/dev/null
  if wait_turns ctxprobe $((BASELINE+1)) 300; then
    log "turn completed: $(last_turn ctxprobe)"
  else
    log "no completed turn within 300s"
  fi
  log "context now: $(context ctxprobe)"
  log "spool events:"; spool_events ctxprobe
  exit 0
fi

for i in $(seq 1 "$MAX_MESSAGES"); do
  BASELINE=$(completed_turns ctxprobe)
  python3 - "$FILL" "$i" <<'PY' > /tmp/ctxmsg.json
import json, sys
fill, i = sys.argv[1], sys.argv[2]
print(json.dumps({"author": "probe",
                  "text": "Block %s. Read and reply with one word: ok.\n\n%s" % (i, fill)}))
PY
  curl -sf -X POST "$BASE/api/loops/ctxprobe/message" -H 'Content-Type: application/json' \
    --data-binary @/tmp/ctxmsg.json >/dev/null
  if wait_turns ctxprobe $((BASELINE+1)) 240; then
    log "message $i -> context: $(context ctxprobe)"
    log "         last turn: $(last_turn ctxprobe)"
  else
    log "message $i -> NO completed turn within 240s; the window likely gave out"
    log "context now: $(context ctxprobe)"
    log "spool events:"; spool_events ctxprobe
    log "done (failure observed at message $i)"
    exit 0
  fi
done

log "reached $MAX_MESSAGES messages without a failure"
log "final context: $(context ctxprobe)"
log "spool events:"; spool_events ctxprobe
