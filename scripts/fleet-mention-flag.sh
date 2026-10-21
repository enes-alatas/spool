#!/usr/bin/env bash
# The issue-guards step that flags an @ before a fleet name (#630): it
# reads the body the event names, and when scripts/fleet-mention.sh finds
# one it adds the `fleet-mention` label to the issue or pull request the
# body sits in. The label is the whole output, as `needs-type` is: no
# comment, which would be one more body, and no mention. The ping has
# already gone out by the time this runs, so the label asks the author to
# take the @ out and a person removes it once they have.
#
# pr-guards runs it on a pull request's own body.
#
# The body is read from $GITHUB_EVENT_PATH and never through `env:`, as
# scripts/secret-redact-body.sh reads it (#207). A dispatched run carries
# inputs, not a payload, so it fetches the body by number in the same
# shapes that script does, and every line after that is the event's path.
#
# Usage (the workflow sets GH_TOKEN; the rest comes from the event):
#   GITHUB_EVENT_NAME=issues GITHUB_EVENT_PATH=event.json fleet-mention-flag.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=${GITHUB_REPOSITORY:?}
event=${GITHUB_EVENT_PATH:?}
name=${GITHUB_EVENT_NAME:?}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

if [ "$name" = workflow_dispatch ]; then
  name=$(jq -r '.inputs.kind // "issues"' "$event")
  number=$(jq -r '.inputs.issue' "$event")
  comment=$(jq -r '.inputs.comment // ""' "$event")
  case "$name" in
    issue_comment)
      gh api "repos/$repo/issues/comments/$comment" \
        --jq '{issue:{number:'"$number"'},comment:{id:.id,body:.body}}' > "$work/event.json"
      ;;
    pull_request_review_comment)
      gh api "repos/$repo/pulls/comments/$comment" \
        --jq '{pull_request:{number:'"$number"'},comment:{id:.id,body:.body}}' > "$work/event.json"
      ;;
    *)
      gh api "repos/$repo/issues/$number" \
        --jq '{issue:{number:.number,body:.body}}' > "$work/event.json"
      ;;
  esac
  event="$work/event.json"
fi

# A comment's body wins: an issue_comment event carries the issue's too.
jq -r '.comment.body // .issue.body // .pull_request.body // ""' "$event" > "$work/body.md"
thread=$(jq -r '.issue.number // .pull_request.number' "$event")

status=0
mention=$(bash "$here/fleet-mention.sh" < "$work/body.md") || status=$?
case $status in
  0)
    echo "No @ before a fleet name in the body on #$thread." >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
    ;;
  1)
    # Through the issues endpoint, which labels a pull request too.
    gh api --method POST "repos/$repo/issues/$thread/labels" -f 'labels[]=fleet-mention' --silent
    echo "A body on #$thread has an @ before $mention — flagged \`fleet-mention\`." >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
    ;;
  *)
    echo "fleet-mention exited $status" >&2
    exit 1
    ;;
esac
