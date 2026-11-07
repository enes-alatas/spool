#!/usr/bin/env bash
# The issue-guards step that holds an issue to the issue standard (#1035):
# it fetches the issue's title, body and labels, and when
# scripts/issue-rewrite.sh finds a broken rule it labels the issue
# `needs-rewrite` and comments once, naming each rule. Once the title and
# body pass, it takes the label off again, the way the typing step clears
# `needs-type`, so an edit is the whole fix.
#
# The issue is fetched rather than read from the event, on every path: a
# `labeled` event's payload is the one place the maintainer's
# `one-deliverable` label can arrive, and fetching after it gives the
# current title too. The body goes to a file, never through `env:` (#207).
#
# Exempt: a `community` issue, whose text is the reporter's own and is
# rewritten into a team issue at triage (ADR-0014, #502). Flagging it would
# ask an outside reporter to follow the team's standard.
#
# The comment is posted when the label goes on, not on every failing edit:
# it says what to do, and the label carries the state from then on.
#
# Usage (the workflow sets GH_TOKEN and NUM):
#   GITHUB_REPOSITORY=o/r NUM=7 issue-rewrite-flag.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=${GITHUB_REPOSITORY:?}
num=${NUM:?}
summary=${GITHUB_STEP_SUMMARY:-/dev/null}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
scenarios_from=2026-11-07T00:00:00Z

gh api "repos/$repo/issues/$num" > "$work/issue.json"
jq -r '.body // ""' "$work/issue.json" > "$work/body.md"
title=$(jq -r '.title' "$work/issue.json")
labels=$(jq -r '.labels[].name' "$work/issue.json")
labelled() { grep -qxF "$1" <<<"$labels"; }

if labelled community; then
	echo "Issue #$num is a community report, rewritten at triage — not checked." >> "$summary"
	exit 0
fi

args=()
labelled one-deliverable && args+=(--one-deliverable)
# A new feature lists its acceptance scenarios (#1377), from the day the
# rule began: one filed before gets them when it is assigned, so an edit
# to it is not flagged.
created=$(jq -r '.created_at // ""' "$work/issue.json")
if labelled "new feature" && [[ ! $created < $scenarios_from ]]; then
	args+=(--scenarios)
fi
status=0
bash "$here/issue-rewrite.sh" "${args[@]}" "$title" < "$work/body.md" > "$work/broken" || status=$?

case $status in
0)
	if labelled needs-rewrite; then
		gh issue edit "$num" -R "$repo" --remove-label needs-rewrite
		echo "Issue #$num now follows the issue standard — cleared \`needs-rewrite\`." >> "$summary"
	else
		echo "Issue #$num follows the issue standard — nothing to do." >> "$summary"
	fi
	;;
1)
	if labelled needs-rewrite; then
		echo "Issue #$num is already labelled \`needs-rewrite\` — no second comment." >> "$summary"
		exit 0
	fi
	{
		echo '<!-- spool:needs-rewrite -->'
		echo "This issue doesn't follow the issue standard in [CONVENTIONS.md](https://github.com/$repo/blob/main/docs/CONVENTIONS.md) yet, so it's labelled \`needs-rewrite\`:"
		echo
		sed 's/^/- /' "$work/broken"
		echo
		echo "Edit the title or the body, and the label comes off by itself once they pass."
		echo
		echo '— Spool maintainers'
		echo
		echo '<sub>Posted automatically by the <code>issue-guards</code> workflow (.github/workflows/secret-redact.yml).</sub>'
	} > "$work/note.md"
	gh issue edit "$num" -R "$repo" --add-label needs-rewrite
	gh issue comment "$num" -R "$repo" --body-file "$work/note.md"
	echo "Issue #$num breaks the issue standard — flagged \`needs-rewrite\`." >> "$summary"
	;;
*)
	echo "issue-rewrite exited $status" >&2
	exit 1
	;;
esac
