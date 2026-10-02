#!/usr/bin/env bash
# The welcome step of .github/workflows/secret-redact.yml (the `issue-guards`
# workflow): an issue opened by someone outside the team gets the `community`
# and `triage` labels and one fixed reply saying what happens next (#503,
# ADR-0014).
#
# The reply is .github/community-welcome.md, verbatim. Nothing from the issue
# goes into it: a community issue's text is untrusted input (ADR-0014), and a
# reply that quoted it would publish whatever it carried under the project's
# name. The script reads only the issue's number and its author association.
#
# The labels are added even when the reporter used the Report a problem form,
# which already sets them: an issue filed through the API, or through a team
# form, carries no `community` label, and adding a label already present is a
# no-op. They are added before the typing step runs, which leaves a
# `community` issue unflagged.
#
# It replies at most once. The workflow runs it only on `opened`, and the
# reply carries a marker the script looks for first, so a dispatch against an
# issue that was already welcomed posts nothing.
#
# Usage (the workflow sets GH_TOKEN and NUM; the rest comes from the event):
#   GITHUB_REPOSITORY=o/r GITHUB_EVENT_PATH=event.json NUM=7 community-welcome.sh
#
# On a dispatch the event file holds inputs, not a payload. An
# `author_association` input stands in for the payload's, which is how a
# maintainer proves the welcome path on an issue they opened themselves
# (#209); without it, the association is fetched.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=${GITHUB_REPOSITORY:?}
event=${GITHUB_EVENT_PATH:?}
num=${NUM:?}
summary=${GITHUB_STEP_SUMMARY:-/dev/null}
note="$here/../.github/community-welcome.md"
marker='<!-- spool:community-welcome -->'

association=$(jq -r '.issue.author_association // .inputs.author_association // empty' "$event")
if [ -z "$association" ]; then
  association=$(gh api "repos/$repo/issues/$num" --jq .author_association)
fi

case "$association" in
OWNER | MEMBER | COLLABORATOR)
  echo "Issue #$num was opened by the team ($association) — no welcome." >>"$summary"
  exit 0
  ;;
esac

gh issue edit "$num" -R "$repo" --add-label community --add-label triage

# Read whole before searching. Piped straight into `grep -q`, the first
# match closes the pipe on a gh still writing, and under pipefail that
# SIGPIPE turns "already welcomed" into "not yet": a second reply.
comments=$(gh api --paginate "repos/$repo/issues/$num/comments" --jq '.[].body')
if grep -qF "$marker" <<<"$comments"; then
  echo "Issue #$num is labelled; it was already welcomed, so no second reply." >>"$summary"
  exit 0
fi
gh issue comment "$num" -R "$repo" --body-file "$note"
echo "Issue #$num ($association) is labelled \`community\` and \`triage\`, and welcomed." >>"$summary"
