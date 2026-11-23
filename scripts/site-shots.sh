#!/usr/bin/env bash
# Retakes the control room shots the docs site shows, into
# site/static/shots/, where the site's pages link them (#1700).
#
#   make site-shots
#
# The shots are taken the one way a control room shot may be: by
# web/scripts/ui-shots.mjs, against a throwaway hub serving fixture data
# (scripts/fixture-hub.sh, CONVENTIONS.md "Screenshots come from
# fixtures"). The full set lands in a temporary directory, and only the
# names below are copied out. So nothing but this script writes
# site/static/shots/, and nothing it writes shows the live fleet.
#
# The site's build takes the committed files as they are, with no npm or
# browser in it (ADR-0071), so a retake reaches the site in a commit:
# run this, read the shots, and commit them on their own.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

# Each name is a shot ui-shots.mjs takes, as <name>.png, and that a page
# under site/content/ links as /shots/<name>.png.
names=(fleet-plan-hot channels loop-timeline connections channel-telegram-group channel-add-telegram-group channel-slack-channel slack-attach)

shots="$(mktemp -d)"
trap 'rm -rf "$shots"' EXIT
SPOOL_SHOTS_DIR="$shots" bash scripts/fixture-hub.sh node scripts/ui-shots.mjs

mkdir -p site/static/shots
for name in "${names[@]}"; do
	if [ ! -f "$shots/$name.png" ]; then
		echo "site-shots: ui-shots.mjs took no $name.png; the site links it" >&2
		exit 1
	fi
	cp "$shots/$name.png" "site/static/shots/$name.png"
done
echo "site-shots: ${#names[@]} shots in site/static/shots/"
