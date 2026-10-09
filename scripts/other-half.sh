#!/usr/bin/env bash
# A feat PR that changes only one half of the product names its other half
# (#634).
#
#   bash scripts/other-half.sh <title> <body-file> <files-file>
#
# <files-file> lists the PR's changed paths, one per line. The check applies
# when the title is a `feat` and the PR changes internal/ but not web/, or
# web/ but not internal/: a hub feature with no room to use it, or a room
# feature with no hub behind it. Every other path is neutral, since a hub
# feature brings its tier-2 tests, docs and fixture with it (#631 touched
# cmd/, docs/ and itest/ as well). v0.4.2 shipped #631's rehome route with
# no control-room way to use it, and #632 was only filed afterwards,
# because the pairing lived in one reviewer's head.
#
# The PR body then carries, outside an HTML comment, one of:
#
#   Other half: #632               the sibling issue, which must exist
#   Other half: none — <reason>    no other half, and why not
#   Other half: API-only           a hub change no room needs (or UI-only)
#
# Checking #n runs $OTHER_HALF_LOOKUP with the number as its last
# argument. It prints `issue`, `pull` or `none`, and fails when it cannot
# tell; it defaults to issue_kind below, and the test stubs it.
set -uo pipefail

# One REST read, which the workflow's `issues: read` grant covers. The
# issues endpoint answers for pull requests too, so a PR number is told
# apart rather than passing; 404 and 410 (deleted) mean no such issue.
# Anything else is the API failing, not the author's number.
issue_kind() {
	local out
	if out=$(gh api "repos/{owner}/{repo}/issues/$1" \
		--jq 'if .pull_request then "pull" else "issue" end' 2>&1); then
		echo "$out"
	elif grep -qE 'HTTP 4(04|10)' <<<"$out"; then
		echo none
	else
		echo "$out" >&2
		return 1
	fi
}

if [ "$#" -ne 3 ]; then
	echo "usage: bash scripts/other-half.sh <title> <body-file> <files-file>" >&2
	exit 2
fi
title=$1
body_file=$2
files_file=$3

if ! grep -qE '^feat(\([^)]*\))?!?:' <<<"$title"; then
	echo "other-half: not a feat PR; nothing to check."
	exit 0
fi

paths=$(grep -v '^[[:space:]]*$' "$files_file" || true)
hub=false
room=false
grep -q '^internal/' <<<"$paths" && hub=true
grep -q '^web/' <<<"$paths" && room=true
if [ "$hub" = "$room" ]; then
	echo "other-half: the PR changes both halves or neither; nothing to check."
	exit 0
fi
half=web/
[ "$hub" = true ] && half=internal/

# The template shows the line's forms inside an HTML comment, which says
# nothing about this PR, so comments go before the line is looked for.
# A list bullet or bold around the label is markdown, not a different line.
line=$(perl -0pe 's/<!--.*?-->//gs' "$body_file" |
	perl -ne 'print if s/^\s*(?:[-*]\s+)?(?:\*\*|__)?other half:(?:\*\*|__)?//i' | head -n1)
value=$(sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//' <<<"$line")

refuse() {
	echo "other-half: this feat PR changes $half and not the other half, so its body must name the other half." >&2
	echo "  $1" >&2
	echo "  Under the template's Issue section, write one of:" >&2
	echo "    Other half: #<n>               (the sibling issue)" >&2
	echo "    Other half: none — <reason>" >&2
	echo "    Other half: API-only           (or UI-only)" >&2
	exit 1
}

if ! perl -0pe 's/<!--.*?-->//gs' "$body_file" | grep -qiE '^[[:space:]]*([-*][[:space:]]+)?(\*\*|__)?other half:'; then
	refuse "There is no 'Other half:' line outside an HTML comment."
fi

if [ -z "$value" ]; then
	refuse "The 'Other half:' line is empty: the template's placeholder, not filled in."
fi

# Words after the number are the author's note; the number is the claim.
if [[ $value =~ ^#([0-9]+)([^0-9].*)?$ ]]; then
	number=${BASH_REMATCH[1]}
	lookup=${OTHER_HALF_LOOKUP:-issue_kind}
	if ! kind=$($lookup "$number"); then
		echo "other-half: could not look up #$number; the API failed, not the PR body. Re-run the check." >&2
		exit 1
	fi
	case $kind in
	issue) ;;
	pull) refuse "'Other half: #$number' is a pull request; name the issue it works on." ;;
	*) refuse "'Other half: #$number' names no issue this repository has." ;;
	esac
	echo "other-half: the other half is #$number."
	exit 0
fi

if [[ $value =~ ^(API|UI)-only$ ]]; then
	echo "other-half: $value."
	exit 0
fi

if [[ $value =~ ^[Nn]one($|[^[:alnum:]].*$) ]]; then
	reason=$(sed -E 's/^[Nn]one[[:space:]—–:,.(-]*//' <<<"$value")
	if [ "${#reason}" -lt 3 ]; then
		refuse "'Other half: none' needs a reason after it, as 'none — <why there is no other half>'."
	fi
	echo "other-half: none — $reason"
	exit 0
fi

refuse "'Other half: $value' is none of the forms below."
