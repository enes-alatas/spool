#!/usr/bin/env bash
# The merge queue's steps that write (#819). The repo is owned by a user,
# and GitHub's own merge queue is only for organizations, so the queue is
# this: a same-repo PR carrying the `queue` label and set to auto-merge
# waits its turn, the oldest first, and only the one at the head is tested,
# rebased onto the main it would land on. Its `queue` status, a required
# check, is what lets auto-merge take it.
#
#   scripts/queue.sh pick     marks every open same-repo PR's `queue` status
#                             and prints pr=, head= and base= for the head
#                             of the queue (nothing when it is empty) on
#                             stdout, for $GITHUB_OUTPUT
#   scripts/queue.sh report   with PR, HEAD, BASE and RESULT set: turns the
#                             tested PR's status green or red, but only if
#                             main, the PR, its label and auto-merge are
#                             still where the test found them; otherwise a
#                             newer run owns it
#   scripts/queue.sh unqueue  with PR set: takes a PR that was pushed to out
#                             of the queue (unqueue.yml)
#
# None of them runs a PR's code: each reads the API and writes statuses. The
# job that runs the code holds a read-only token (ADR-0054).
#
# Needs GITHUB_REPOSITORY and an authenticated `gh`. RUN_URL, when set, is
# where each status links.
set -euo pipefail

repo=${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is not set}
label=queue

short() { printf '%.7s' "$1"; }

main_sha() { gh api "repos/$repo/commits/main" --jq .sha; }

# current SHA is the `queue` status SHA carries, as "state description".
current() {
  gh api "repos/$repo/commits/$1/status" \
    --jq '[.statuses[] | select(.context == "queue")][0] | "\(.state) \(.description)"'
}

# status SHA STATE DESCRIPTION posts the `queue` status, unless it already
# reads exactly that: a push to main re-marks every open PR, and a status
# unchanged need not be posted again.
status() {
  if [ "$(current "$1")" = "$2 $3" ]; then
    return 0
  fi
  local args=(--method POST "repos/$repo/statuses/$1" -f state="$2" -f context=queue -f description="$3")
  if [ -n "${RUN_URL:-}" ]; then
    args+=(-f target_url="$RUN_URL")
  fi
  gh api "${args[@]}" > /dev/null
}

pick() {
  local base prs head="" position=0 number sha queued auto now
  local -a members=() shas=() states=()
  base=$(main_sha)
  # Same-repo PRs only. A fork's PR is never queued: its code would be
  # tested by a run started from main, and a maintainer who wants it in
  # pushes its branch here first (ADR-0054). Oldest number first.
  prs=$(gh api "repos/$repo/pulls?state=open&base=main&per_page=100" |
    jq -r --arg repo "$repo" --arg label "$label" '
      [.[] | select(.head.repo.full_name == $repo)]
      | sort_by(.number)[]
      | "\(.number) \(.head.sha) \([.labels[].name] | index($label) != null) \(.auto_merge != null)"')
  while read -r number sha queued auto; do
    [ -n "$number" ] || continue
    now=$(current "$sha")
    # In the queue means labelled by the reviewer and set to auto-merge by
    # the operator: both decisions, so the head is always a PR that merges
    # the moment it is green.
    if [ "$queued" != true ]; then
      # Pending, not absent: a PR taken out of the queue must not keep a
      # green status from a main that has since moved.
      status "$sha" pending "not queued: add the queue label to merge"
      continue
    fi
    if [ "$auto" != true ]; then
      status "$sha" pending "not queued: waiting for auto-merge"
      continue
    fi
    # A red head stays red, and the queue moves past it, until its author
    # pushes (a new head has no status) or takes it out and queues it again.
    # Testing it again on every event would hold the queue on it.
    if [ "${now%% *}" = failure ]; then
      continue
    fi
    members+=("$number") shas+=("$sha") states+=("$now")
  done <<< "$prs"
  [ ${#members[@]} -gt 0 ] || return 0

  # A PR already under test or green on this main keeps the head: a lower
  # number queued after it waits its turn rather than taking the head and
  # throwing that test away.
  head=0
  for i in "${!members[@]}"; do
    case "${states[$i]}" in
      "pending testing on main@$(short "$base")" | "success tested on main@$(short "$base")")
        head=$i
        break
        ;;
    esac
  done

  status_head "${members[$head]}" "${shas[$head]}" "${states[$head]}" "$base"
  for i in "${!members[@]}"; do
    [ "$i" -ne "$head" ] || continue
    position=$((position + 1))
    status "${shas[$i]}" pending "queued at $((position + 1)), behind #${members[$head]}"
  done
}

# status_head NUMBER SHA NOW BASE marks the head of the queue and prints
# what `test` needs, unless it is already green on this very main: then
# there is nothing to test, and the green stays for auto-merge to act on.
status_head() {
  if [ "$3" = "success tested on main@$(short "$4")" ]; then
    return 0
  fi
  status "$2" pending "testing on main@$(short "$4")"
  printf 'pr=%s\nhead=%s\nbase=%s\n' "$1" "$2" "$4"
}

# unqueue takes PR out of the queue after a push to it: the new head was
# not what the reviewer approved or the operator set to merge, so it
# re-enters only through both of them again (ADR-0054). GitHub turns
# auto-merge off on a push only when the pusher cannot write, and every
# loop can, so the queue does it. The label goes first: off the queue is
# what keeps the head from a green, and auto-merge off is what makes the
# operator decide again.
unqueue() {
  : "${PR:?}"
  local pr labelled auto node sha
  pr=$(gh api "repos/$repo/pulls/$PR")
  read -r labelled auto node sha < <(jq -r --arg label "$label" \
    '"\([.labels[].name] | index($label) != null) \(.auto_merge != null) \(.node_id) \(.head.sha)"' <<< "$pr")
  if [ "$labelled" = true ]; then
    gh api --method DELETE "repos/$repo/issues/$PR/labels/$label" > /dev/null
  fi
  if [ "$auto" = true ]; then
    gh api --method POST graphql -f id="$node" \
      -f query='mutation($id: ID!) { disablePullRequestAutoMerge(input: {pullRequestId: $id}) { clientMutationId } }' > /dev/null
  fi
  if [ "$labelled" = true ] || [ "$auto" = true ]; then
    status "$sha" pending "not queued: pushed after queueing, review it again"
    # For $GITHUB_OUTPUT: the queue behind it moves up, and a run must say so.
    echo unqueued=true
  else
    status "$sha" pending "not queued: add the queue label to merge"
  fi
}

report() {
  : "${PR:?}" "${HEAD:?}" "${BASE:?}" "${RESULT:?}"
  local now pr_now
  now=$(main_sha)
  if [ "$now" != "$BASE" ]; then
    echo "main moved to $(short "$now") during the test of #$PR on $(short "$BASE"); the run main's push started tests it again"
    return 0
  fi
  pr_now=$(gh api "repos/$repo/pulls/$PR" |
    jq -r --arg label "$label" '"\(.state) \(.head.sha) \([.labels[].name] | index($label) != null) \(.auto_merge != null)"')
  if [ "$pr_now" != "open $HEAD true true" ]; then
    echo "#$PR changed during its test ($pr_now); the run that change started owns it"
    return 0
  fi
  case "$RESULT" in
    success) status "$HEAD" success "tested on main@$(short "$BASE")" ;;
    failure) status "$HEAD" failure "failed on main@$(short "$BASE"): see the run" ;;
    *) echo "the test of #$PR ended $RESULT; its status stays pending" ;;
  esac
}

case "${1:-}" in
  pick) pick ;;
  report) report ;;
  unqueue) unqueue ;;
  *)
    echo "usage: $0 pick|report|unqueue" >&2
    exit 2
    ;;
esac
