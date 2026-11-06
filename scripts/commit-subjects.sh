#!/usr/bin/env bash
# Checks a pull request's commits against the mechanical half of the
# commit-message rules in CONVENTIONS.md (#76, #1011). Rebase merges land
# each commit on main as written (ADR-0016), so a commit's message is what
# main keeps. The rules that take judgement, such as whether the summary
# says what is true once the commit lands, stay with the author and review.
# This checks:
#   - the subject is `type(scope): summary` or `type: summary`, with one of
#     the listed types, and `!` allowed before the colon;
#   - a scope names one thing: no comma, space or plus;
#   - the subject is at most 80 characters;
#   - no closing keyword sits next to `#n` anywhere in the message, since
#     GitHub closes that issue when the commit reaches main.
# Dependabot's commits are left alone: their subjects are its own. So is
# a merge commit, such as the Update branch button's: a rebase merge drops
# it before main (ADR-0016). A closing keyword counts before `#n`,
# `owner/repo#n` and an issue URL, as GitHub reads all three.
#
# Usage: commit-subjects.sh <commits.jsonl>
#   a stream of JSON objects:
#   {"sha": "...", "author": "<login>", "parents": <count>, "message": "..."}
# or: commit-subjects.sh --git <revision-range>
#   the commits in a range of the clone, such as origin/main..HEAD. This is
#   how the repo's PreToolUse hook runs it before a push (#1371), on a
#   workstation with no jq; the rules are the same lines either way.
# Exit 0 when every commit passes, 1 when one doesn't (each is named on
# stdout), 2 when the input can't be read.
set -uo pipefail

usage='usage: commit-subjects.sh <commits.jsonl> | --git <revision-range>'
[ $# -ge 1 ] || { echo "$usage" >&2; exit 2; }

types='feat|fix|refactor|docs|test|chore|ci'
keyword='(^|[^[:alnum:]_])(close[sd]?|fix(e[sd])?|resolve[sd]?):?[[:space:]]+(([[:alnum:]_.-]+/[[:alnum:]_.-]+)?#|https://github\.com/[^/[:space:]]+/[^/[:space:]]+/issues/)[0-9]+'
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

failed=0
# judge <sha> <author> <parents>, with the message in $work/message.
judge() {
	local sha=$1 author=$2 parents=$3 subject length reason said=()
	[ "$parents" -gt 1 ] && return
	case $author in
	dependabot\[bot\] | dependabot) return ;;
	esac
	subject=$(head -n 1 "$work/message")
	if ! printf '%s\n' "$subject" | grep -Eq "^($types)(\([^()]+\))?!?: [^[:space:]]"; then
		said+=("the subject isn't \`type(scope): summary\` with a type of ${types//|/, }")
	elif printf '%s\n' "$subject" | grep -Eq '^[a-z]+\([^()]*[, +][^()]*\)'; then
		said+=("the scope names more than one thing; use one scope, or none")
	fi
	length=$(printf '%s' "$subject" | LC_ALL=C.UTF-8 wc -m)
	if [ "$length" -gt 80 ]; then
		said+=("the subject is $length characters; cut it to 80 or fewer")
	fi
	if LC_ALL=C grep -Eiq "$keyword" "$work/message"; then
		said+=("a closing keyword sits next to an issue number; link the issue in the PR body, and write around it here")
	fi
	for reason in "${said[@]}"; do
		echo "$sha: $reason"
		failed=1
	done
}

if [ "$1" = --git ]; then
	# The commits a push would send, read from the clone: the repo's
	# PreToolUse hook runs this before `git push` (#1371).
	[ $# -eq 2 ] || { echo "$usage" >&2; exit 2; }
	shas=$(git rev-list --reverse "$2") || exit 2
	for sha in $shas; do
		git log -1 --format=%B "$sha" > "$work/message" || exit 2
		parents=$(git rev-list --parents -n 1 "$sha" | wc -w)
		judge "${sha:0:8}" "$(git log -1 --format=%an "$sha")" $((parents - 1))
	done
else
	commits=$1
	[ -r "$commits" ] || exit 2
	count=$(jq -s 'length' "$commits") || exit 2
	for ((index = 0; index < count; index++)); do
		jq -s -j ".[$index].message" "$commits" > "$work/message"
		judge "$(jq -s -r ".[$index].sha[0:8]" "$commits")" \
			"$(jq -s -r ".[$index].author // \"\"" "$commits")" \
			"$(jq -s -r ".[$index].parents // 1" "$commits")"
	done
fi
if [ "$failed" -ne 0 ]; then
	echo "Commit subjects: docs/CONVENTIONS.md, \"Commits / PR titles\". Fix them with an interactive rebase and a force-push."
	exit 1
fi
exit 0
