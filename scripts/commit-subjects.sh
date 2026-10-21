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
# Exit 0 when every commit passes, 1 when one doesn't (each is named on
# stdout), 2 when the input can't be read.
set -uo pipefail

commits=${1:?usage: commit-subjects.sh <commits.jsonl>}
[ -r "$commits" ] || exit 2

types='feat|fix|refactor|docs|test|chore|ci'
keyword='(^|[^[:alnum:]_])(close[sd]?|fix(e[sd])?|resolve[sd]?):?[[:space:]]+(([[:alnum:]_.-]+/[[:alnum:]_.-]+)?#|https://github\.com/[^/[:space:]]+/[^/[:space:]]+/issues/)[0-9]+'
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

failed=0
count=$(jq -s 'length' "$commits") || exit 2
for ((index = 0; index < count; index++)); do
	jq -s -r ".[$index].sha[0:8]" "$commits" > "$work/sha"
	jq -s -r ".[$index].author // \"\"" "$commits" > "$work/author"
	jq -s -j ".[$index].message" "$commits" > "$work/message"
	parents=$(jq -s -r ".[$index].parents // 1" "$commits")
	[ "$parents" -gt 1 ] && continue
	sha=$(cat "$work/sha")
	case $(cat "$work/author") in
	dependabot\[bot\] | dependabot) continue ;;
	esac
	subject=$(head -n 1 "$work/message")
	said=()
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
done
if [ "$failed" -ne 0 ]; then
	echo "Commit subjects: docs/CONVENTIONS.md, \"Commits / PR titles\". Fix them with an interactive rebase and a force-push."
	exit 1
fi
exit 0
