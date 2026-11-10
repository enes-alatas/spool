#!/usr/bin/env bash
# The one step of .github/workflows/community-pr.yml (ADR-0069 item 1): a
# pull request from a fork gets the `community` and `needs-security-review`
# labels and one fixed comment saying what happens next.
#
# The comment is .github/community-pr.md, verbatim. Nothing from the PR goes
# into it or into any call: a community PR's text is untrusted input
# (ADR-0069 item 2). The script reads only the PR's number, whether its head
# is in a fork, and its author association.
#
# The workflow runs on pull_request_target, so this script is the base
# branch's copy, checked out with no ref: the PR cannot change what runs here.
# Nothing of the PR's head is fetched, built or run.
#
# A PR the team opened from a fork is skipped: OWNER, MEMBER and
# COLLABORATOR are the team. It comments at most once: the comment carries a
# marker the script looks for first, so a reopen or a dispatch against a PR
# already welcomed only labels again, which is a no-op.
#
# Usage (the workflow sets GH_TOKEN and NUM; the rest comes from the event):
#   GITHUB_REPOSITORY=o/r GITHUB_EVENT_PATH=event.json NUM=7 community-pr.sh
#
# On a dispatch the event file holds inputs, not a payload. `fork` and
# `author_association` inputs stand in for the payload's, which is how a
# maintainer proves the path on a PR of their own (#209); without them, both
# are fetched.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=${GITHUB_REPOSITORY:?}
event=${GITHUB_EVENT_PATH:?}
num=${NUM:?}
case "$num" in *[!0-9]*)
  echo "community-pr: NUM is not a PR number" >&2
  exit 2
  ;;
esac
summary=${GITHUB_STEP_SUMMARY:-/dev/null}
note="$here/../.github/community-pr.md"
marker='<!-- spool:community-pr -->'

fork=$(jq -r 'if .pull_request.head.repo.fork != null then .pull_request.head.repo.fork else .inputs.fork // empty end | tostring' "$event")
association=$(jq -r '.pull_request.author_association // .inputs.author_association // empty' "$event")
if [ -z "$fork" ] || [ -z "$association" ]; then
  fetched=$(gh api "repos/$repo/pulls/$num" --jq '"\(.head.repo.fork) \(.author_association)"')
  fork=${fork:-${fetched% *}}
  association=${association:-${fetched#* }}
fi

if [ "$fork" != "true" ]; then
  echo "PR #$num is not from a fork: nothing to do." >>"$summary"
  exit 0
fi
case "$association" in
OWNER | MEMBER | COLLABORATOR)
  echo "PR #$num is from a fork, but the team opened it ($association): nothing to do." >>"$summary"
  exit 0
  ;;
esac

# Through the issues endpoint, as pr-guards labels: on a PR the token needs
# pull-requests: write for it (#743).
gh api -X POST "repos/$repo/issues/$num/labels" -f 'labels[]=community' -f 'labels[]=needs-security-review' --silent

# Only this workflow's own comments count: anyone can write the marker, and
# a contributor who posted it first would otherwise silence the welcome
# (ADR-0069 item 6). The workflow posts as github-actions[bot].
#
# Read whole before searching (see community-welcome.sh: under pipefail a
# grep -q that closes the pipe early reads as "not yet", and posts twice).
comments=$(gh api --paginate "repos/$repo/issues/$num/comments" --jq '.[] | select(.user.login == "github-actions[bot]") | .body')
if grep -qF "$marker" <<<"$comments"; then
  echo "PR #$num is labelled; it was already welcomed, so no second comment." >>"$summary"
  exit 0
fi
gh pr comment "$num" -R "$repo" --body-file "$note"
echo "PR #$num ($association, from a fork) is labelled \`community\` and \`needs-security-review\`, and welcomed." >>"$summary"
