#!/usr/bin/env bash
# The one step of .github/workflows/community-stale.yml (#1690): an outside
# contributor's pull request that waits on its author is labelled `stale`
# with a fixed comment after QUIET_DAYS quiet days, and closed with another
# fixed comment after QUIET_DAYS more. CONTRIBUTING.md states the rule.
#
# A PR is considered only when it is open, not a draft, from a fork, and its
# author is not the team (OWNER, MEMBER, COLLABORATOR), the test
# community-pr.sh applies. It waits on its author when:
# - the team's latest review that decides, Request changes or Approve, is
#   Request changes. Only the team counts (OWNER, MEMBER, COLLABORATOR on
#   the review): any account can review a public PR. A Comment review
#   neither starts nor stops the count, since the team posts its
#   safe-to-run reads and status notes that way (CONVENTIONS.md);
# - that review is on the PR's head commit, so the author has not pushed
#   since; and
# - the author has not commented, reviewed or commented on a line since it.
# Its quiet days count from that review. The team's own later comments do
# not restart them. Anything else clears the PR: it waits on us, and a
# `stale` label on it comes off.
#
# Nothing a PR's author wrote reaches a call or a comment: the comments are
# .github/community-stale.md and .github/community-stale-close.md, verbatim
# (ADR-0069 item 2). The script reads numbers, commit ids, logins compared
# for equality, review states and dates.
#
# Usage (the workflow sets GH_TOKEN and GITHUB_REPOSITORY):
#   DRY_RUN=true community-stale.sh    names what it would do, does nothing
# NOW (epoch seconds) and QUIET_DAYS stand in for the clock and the period
# in tests.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=${GITHUB_REPOSITORY:?}
dry=${DRY_RUN:-true}
now=${NOW:-$(date +%s)}
days=${QUIET_DAYS:-7}
summary=${GITHUB_STEP_SUMMARY:-/dev/null}
note="$here/../.github/community-stale.md"
close_note="$here/../.github/community-stale-close.md"
marker='<!-- spool:community-stale -->'
period=$((days * 86400))

say() { printf '%s\n' "$1" | tee -a "$summary"; }
act() {
  if [ "$dry" = "true" ]; then
    say "  would: $1"
  else
    say "  $1"
  fi
}

# number, draft, fork, association, author, head and whether it is labelled
# stale, one PR a line. Nothing free-text.
prs=$(gh api --paginate "repos/$repo/pulls?state=open&per_page=100" --jq '.[] |
  [.number, .draft, (.head.repo.fork // false), .author_association, .user.login, .head.sha,
   ([.labels[].name] | index("stale") != null)] | @tsv')

named=0
while IFS=$'\t' read -r num draft fork association author head labelled; do
  [ -n "$num" ] || continue
  [ "$draft" = "false" ] && [ "$fork" = "true" ] || continue
  case "$association" in OWNER | MEMBER | COLLABORATOR) continue ;; esac

  # The team's latest deciding review: time, state, commit.
  review=$(gh api --paginate "repos/$repo/pulls/$num/reviews?per_page=100" --jq \
    ".[] | select(.user.login != \"$author\" and (.state == \"CHANGES_REQUESTED\" or .state == \"APPROVED\" or .state == \"DISMISSED\") and (.author_association == \"OWNER\" or .author_association == \"MEMBER\" or .author_association == \"COLLABORATOR\")) | [.submitted_at, .state, .commit_id] | @tsv" |
    sort | tail -n 1)
  waits=false
  if [ -n "$review" ]; then
    IFS=$'\t' read -r at state commit <<<"$review"
    if [ "$state" = "CHANGES_REQUESTED" ] && [ "$commit" = "$head" ]; then
      # The author's latest word since that review, on any of the three
      # surfaces. Read whole before testing (see community-pr.sh).
      since=$(
        {
          gh api --paginate "repos/$repo/issues/$num/comments?per_page=100" --jq ".[] | select(.user.login == \"$author\") | .created_at"
          gh api --paginate "repos/$repo/pulls/$num/comments?per_page=100" --jq ".[] | select(.user.login == \"$author\") | .created_at"
          gh api --paginate "repos/$repo/pulls/$num/reviews?per_page=100" --jq ".[] | select(.user.login == \"$author\") | .submitted_at"
        } | awk -v at="$at" '$0 > at' | head -n 1
      )
      [ -z "$since" ] && waits=true
    fi
  fi

  if [ "$waits" != "true" ]; then
    if [ "$labelled" = "true" ]; then
      say "PR #$num waits on us, not its author."
      act "remove \`stale\`"
      [ "$dry" = "true" ] || gh api -X DELETE "repos/$repo/issues/$num/labels/stale" --silent
    fi
    continue
  fi

  quiet=$(jq -rn --arg at "$at" --argjson now "$now" '$now - ($at | fromdateiso8601)')
  if [ "$quiet" -lt "$period" ]; then
    continue
  fi
  named=$((named + 1))

  # When this workflow last said so, counted only from its own identity and
  # only since the review: anyone can write the marker (ADR-0069 item 6).
  warned=$(gh api --paginate "repos/$repo/issues/$num/comments?per_page=100" --jq \
    ".[] | select(.user.login == \"github-actions[bot]\" and (.body | contains(\"$marker\"))) | .created_at" |
    awk -v at="$at" '$0 > at' | sort | tail -n 1)

  # Unlabelled means a fresh count: a new case, a reset, a reopen, or a
  # maintainer taking the label off. Each gets the comment again, so the
  # close counts from a comment the author has had 7 days to see.
  if [ "$labelled" != "true" ] || [ -z "$warned" ]; then
    say "PR #$num has waited on its author $((quiet / 86400)) days: stale."
    act "label \`stale\` and post .github/community-stale.md"
    if [ "$dry" != "true" ]; then
      gh api -X POST "repos/$repo/issues/$num/labels" -f 'labels[]=stale' --silent
      gh pr comment "$num" -R "$repo" --body-file "$note"
    fi
    continue
  fi

  since_warned=$(jq -rn --arg at "$warned" --argjson now "$now" '$now - ($at | fromdateiso8601)')
  if [ "$since_warned" -ge "$period" ]; then
    say "PR #$num has waited on its author $((quiet / 86400)) days, $((since_warned / 86400)) since it was marked stale: closed."
    act "close with .github/community-stale-close.md, and take \`stale\` off"
    if [ "$dry" != "true" ]; then
      gh pr close "$num" -R "$repo" --comment "$(cat "$close_note")"
      # Off, so a reopened PR starts a fresh count with a fresh comment
      # rather than closing again on the old one.
      gh api -X DELETE "repos/$repo/issues/$num/labels/stale" --silent
    fi
  else
    say "PR #$num is stale: it closes $(((period - since_warned + 86399) / 86400)) days from now without a reply."
  fi
done <<<"$prs"

[ "$named" -gt 0 ] || say "No outside PR has waited on its author $days days."
