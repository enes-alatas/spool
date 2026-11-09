#!/usr/bin/env bash
# Exercises scripts/release-gate.sh against fixture PRs in a scratch repo
# (#1446).
#
# The refusals are the release that motivated it (Teams: the hub's half
# merged, #1441 still open) under each way an Other half: line names an
# issue; the passes are each way a line argues the half away, and the PRs
# the gate must leave alone, since a gate that holds every tag gets
# bypassed. GitHub is stubbed; git is real, so the range since the last
# tag is what the script computes.
set -uo pipefail

cd "$(dirname "$0")/.."
root=$(pwd)
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

# main: before - v0.1.0 - one - two; and a branch off two that main never had
repo=$work/repo
git init -q -b main "$repo"
commit() {
	git -C "$repo" -c user.name=t -c user.email=t@example.invalid commit -q --allow-empty -m "$1"
	git -C "$repo" rev-parse HEAD
}
before=$(commit before)
git -C "$repo" tag v0.1.0
one=$(commit one)
two=$(commit two)
git -C "$repo" checkout -q -b later
later=$(commit later)
git -C "$repo" checkout -q main

# Issue states, stubbed: 1441 and 1450 are open, 1426 and 1431 closed,
# 500 an API failure.
cat >"$work/state" <<'STUB'
#!/usr/bin/env bash
case $1 in
1441 | 1450) echo open ;;
1426 | 1431) echo closed ;;
500) echo 'gh: Server Error (HTTP 500)' >&2; exit 1 ;;
*) echo closed ;;
esac
STUB
cat >"$work/list" <<'STUB'
#!/usr/bin/env bash
[ -z "${STUB_LIST_FAIL:-}" ] || { echo 'gh: HTTP 502' >&2; exit 1; }
cat "$STUB_PRS"
STUB
chmod +x "$work/state" "$work/list"
export RELEASE_GATE_STATE=$work/state
export RELEASE_GATE_PRS=$work/list STUB_PRS=$work/prs.json

# name, want (pass, refuse, error or broken), title, merge commit; body on
# stdin. error blames the API; broken, a tool the gate could not run.
# want refuse also names the issue the refusal must name.
check() {
	local name=$1 want=$2 title=$3 oid=$4 issue=${5:-} out status
	jq -n --arg t "$title" --arg o "$oid" --rawfile b /dev/stdin \
		'[{number: 1442, title: $t, body: $b, mergeCommit: {oid: $o}}]' >"$work/prs.json"
	out=$(cd "$repo" && bash "$root/scripts/release-gate.sh" main 2>&1)
	status=$?
	if [ "$want" = pass ] && [ "$status" -ne 0 ]; then
		fail "$name: want pass, got status $status: $out"
	elif [ "$want" = refuse ] && { [ "$status" -ne 1 ] || ! grep -q "#1442 .*other half, #$issue, is still open" <<<"$out"; }; then
		fail "$name: want a refusal naming #1442 and #$issue, got status $status: $out"
	elif [ "$want" = error ] && { [ "$status" -ne 2 ] || ! grep -q "Re-run it" <<<"$out"; }; then
		fail "$name: want an API error, got status $status: $out"
	elif [ "$want" = broken ] && { [ "$status" -ne 2 ] || ! grep -q "nothing was" <<<"$out"; }; then
		fail "$name: want the gate to refuse to decide, got status $status: $out"
	else
		pass "$name"
	fi
}

hub='feat: a team'"'"'s contained loops run on the team'"'"'s Claude login'

# The ways a line names the other half.
check bare-number refuse "$hub" "$two" 1441 <<<$'## Issue\n\nCloses #1440\n\nOther half: #1441\n'
check bullet-and-bold refuse "$hub" "$two" 1441 <<<$'- **Other half:** #1441 (the control room\'s team login picker)\n'
check number-then-dash refuse "$hub" "$two" 1441 <<<$'Other half: #1441 — the web half, Iris\'s\n'
check lower-case refuse "$hub" "$two" 1441 <<<$'other half: #1441\n'
check bold-around-all refuse "$hub" "$two" 1441 <<<$'**Other half: #1441**\n'
check issue-url refuse "$hub" "$two" 1441 <<<$'Other half: https://github.com/enes-alatas/spool/issues/1441\n'
check two-halves-one-open refuse "$hub" "$two" 1450 <<<$'Other half: #1426 and #1450\n'
check web-feat refuse 'feat(web): the team page picks its Claude login' "$one" 1441 <<<$'Other half: #1441\n'
check breaking-feat refuse 'feat(httpapi)!: a team names its login' "$two" 1441 <<<$'Other half: #1441\n'

# A half that has shipped, or that the line argues away.
check other-half-closed pass "$hub" "$two" <<<$'Other half: #1426\n'
check none-with-reason pass "$hub" "$two" <<<$'Other half: none — the line is read by loops only; the control room shows a loop\'s team through #1411 and #1431.\n'
check none-citing-an-open-issue pass "$hub" "$two" <<<$'Other half: none — the web half was split off as #1441, which is not a half of this.\n'
check api-only pass "$hub" "$two" <<<$'Other half: API-only\n'
check ui-only pass 'feat(web): a calmer empty state' "$two" <<<$'- **Other half:** UI-only\n'
check no-issue-named pass "$hub" "$two" <<<$'Other half: the hub already serves it\n'

# PRs the gate leaves alone.
check template-comment-only pass "$hub" "$two" <<<$'<!--\nOther half: #1441\n-->\n'
check no-line pass "$hub" "$two" <<<$'## Issue\n\nCloses #1440\n'
check not-a-feat pass 'fix(httpapi): a team PATCH serializes' "$two" <<<$'Other half: #1441\n'
check merged-before-the-tag pass "$hub" "$before" <<<$'Other half: #1441\n'
check not-on-the-ref pass "$hub" "$later" <<<$'Other half: #1441\n'
check no-merge-commit pass "$hub" "" <<<$'Other half: #1441\n'

# The gate fails closed when it can't tell.
check state-api-fails error "$hub" "$two" <<<$'Other half: #500\n'
STUB_LIST_FAIL=1 check list-api-fails error "$hub" "$two" <<<$'Other half: #1441\n'

# Nor when a tool it reads with fails (#1447): a perl that can't run would
# read as no line, and a BSD date with no -d as no PR, so as releasable.
mkdir -p "$work/no-perl" "$work/bsd-date"
printf '#!/bin/sh\necho "perl: not found" >&2\nexit 127\n' >"$work/no-perl/perl"
printf '#!/bin/sh\necho "date: illegal option -- d" >&2\nexit 1\n' >"$work/bsd-date/date"
chmod +x "$work/no-perl/perl" "$work/bsd-date/date"
PATH=$work/no-perl:$PATH check perl-fails broken "$hub" "$two" <<<$'Other half: #1441\n'
PATH=$work/bsd-date:$PATH check bsd-date refuse "$hub" "$two" 1441 <<<$'Other half: #1441\n'

# A tag's own run measures from the tag before it, not from itself.
jq -n --arg o "$two" '[{number: 1442, title: "feat: x", body: "Other half: #1441", mergeCommit: {oid: $o}}]' >"$work/prs.json"
git -C "$repo" tag v0.2.0 "$two"
if out=$(cd "$repo" && bash "$root/scripts/release-gate.sh" --tag v0.2.0 v0.2.0 2>&1); then
	fail "tag-run-excludes-itself: want a refusal, got: $out"
else
	pass tag-run-excludes-itself
fi
git -C "$repo" tag -d v0.2.0 >/dev/null

# Several PRs, and the summary counts and names each open half.
jq -n --arg a "$one" --arg b "$two" '[
	{number: 1438, title: "feat(loop): a fleet rule binds a team", body: "Other half: #1437", mergeCommit: {oid: $a}},
	{number: 1442, title: "feat: a team login", body: "Other half: #1441", mergeCommit: {oid: $b}},
	{number: 1443, title: "feat: a team page", body: "Other half: #1450", mergeCommit: {oid: $b}}]' >"$work/prs.json"
out=$(cd "$repo" && bash "$root/scripts/release-gate.sh" main 2>&1)
if [ $? -eq 1 ] && grep -q '#1442.*#1441' <<<"$out" && grep -q '#1443.*#1450' <<<"$out" &&
	! grep -q '#1438.*still open' <<<"$out" && grep -q '2 other half(s) open among the 3 feat PR(s) since v0.1.0' <<<"$out"; then
	pass several-prs
else
	fail "several-prs: $out"
fi

out=$(cd "$repo" && bash "$root/scripts/release-gate.sh" no-such-ref 2>&1)
if [ $? -eq 2 ] && grep -q 'git fetch --tags' <<<"$out"; then pass unknown-ref; else fail "unknown-ref: $out"; fi

if [ "$failures" -gt 0 ]; then
	echo "release-gate-test: $failures failure(s)"
	exit 1
fi
echo "release-gate-test: all passed"
