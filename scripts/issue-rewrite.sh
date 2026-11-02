#!/usr/bin/env bash
# Reads an issue's body on stdin, takes its title as the first argument, and
# prints one line for each rule of the issue standard (CONVENTIONS.md, #1034)
# the two break (#1035). Exit 1 when one is broken, 0 when none is, 2 on a
# usage error. scripts/issue-rewrite-flag.sh turns the lines into the
# `needs-rewrite` label and one comment.
#
# The rules:
#
#   1. No process names in a product title: no ADR, slice, PR, "web half"
#      and its kind, "part of", "item N", issue number, or backticked or
#      snake_case symbol.
#   2. One deliverable in a product title: no ", and", ", plus",
#      ", then", "; " or " & ". Not a comma count: a list of three is
#      one deliverable as often as not (#1113, #1247).
#      `--one-deliverable` (the maintainer's label of that name) skips
#      it, for a title whose "and" stays inside one phrase.
#   3. A title of at most 100 characters.
#   4. The body's first line, under the form's first heading, is not a
#      reference: not "Part of", "ADR-", "Split out of", "Web half of",
#      a bare #n, or their next phrasings.
#
# A title opening with a Conventional `type(scope):`, lowercase as the set
# is (`Fix:` opens a sentence), is a team-only issue (CI, tests, docs, repo
# guards), which may name its test or workflow, so rules 1 and 2 skip it;
# 3 and 4 hold for both.
#
# What the lines say is fixed text: nothing of the title or body is echoed,
# so the comment built from them can't carry a mention or a link that the
# issue's author wrote.
#
# A check of words, not meaning: whether the title says what changes for
# the operator stays with the product owner's sweep.
#
# Usage: issue-rewrite.sh [--one-deliverable] <title> < body
set -uo pipefail
export LC_ALL=C.UTF-8

one_deliverable=false
if [ "${1:-}" = --one-deliverable ]; then
	one_deliverable=true
	shift
fi
[ $# -eq 1 ] && [ -n "$1" ] || {
	echo "usage: issue-rewrite.sh [--one-deliverable] <title> < body" >&2
	exit 2
}
title=$1
body=$(tr -d '\r')

broken=()

# has reports whether the title matches an extended regex, ignoring case
# unless the third argument is "case".
has() { # pattern [case]
	local flags=-qE
	[ "${2:-}" = case ] || flags=-qiE
	printf '%s\n' "$title" | grep $flags -- "$1"
}

types='feat|fix|refactor|perf|docs|test|build|ci|chore|style|revert'
if ! has "^($types)(\([^()]+\))?!?: " case; then
	process=()
	has '\badr[- ]?[0-9]' && process+=("an ADR")
	has '\bslices?\b' && process+=("a slice")
	has '\bPRs?\b' case && process+=("a PR")
	has '\b(web|hub|go|ui|backend|frontend|server) half\b' && process+=("a web, hub or Go half")
	has '\bpart of\b' && process+=('"part of"')
	has '\bitems? [0-9]' && process+=("an item number")
	has '#[0-9]' && process+=("an issue number")
	has '`' && process+=("a backticked symbol")
	has '\b[a-z0-9]+_[a-z0-9_]*[a-z0-9]\b' && process+=("a snake_case symbol")
	if [ ${#process[@]} -gt 0 ]; then
		names=$(printf '%s, ' "${process[@]}")
		broken+=("Title: names the team's process (${names%, }). A product title is one sentence in the operator's words; the references go in the body.")
	fi

	if ! $one_deliverable; then
		if has ', (and|plus|then) ' || has '; ' || has ' & '; then
			broken+=("Title: joins more than one deliverable. One issue carries one; split it, or a maintainer labels it \`one-deliverable\` when the \"and\" stays inside one phrase.")
		fi
	fi
fi

if [ ${#title} -gt 100 ]; then
	broken+=("Title: longer than 100 characters.")
fi

# The first non-blank line under the first heading, or of the body when it
# has none, with any quote, list, link or emphasis marker in front taken off.
first=$(printf '%s\n' "$body" | awk '
	BEGIN { seen = 0 }
	!seen && /^#+[ \t]/ { seen = 1; next }
	NF { line[seen] = line[seen] ? line[seen] : $0 }
	END { print (seen ? line[1] : line[0]) }
' | sed -E 's/^([[:space:]>*_+([-]|[0-9]+[.)])+//')
starts='part of|adr[- ]?[0-9]|split (out )?(of|from)|(web|hub|go|ui|backend|frontend|server) half of|slice [0-9]+ of|follow[- ]?up (to|of|on|from)|(parent|epic)( issue)?:|#[0-9]'
if printf '%s\n' "$first" | grep -qiE "^($starts)"; then
	broken+=("Body: opens with a reference. Its first line says what the operator sees today; \"Part of #n\" and the like go further down.")
fi

[ ${#broken[@]} -gt 0 ] || exit 0
printf '%s\n' "${broken[@]}"
exit 1
