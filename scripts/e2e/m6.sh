#!/usr/bin/env bash
# M6 e2e: worktree isolation. Two loops share one repo; each commits to its
# own loop/<name> branch; main stays untouched; delete prunes the worktree
# but keeps the branch.
set -euo pipefail

PORT="${PORT:-8095}"
# the loop-facing listener (#238); on every interface so a docker workstation
# reaches it through the gateway, while the API above stays on loopback
MCP_PORT="${MCP_PORT:-8195}"
BASE="http://127.0.0.1:$PORT"
DATA="$(mktemp -d)"
REPO="$(mktemp -d)"
BIN="${BIN:-./bin/spool}"
MODEL="${MODEL:-claude-haiku-4-5-20251001}"

log() { echo "[m6] $*"; }
fail() { echo "[m6] FAIL: $*" >&2; exit 1; }

cleanup() {
  [[ -n "${SPOOL_PID:-}" ]] && kill "$SPOOL_PID" 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT

log "preparing test repo at $REPO"
git -C "$REPO" init -q -b main
echo "# test" > "$REPO/README.md"
git -C "$REPO" add . && git -C "$REPO" -c user.email=t@t -c user.name=t commit -qm init
MAIN_SHA=$(git -C "$REPO" rev-parse main)

"$BIN" --listen "127.0.0.1:$PORT" --mcp-listen "0.0.0.0:$MCP_PORT" --data-dir "$DATA" &
SPOOL_PID=$!
for _ in $(seq 1 50); do curl -sf "$BASE/api/health" >/dev/null 2>&1 && break; sleep 0.2; done

mk_loop() {
  curl -sf -X POST "$BASE/api/loops" -H 'Content-Type: application/json' -d "{
    \"name\": \"$1\",
    \"mission\": \"You are a test loop working in a git worktree. On tick turns reply exactly: standing by. When asked to commit a file, do exactly that and reply: done. Configure git user.email=loop@spool user.name=$1 locally first if needed. Never use the next-wake trailer.\",
    \"model\": \"$MODEL\",
    \"workspace_path\": \"$REPO\",
    \"tick_interval_sec\": 3600,
    \"idle_timeout_sec\": 60
  }" >/dev/null
}

completed_turns() {
  curl -sf "$BASE/api/loops/$1/turns?limit=50" | python3 -c 'import json,sys; print(sum(1 for t in json.load(sys.stdin) if t["ended_at"]>0))'
}

wait_turns() { # <loop> <min> <timeout>
  local t=0
  while (( t < $3 )); do
    (( $(completed_turns "$1") >= $2 )) && return 0
    sleep 2; ((t+=2)) || true
  done
  fail "loop $1 never reached $2 completed turns"
}

log "creating loops alpha + beta on the same repo"
mk_loop alpha
mk_loop beta

git -C "$REPO" worktree list | grep -q "loop/alpha" || fail "alpha worktree missing"
git -C "$REPO" worktree list | grep -q "loop/beta" || fail "beta worktree missing"
log "both worktrees exist"

WS_ALPHA=$(curl -sf "$BASE/api/loops/alpha" | python3 -c 'import json,sys; print(json.load(sys.stdin)["workspace_path"])')
[[ "$WS_ALPHA" == "$DATA/worktrees/alpha" ]] || fail "alpha workspace_path unexpected: $WS_ALPHA"

log "waiting for initial ticks to settle"
wait_turns alpha 1 120
wait_turns beta 1 120

log "asking each loop to commit a file named after itself"
BA=$(completed_turns alpha); BB=$(completed_turns beta)
curl -sf -X POST "$BASE/api/loops/alpha/message" -H 'Content-Type: application/json' \
  -d '{"author":"tester","text":"Create a file alpha.txt containing the single word hello, then git add and commit it with message: from alpha. Reply: done."}' >/dev/null
curl -sf -X POST "$BASE/api/loops/beta/message" -H 'Content-Type: application/json' \
  -d '{"author":"tester","text":"Create a file beta.txt containing the single word hello, then git add and commit it with message: from beta. Reply: done."}' >/dev/null
wait_turns alpha $((BA+1)) 180
wait_turns beta $((BB+1)) 180

git -C "$REPO" cat-file -e "loop/alpha:alpha.txt" || fail "alpha.txt not committed on loop/alpha"
git -C "$REPO" cat-file -e "loop/beta:beta.txt" || fail "beta.txt not committed on loop/beta"
if git -C "$REPO" cat-file -e "loop/alpha:beta.txt" 2>/dev/null; then fail "beta.txt leaked onto loop/alpha"; fi
[[ "$(git -C "$REPO" rev-parse main)" == "$MAIN_SHA" ]] || fail "main moved"
log "commits landed on their own branches; main untouched"

log "deleting alpha with remove_worktree=1"
curl -sf -X DELETE "$BASE/api/loops/alpha?remove_worktree=1" >/dev/null
sleep 1
git -C "$REPO" worktree list | grep -q "loop/alpha" && fail "alpha worktree still present"
git -C "$REPO" rev-parse --verify -q loop/alpha >/dev/null || fail "loop/alpha branch was deleted (should be kept)"
log "worktree pruned, branch kept"

log "PASS — M6 verified"
