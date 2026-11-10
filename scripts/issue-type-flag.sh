#!/usr/bin/env bash
# The issue-guards typing step (ADR-0014, #1474). Issue forms pre-apply a
# type label and print their headings; a gh or API filing skips the form, so
# this flags `needs-type` on an issue that arrives
#   - with no type label, or
#   - typed, but without its form's required headings, or, for a bug,
#     without a `severity:*` label (scripts/issue-headings.sh, which reads
#     them from the forms);
# and takes the flag off once an edit or a label fixes it.
#
# The headings are held on an issue created from the day the rule began:
# one filed before is fixed when someone picks it up, so an edit to it is
# not flagged. A missing heading is told once, in a comment with a marker,
# naming only the headings the form wants; nothing of the issue's own text
# is echoed. An untyped issue gets the label alone, as before.
#
# Exempt: a `community` issue, from the Report a problem form, which sets no
# type; triage types it (ADR-0014, #502). Its flag, if any, comes off.
#
# The issue is fetched rather than read from the event, so a label event
# judges the current labels and body. The body goes to a file, never
# through `env:` (#207).
#
# Usage (the workflow sets GH_TOKEN and NUM):
#   GITHUB_REPOSITORY=o/r NUM=7 issue-type-flag.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
forms=${ISSUE_FORMS:-$here/../.github/ISSUE_TEMPLATE}
repo=${GITHUB_REPOSITORY:?}
num=${NUM:?}
summary=${GITHUB_STEP_SUMMARY:-/dev/null}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
headings_from=2026-11-10T15:00:00Z
marker='<!-- spool:needs-headings -->'

gh api "repos/$repo/issues/$num" > "$work/issue.json"
jq -r '.body // ""' "$work/issue.json" > "$work/body.md"
jq -r '.labels[].name' "$work/issue.json" > "$work/labels"
created=$(jq -r '.created_at // ""' "$work/issue.json")
labelled() { grep -qxF -- "$1" "$work/labels"; }
clear() {
	if labelled needs-type; then
		gh issue edit "$num" -R "$repo" --remove-label needs-type
		echo "Issue #$num $1 — cleared \`needs-type\`." >> "$summary"
	else
		echo "Issue #$num $1 — nothing to do." >> "$summary"
	fi
}

types=$(bash "$here/issue-headings.sh" --types)
n=$(grep -cE "^($types)$" "$work/labels" || true)

if [ "$n" -eq 0 ] && labelled community; then
	clear "is a community report, typed at triage"
	exit 0
fi
if [ "$n" -eq 0 ]; then
	if ! labelled needs-type; then
		gh issue edit "$num" -R "$repo" --add-label needs-type
	fi
	echo "Issue #$num has no type label — flagged \`needs-type\`." >> "$summary"
	exit 0
fi

if [ -n "$created" ] && [[ $created < $headings_from ]]; then
	clear "is typed, filed before the headings rule"
	exit 0
fi

status=0
bash "$here/issue-headings.sh" "$forms" "$work/labels" < "$work/body.md" > "$work/missing" || status=$?
case $status in
0)
	clear "is typed and carries its form's headings"
	;;
1)
	# Told once: a later failing edit finds the marker and stays quiet. Read
	# into a variable, before any write, so a failed read fails the step
	# under set -e rather than reading as "not told" and posting again.
	ours=$(gh api "repos/$repo/issues/$num/comments" --paginate \
		--jq ".[] | select(.user.login == \"github-actions[bot]\") | .body")
	told=$(grep -cF -- "$marker" <<<"$ours" || true)
	if ! labelled needs-type; then
		gh issue edit "$num" -R "$repo" --add-label needs-type
	fi
	if [ "$told" -eq 0 ]; then
		{
			echo "$marker"
			echo "This issue was filed without everything its form asks for, so it's labelled \`needs-type\`. When you file with \`gh\`, copy the form's \`###\` headings in order (see the file-issue skill and [the forms](https://github.com/$repo/tree/main/.github/ISSUE_TEMPLATE)). Missing:"
			echo
			sed 's/^/- /' "$work/missing"
			echo
			echo "Edit the body or add the label, and \`needs-type\` comes off by itself once it passes."
			echo
			echo '— Spool maintainers'
			echo
			echo '<sub>Posted automatically by the <code>issue-guards</code> workflow (.github/workflows/secret-redact.yml).</sub>'
		} > "$work/note.md"
		gh issue comment "$num" -R "$repo" --body-file "$work/note.md"
	fi
	echo "Issue #$num is typed but misses its form's headings — flagged \`needs-type\`." >> "$summary"
	;;
*)
	echo "issue-headings exited $status" >&2
	exit 1
	;;
esac
