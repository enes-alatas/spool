#!/usr/bin/env bash
# Says whether a commit may be tagged: no feature merged since the last tag
# is half-shipped (#1446).
#
#   bash scripts/release-gate.sh [--tag <tag>] [<ref>]
#
# <ref> is the commit to release, origin/main when absent: run
# `git fetch --tags origin` first and it answers "is main releasable
# today?". --tag names the tag being cut, so a tag's own run measures from
# the tag before it. release.yml runs this before anything is built.
#
# For every `feat` PR merged into main after the last v* tag and reachable
# from <ref>, the PR's `Other half:` line (#634) is read. Each issue it
# names, as #n or as an issue URL, must be closed: an open one is a hub
# half shipped without its room, or the reverse, and the gate fails naming
# both. A line that argues the half away (`none — <reason>`, `API-only`,
# `UI-only`), or names no issue, passes. A PR with no line passes too:
# pr-guards decided at review whether it needed one.
#
# v0.8.0 would have shipped Teams this way: the hub's half merged and an
# upgrade making a `default` team, with no page to see it. The operator
# decided on 2026-11-09 that no tag goes out while a feature is half-shipped.
#
# Two reads go to GitHub, each replaceable for the test:
#   $RELEASE_GATE_PRS <since>    prints the PRs merged into main since that
#                                date, as gh's JSON: number, title, body,
#                                mergeCommit.oid
#   $RELEASE_GATE_STATE <n>      prints issue n's state, open or closed
# Exit: 0 releasable, 1 a feature is half-shipped, 2 the gate could not tell.
set -uo pipefail

merged_prs() {
	gh pr list --state merged --base main --search "merged:>=$1" --limit 1000 \
		--json number,title,body,mergeCommit
}

# The issues endpoint answers for a pull request too; an open PR named as
# the other half is as unshipped as an open issue.
issue_state() {
	gh api "repos/{owner}/{repo}/issues/$1" --jq .state
}

tag=
while [ "$#" -gt 0 ]; do
	case $1 in
	--tag)
		[ "$#" -ge 2 ] || { echo "release-gate: --tag needs a tag" >&2; exit 2; }
		tag=$2
		shift 2
		;;
	-h | --help)
		sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	-*) echo "release-gate: unknown option $1" >&2; exit 2 ;;
	*) break ;;
	esac
done
[ "$#" -le 1 ] || { echo "usage: bash scripts/release-gate.sh [--tag <tag>] [<ref>]" >&2; exit 2; }
ref=${1:-origin/main}
list=${RELEASE_GATE_PRS:-merged_prs}
state_of=${RELEASE_GATE_STATE:-issue_state}

# A tool that is missing reads as no PR and no line, so as releasable: it
# fails the gate instead (#1447).
need=(git jq perl)
[ -n "${RELEASE_GATE_PRS:-}" ] && [ -n "${RELEASE_GATE_STATE:-}" ] || need+=(gh)
for tool in "${need[@]}"; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "release-gate: $tool is not installed; it is needed to tell, so nothing was checked." >&2
		exit 2
	fi
done

if ! head=$(git rev-parse --verify --quiet "$ref^{commit}"); then
	echo "release-gate: $ref is not a commit here; run git fetch --tags origin first" >&2
	exit 2
fi

describe=(git describe --tags --abbrev=0 --match 'v*')
[ -z "$tag" ] || describe+=(--exclude "$tag")
if last=$("${describe[@]}" "$head" 2>/dev/null); then
	# The search counts days; ancestry decides. Two days back covers a
	# clock and a time zone between the tag's commit and the merge.
	# perl, not date -d, which BSD and macOS lack (#1447).
	since=$(perl -MPOSIX -e 'print strftime("%Y-%m-%d", gmtime($ARGV[0] - 2 * 86400))' \
		"$(git log -1 --format=%ct "$last^{commit}")")
else
	last=
	since=1970-01-01
fi
# An empty or malformed date would search nothing and pass every tag.
if ! [[ $since =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
	echo "release-gate: could not compute the date of $last ('$since'); nothing was checked." >&2
	exit 2
fi

if ! prs=$($list "$since"); then
	echo "release-gate: could not list the PRs merged since $since; the API failed, not main. Re-run it." >&2
	exit 2
fi

# One line per feat PR: number, merge commit, title; its body beside it.
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
if ! jq -r '.[] | select(.title | test("^feat(\\([^)]*\\))?!?:"))
		| "\(.number)\t\(.mergeCommit.oid // "")\t\(.title)"' <<<"$prs" >"$work/feats"; then
	echo "release-gate: the PR list is not the JSON gh prints" >&2
	exit 2
fi

shipped() {
	[ -n "$1" ] && git merge-base --is-ancestor "$1" "$head" 2>/dev/null &&
		{ [ -z "$last" ] || ! git merge-base --is-ancestor "$1" "$last" 2>/dev/null; }
}

checked=0
blocked=0
while IFS=$'\t' read -r number oid title; do
	shipped "$oid" || continue
	checked=$((checked + 1))
	# As other-half.sh reads it: outside HTML comments, a bullet or bold
	# around the label allowed, the first such line.
	# A step that fails would read as no line; it fails the gate instead.
	if ! jq -r --argjson n "$number" '.[] | select(.number == $n) | .body // ""' <<<"$prs" >"$work/body" ||
		! value=$(perl -0pe 's/<!--.*?-->//gs' "$work/body" |
			perl -ne 'if (s/^\s*(?:[-*]\s+)?(?:\*\*|__)?other half:(?:\*\*|__)?//i) { print; exit }' |
			sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//'); then
		echo "release-gate: could not read #$number's Other half: line; nothing was decided." >&2
		exit 2
	fi
	# Argued away: whatever the reason goes on to cite is not the half.
	if [ -z "$value" ] || grep -qiE '^(none([^[:alnum:]]|$)|(api|ui)-only)' <<<"$value"; then
		continue
	fi
	numbers=$(grep -oE '(^|[^[:alnum:]&/])#[0-9]+|/issues/[0-9]+' <<<"$value" | grep -oE '[0-9]+$' | sort -un)
	for n in $numbers; do
		if ! state=$($state_of "$n"); then
			echo "release-gate: could not read #$n, which #$number names; the API failed, not main. Re-run it." >&2
			exit 2
		fi
		if [ "$state" = open ]; then
			echo "release-gate: #$number ($title) shipped one half; its other half, #$n, is still open." >&2
			blocked=$((blocked + 1))
		fi
	done
done <"$work/feats"

from=${last:-the first commit}
if [ "$blocked" -gt 0 ]; then
	echo "release-gate: $ref is not releasable: $blocked other half(s) open among the $checked feat PR(s) since $from." >&2
	echo "  Merge or close each named issue, or tag a commit before its PR." >&2
	exit 1
fi
echo "release-gate: $ref is releasable: no other half open among the $checked feat PR(s) since $from."
