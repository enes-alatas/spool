#!/usr/bin/env bash
# Runs a command in web/ against a throwaway hub serving fixture data: the
# launch `make ui-shots` and `make ui-smoke` share.
#
#   bash scripts/fixture-hub.sh <command> [args…]
#
# The command sees SPOOL_URL, the hub's address, and SPOOL_DATA_DIR, the
# directory cmd/uifixture wrote seconds earlier. It reads the operator token
# from $SPOOL_DATA_DIR/operator-token, never from the environment or argv.
#
# A screenshot in a PR is driven against fixture data, never the live fleet
# (CONVENTIONS.md "Screenshots come from fixtures"): the timeline renders raw
# assistant text and tool inputs, so a live shot publishes whatever a loop
# echoed. Until #248 that rule had no tooling behind it and the documented
# path was a dev server pointed at whatever was handy. That is how thirteen
# live-data images reached the board before the go-public audit (#153).
#
# Everything here is thrown away: a hub on a temp data directory, on a port of
# its own so a dev server on :8080 is untouched, killed and deleted on any
# exit. It serves bin/spool as built, so run it after `make build`: what the
# command sees is what ships, not a vite dev server.
set -euo pipefail

if [ "$#" -eq 0 ]; then
	echo "usage: bash scripts/fixture-hub.sh <command> [args…]" >&2
	exit 2
fi

port="${FIXTURE_HUB_PORT:-8390}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

if ! (cd web && node -e "import('playwright')" >/dev/null 2>&1); then
	echo "fixture-hub: the command needs playwright: cd web && npm install && npx playwright install chromium" >&2
	exit 1
fi

dir="$(mktemp -d)"
hub=""
cleanup() {
	[ -n "$hub" ] && kill "$hub" 2>/dev/null || true
	rm -rf "$dir"
}
trap cleanup EXIT

go run ./cmd/uifixture --data-dir "$dir" >/dev/null

# The hub runs fakeclaude, never whatever `claude` is on PATH. A CI runner has
# none, and the hub refuses to start without one it can ask for a version. On
# a workstation the real CLI is there, and a tick falling due while the hub is
# up would spend the operator's plan on a fixture loop.
go build -o "$dir/fakeclaude" ./cmd/fakeclaude

# The fixture's bots carry a synthetic token, so the hub starts a poller for
# each. Port 9 on loopback is closed: every call fails at once and nothing
# leaves the machine, where the default base would send them to Telegram.
./bin/spool --data-dir "$dir" \
	--listen "127.0.0.1:$port" --mcp-listen "127.0.0.1:$((port + 1))" \
	--runtime bare --egress-image "" --claude-bin "$dir/fakeclaude" \
	--telegram-api-base "http://127.0.0.1:9" >"$dir/hub.log" 2>&1 &
hub=$!

for _ in $(seq 60); do
	if curl -fsS -o /dev/null "http://127.0.0.1:$port/api/health" 2>/dev/null; then
		break
	fi
	sleep 0.25
done
if ! curl -fsS -o /dev/null "http://127.0.0.1:$port/api/health" 2>/dev/null; then
	echo "fixture-hub: the hub did not come up; its log:" >&2
	cat "$dir/hub.log" >&2
	exit 1
fi

cd web
SPOOL_URL="http://127.0.0.1:$port" SPOOL_DATA_DIR="$dir" "$@"
