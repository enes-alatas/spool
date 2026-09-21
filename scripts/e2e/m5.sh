#!/usr/bin/env bash
# M5 e2e: Telegram bridge. Needs two REAL bot tokens (create via @BotFather,
# /setprivacy -> Disable for both) and a Telegram group with both bots added.
#
#   TG_TOKEN_A=123:abc TG_TOKEN_B=456:def bash scripts/e2e/m5.sh
#
# This script is interactive: it tells you what to do in Telegram and checks
# what arrives on the Spool side.
set -euo pipefail

[[ -n "${TG_TOKEN_A:-}" && -n "${TG_TOKEN_B:-}" ]] || {
  echo "Set TG_TOKEN_A and TG_TOKEN_B (bot tokens from @BotFather, privacy disabled)."
  exit 2
}

PORT="${PORT:-8094}"
# the loop-facing listener (#238); on every interface so a docker workstation
# reaches it through the gateway, while the API above stays on loopback
MCP_PORT="${MCP_PORT:-8194}"
BASE="http://127.0.0.1:$PORT"

DATA="${DATA:-$(mktemp -d)}"

# Every /api request carries the operator token (ADR-0030); /api/health does
# not need it, and the wait loop below calls that with plain curl.
api() { curl -sf -H "Authorization: Bearer $(cat "$DATA/operator-token")" "$@"; }

BIN="${BIN:-./bin/spool}"
MODEL="${MODEL:-claude-haiku-4-5-20251001}"

log() { echo "[m5] $*"; }
fail() { echo "[m5] FAIL: $*" >&2; exit 1; }
cleanup() { [[ -n "${SPOOL_PID:-}" ]] && kill "$SPOOL_PID" 2>/dev/null || true; wait 2>/dev/null || true; }
trap cleanup EXIT

"$BIN" --listen "127.0.0.1:$PORT" --mcp-listen "0.0.0.0:$MCP_PORT" --data-dir "$DATA" &
SPOOL_PID=$!
for _ in $(seq 1 50); do curl -sf "$BASE/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

mk() {
  api -X POST "$BASE/api/loops" -H 'Content-Type: application/json' -d "{
    \"name\": \"$1\",
    \"mission\": \"You are a friendly test loop. Answer questions concisely. When asked to contact another loop, @mention it. On ticks reply: standing by. No next-wake trailers.\",
    \"model\": \"$MODEL\", \"tick_interval_sec\": 3600, \"idle_timeout_sec\": 120,
    \"tg_bot_token\": \"$2\"
  }" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("tg_bot_username") or d.get("error"))'
}

log "creating loops one + two with real bots"
U1=$(mk one "$TG_TOKEN_A"); U2=$(mk two "$TG_TOKEN_B")
log "bots: @$U1 (loop one), @$U2 (loop two)"

log ""
log ">>> In Telegram: add BOTH bots to one group, then send any message in the group."
log ">>> Waiting for group binding (checks every 5s, up to 5m)…"
for _ in $(seq 1 60); do
  BOUND=$(api "$BASE/api/loops/one/telegram/status" | python3 -c 'import json,sys; print(json.load(sys.stdin)["group_bound"])')
  [[ "$BOUND" == "True" ]] && break
  sleep 5
done
[[ "$BOUND" == "True" ]] || fail "group never bound — is privacy mode disabled for @$U1?"
log "group bound OK"

log ""
log ">>> Now, in the group, send:  @$U1 please say hello to the group"
log ">>> Expect: loop one replies in the group as @$U1."
log ">>> Then DM @$U2 any question. Expect a DM reply, mirrored to the group."
log ">>> Then in the group:  @$U1 please ask @two how it is doing"
log ">>> Expect: one's message appears once (from @$U1); loop two answers as @$U2."
log ""
log "Watching messages for 5 minutes — press Ctrl-C when satisfied."
for _ in $(seq 1 60); do
  sleep 5
  api "$BASE/api/activity?limit=8" | python3 -c '
import json,sys
for m in reversed(json.load(sys.stdin)):
    print(f"  [{m[\"origin\"]:>14}] @{m[\"author\"]}: {m[\"text\"][:70]}")'
  echo "  ---"
done

# dedup check: every telegram-group message must be stored exactly once
DUPS=$(python3 -c "
import sqlite3
db = sqlite3.connect('$DATA/spool.db')
n = db.execute('SELECT COUNT(*) FROM (SELECT tg_chat_id, tg_message_id, COUNT(*) c FROM messages WHERE tg_message_id IS NOT NULL GROUP BY 1,2 HAVING c>1)').fetchone()[0]
print(n)")
[[ "$DUPS" == "0" ]] || fail "$DUPS duplicated telegram messages stored"
log "dedup OK (no duplicated telegram rows)"
log "PASS — M5 verified (manual checks above)"
