#!/usr/bin/env bash
# Takes the control room's standard screenshot set against a fixture store.
#
# A screenshot in a PR is driven against fixture data, never the live fleet
# (CONVENTIONS.md "Screenshots come from fixtures"): the timeline renders raw
# assistant text and tool inputs, so a live shot publishes whatever a loop
# echoed. Until now that rule had no tooling behind it and the documented path
# was a dev server pointed at whatever was handy — which is how thirteen
# live-data images reached the board before the go-public audit (#153, #248).
#
# Everything here is thrown away: a hub on a temp data directory that
# cmd/uifixture wrote seconds earlier, on a port of its own so a dev server on
# :8080 is untouched, killed and deleted on any exit.
#
# Run as `make ui-shots`, which builds the binary and the UI first.
set -euo pipefail

port="${UI_SHOTS_PORT:-8390}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

if ! node -e "require.resolve('playwright')" >/dev/null 2>&1 &&
	! (cd web && node -e "import('playwright')" >/dev/null 2>&1); then
	echo "ui-shots needs playwright: cd web && npm install && npx playwright install chromium" >&2
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

# The fixture's bots carry a synthetic token, so the hub starts a poller for
# each. Port 9 on loopback is closed: every call fails at once and nothing
# leaves the machine, where the default base would send them to Telegram.

./bin/spool --data-dir "$dir" \
	--listen "127.0.0.1:$port" --mcp-listen "127.0.0.1:$((port + 1))" \
	--runtime bare --egress-image "" \
	--telegram-api-base "http://127.0.0.1:9" >"$dir/hub.log" 2>&1 &
hub=$!

for _ in $(seq 60); do
	if curl -fsS -o /dev/null "http://127.0.0.1:$port/api/health" 2>/dev/null; then
		break
	fi
	sleep 0.25
done
if ! curl -fsS -o /dev/null "http://127.0.0.1:$port/api/health" 2>/dev/null; then
	echo "ui-shots: the fixture hub did not come up; its log:" >&2
	cat "$dir/hub.log" >&2
	exit 1
fi

cd web
SPOOL_URL="http://127.0.0.1:$port" SPOOL_DATA_DIR="$dir" node scripts/ui-shots.mjs
