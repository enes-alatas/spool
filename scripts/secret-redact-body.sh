#!/usr/bin/env bash
# The secret-redact workflow's body, as a script rather than a `run:` block.
#
# It reads the event GitHub wrote to disk, redacts the body it names, writes
# the result back and posts the incident note. Two things that were wrong when
# this lived inline are why it is a file (#207):
#
#   1. Actions runs a `run:` block as `bash -e`, and secret-redact.sh reports
#      a hit by exiting 1. Under -e those are the same event: the runner ended
#      the step at the scan, before `status=$?` was ever read, so the guard
#      did nothing on every body that actually held a credential and said
#      nothing while not doing it. An exit-status protocol and -e have to be
#      reconciled deliberately — hence `|| status=$?` below, which is the
#      whole fix and easy to delete by tidying.
#   2. A body handed to a step through `env:` is printed in that step's "Run"
#      group header, so a credential in an issue body was copied verbatim into
#      an Actions log. The body is read from $GITHUB_EVENT_PATH instead — the
#      runner writes it to disk and echoes none of it — and never enters the
#      environment.
#
# A file is also something a test can run, which is what scripts/
# secret-redact-body-test.sh does under `bash -e` with a stub `gh`.
#
# Usage (the workflow sets GH_TOKEN; the rest comes from the event):
#   GITHUB_EVENT_NAME=issues GITHUB_EVENT_PATH=event.json secret-redact-body.sh
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=${GITHUB_REPOSITORY:?}
event=${GITHUB_EVENT_PATH:?}
name=${GITHUB_EVENT_NAME:?}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# The body, and where it lives. A comment's body wins over an issue's because
# an `issue_comment` event carries both, and the comment is the one that fired.
jq -r '.comment.body // .issue.body // ""' "$event" > "$work/body.md"

case "$name" in
  pull_request_review_comment)
    target="repos/$repo/pulls/comments/$(jq -r '.comment.id' "$event")"
    ;;
  issue_comment)
    target="repos/$repo/issues/comments/$(jq -r '.comment.id' "$event")"
    ;;
  *)
    target="repos/$repo/issues/$(jq -r '.issue.number' "$event")"
    ;;
esac
# Where the incident note goes: the thread the body sits in. A review
# comment's thread is the pull request, which is an issue for this.
thread=$(jq -r '.issue.number // .pull_request.number' "$event")

# An issue opened with no body at all: nothing to read, nothing to do.
if [ ! -s "$work/body.md" ]; then
  exit 0
fi

status=0
bash "$here/secret-redact.sh" < "$work/body.md" > "$work/redacted.md" 2>/dev/null || status=$?

# Its own edit fires this workflow again; the marker holds no shape, so the
# second run ends here rather than looping.
if [ "$status" -eq 0 ]; then
  exit 0
fi

# Only 1 means "replaced something". Any other status is the guard failing —
# an unreadable rules file, an awk that died mid-body — and treating that as a
# hit would PATCH a truncated body over the report while posting a note saying
# it had been handled. The original would then survive only in the revision
# history the note tells the author to delete. Fail loudly, leave the body.
if [ "$status" -ne 1 ] || [ ! -s "$work/redacted.md" ]; then
  echo "secret-redact exited $status; leaving the body untouched" >&2
  exit 1
fi

# Checked, because the note that follows says the value is gone. A failed
# write with the note posted anyway is worse than silence: it tells the author
# to stop worrying about a credential still sitting in the text above it.
if ! gh api --method PATCH "$target" -F "body=@$work/redacted.md" --silent; then
  echo "could not write the redacted body back" >&2
  exit 1
fi

# Deliberately says nothing about what was found. The count, the shape and the
# position are all facts about a value that has just been taken out of public
# view; a workflow that narrates them puts half of it back. The copy lives in
# a file rather than a heredoc so it can be read and edited as prose.
gh api --method POST "repos/$repo/issues/$thread/comments" \
  -F "body=@$here/../.github/secret-redact-note.md" --silent
