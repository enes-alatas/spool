#!/usr/bin/env bash
# Exercises scripts/other-half.sh against fixture PRs (#634).
#
# The refusals are the PR that motivated it (#631's hub-only rehome route,
# no line) and the ways a line can look filled in without saying anything;
# the passes are each accepted form, and the PRs the check must leave alone,
# since a guard that fires on a correct PR gets disabled.
set -uo pipefail

cd "$(dirname "$0")/.."
root=$(pwd)
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

# The issue lookup, stubbed: #632 is an issue, #664 a pull request, #500
# an API failure, and nothing else exists.
cat >"$work/lookup" <<'STUB'
#!/usr/bin/env bash
case ${!#} in
632) echo issue ;;
664) echo pull ;;
500) echo 'gh: Server Error (HTTP 500)' >&2; exit 1 ;;
*) echo none ;;
esac
STUB
chmod +x "$work/lookup"
export OTHER_HALF_LOOKUP="$work/lookup"

hub=$'internal/httpapi/loops.go\ninternal/loop/actor.go'
room=$'web/src/pages/Fleet.tsx\nweb/src/styles.css'
both=$'internal/httpapi/loops.go\nweb/src/api.ts'

# name, want (pass, refuse or error), title, changed files; body on stdin.
# An error fails the check too, but blames the API and shows no template.
check() {
	local name=$1 want=$2 title=$3 files=$4 out status
	cat >"$work/body"
	printf '%s\n' "$files" >"$work/files"
	out=$(bash "$root/scripts/other-half.sh" "$title" "$work/body" "$work/files" 2>&1)
	status=$?
	if [ "$want" = pass ] && [ "$status" -ne 0 ]; then
		fail "$name: want pass, got: $out"
	elif [ "$want" = refuse ] && [ "$status" -ne 1 ]; then
		fail "$name: want a refusal, got status $status: $out"
	elif [ "$want" = refuse ] && ! grep -q "Other half: #<n>" <<<"$out"; then
		fail "$name: the refusal does not show the template line: $out"
	elif [ "$want" = error ] && { [ "$status" -ne 1 ] || ! grep -q "Re-run the check" <<<"$out"; }; then
		fail "$name: want an API error, got status $status: $out"
	else
		pass "$name"
	fi
}

check hub-only-no-line refuse 'feat(httpapi): a loop can be rehomed into a workstation' "$hub" <<'EOF'
## Issue

Closes #631
EOF

check room-only-no-line refuse 'feat(web): the first-run page' "$room" <<'EOF'
Closes #581
EOF

check names-the-sibling pass 'feat(httpapi): a loop can be rehomed into a workstation' "$hub" <<'EOF'
Closes #631
Other half: #632
EOF

check names-no-issue refuse 'feat(httpapi): x' "$hub" <<'EOF'
Other half: #9999
EOF

check names-a-pull-request refuse 'feat(httpapi): x' "$hub" <<'EOF'
Other half: #664
EOF

check lookup-fails error 'feat(httpapi): x' "$hub" <<'EOF'
Other half: #500
EOF

check bare-hash refuse 'feat(httpapi): x' "$hub" <<'EOF'
Other half: #
EOF

check empty-placeholder refuse 'feat(httpapi): x' "$hub" <<'EOF'
Other half:
EOF

check only-in-a-comment refuse 'feat(httpapi): x' "$hub" <<'EOF'
<!-- Other half: #632 | none — <reason> | API-only | UI-only -->
EOF

check none-with-reason pass 'feat(web): x' "$room" <<'EOF'
Other half: none — the hub already serves this field
EOF

check none-with-colon pass 'feat(web): x' "$room" <<'EOF'
Other half: none: the hub already serves this field
EOF

check none-without-reason refuse 'feat(web): x' "$room" <<'EOF'
Other half: none
EOF

check none-with-a-dash-only refuse 'feat(web): x' "$room" <<'EOF'
Other half: none —
EOF

check a-word-starting-none refuse 'feat(web): x' "$room" <<'EOF'
Other half: nonexistent
EOF

check api-only pass 'feat(httpapi): x' "$hub" <<'EOF'
Other half: API-only
EOF

check ui-only pass 'feat(web): x' "$room" <<'EOF'
Other half: UI-only
EOF

check lower-case-label pass 'feat(web): x' "$room" <<'EOF'
other half: #632
EOF

check free-text refuse 'feat(web): x' "$room" <<'EOF'
Other half: Terra is on it
EOF

check breaking-feat refuse 'feat(httpapi)!: x' "$hub" <<'EOF'
Closes #1
EOF

check unscoped-feat refuse 'feat: x' "$room" <<'EOF'
Closes #1
EOF

check both-halves pass 'feat: x' "$both" <<'EOF'
Closes #1
EOF

check not-a-feat pass 'fix(httpapi): x' "$hub" <<'EOF'
Closes #1
EOF

# #631's own shape: the hub, its tier-2 test, docs and the CLI, no room.
check hub-with-its-tests-and-docs refuse 'feat: a bare loop can be rehomed' $'cmd/spool/main.go\ndocs/ARCHITECTURE.md\ninternal/httpapi/loops.go\nitest/rehome_test.go' <<'EOF'
Closes #631
EOF

# #659's: the room and the fixture that feeds its screenshots.
check room-with-its-fixture refuse 'feat(web): the plan strip' $'cmd/uifixture/main.go\nweb/src/api.ts' <<'EOF'
Closes #648
EOF

check neither-half pass 'feat: a new make target' $'Makefile\nscripts/x.sh' <<'EOF'
Closes #1
EOF

check feature-word-in-title pass 'fix(web): a feature flag reads right' "$room" <<'EOF'
Closes #1
EOF

check bulleted-bold-label pass 'feat(web): x' "$room" <<'EOF'
- **Other half:** #632
EOF

check number-with-a-note pass 'feat(web): x' "$room" <<'EOF'
Other half: #632 (Terra's, in review)
EOF

check number-run-on refuse 'feat(web): x' "$room" <<'EOF'
Other half: #6321
EOF

check label-mid-sentence refuse 'feat(web): x' "$room" <<'EOF'
The Other half: #632 is filed.
EOF

if [ "$failures" -gt 0 ]; then
	echo "$failures other-half check(s) failed"
	exit 1
fi
