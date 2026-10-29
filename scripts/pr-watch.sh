#!/usr/bin/env bash
# pr-watch.sh [CLONE] prints what is red on a loop's open PRs, for a Claude
# Code UserPromptSubmit hook to put in front of its next turn (#904).
#
# A loop's PRs are the open ones whose head branch exists in its clone
# (CLONE, or the current directory), from this repo and never a fork: a
# fork's PR from its own `main` would match every clone, and its checks'
# names and annotations are a stranger's text in the loop's turn. Red is a
# failed check run, and a
# failed or errored status, the merge queue's `queue` among them. Each failed
# job comes with its first failure annotations, which CI's test steps write
# (scripts/test-failures.sh), so the loop reads which test broke without
# opening the run.
#
# It prints nothing when nothing is red, and exits 0 whatever happens: a
# UserPromptSubmit hook that exits 2 blocks the turn, and a GitHub that is
# down must not stop a loop from answering a person. It reads and writes
# nothing on GitHub beyond those reads.
#
# Hook it up in the loop's own ~/.claude/settings.json (CONVENTIONS.md,
# "Seeing your PRs' CI"), naming its clone and the variable its GitHub token
# is in. The hook runs the script as origin/main has it, never the checked-out
# branch's copy, so a PR under review can't change what runs at every turn:
#
#   {"hooks": {"UserPromptSubmit": [{"hooks": [{"type": "command", "timeout": 30,
#     "command": "GH_TOKEN=\"$GH_TOKEN_SPOOL\" bash -c \"$(git -C \"$HOME/spool\" show origin/main:scripts/pr-watch.sh)\" pr-watch \"$HOME/spool\""}]}]}}
#
# PR_WATCH_REPO is the repo (default enes-alatas/spool); PR_WATCH_NOTES the
# annotations shown per failed job (default 3).
set -uo pipefail

clone=${1:-.}
repo=${PR_WATCH_REPO:-enes-alatas/spool}
notes=${PR_WATCH_NOTES:-3}

# One line per red check or status of an open PR, as tab-separated fields:
# number, branch, what failed, its link.
red_jq='.[] | select(.isCrossRepository | not) | .number as $n | .headRefName as $b | (.statusCheckRollup // [])[] |
  if .__typename == "CheckRun" then
    select(.conclusion == "FAILURE" or .conclusion == "TIMED_OUT" or .conclusion == "STARTUP_FAILURE")
    | [$n, $b, ((if (.workflowName // "") != "" then .workflowName + " / " else "" end) + .name), (.detailsUrl // "")]
  else
    select(.state == "FAILURE" or .state == "ERROR")
    | [$n, $b, (.context + ": " + (.description // "")), (.targetUrl // "")]
  end | @tsv'

main() {
	local branches red
	branches=$(git -C "$clone" for-each-ref --format='%(refname:short)' refs/heads 2>/dev/null) || return 0
	[ -n "$branches" ] || return 0
	red=$(gh pr list -R "$repo" --state open --limit 100 \
		--json number,headRefName,isCrossRepository,statusCheckRollup --jq "$red_jq" 2>/dev/null) || return 0
	[ -n "$red" ] || return 0

	local lines=() number branch what url
	while IFS=$'\t' read -r number branch what url; do
		grep -qxF -- "$branch" <<<"$branches" || continue
		lines+=("$(printf -- '- #%s (%s): %s %s' "$number" "$branch" "$what" "$url")")
		annotations "$url"
	done < <(sort -t$'\t' -k1,1n -s <<<"$red")
	[ ${#lines[@]} -gt 0 ] || return 0

	printf 'CI is red on your open PRs (scripts/pr-watch.sh):\n'
	printf '%s\n' "${lines[@]}"
}

# annotations URL appends the first failure annotations of each failed job
# the link names, as indented lines of lines: a check run's link names its
# job, and the queue's status names its run, whose failed jobs are looked up.
annotations() {
	local url=$1 jobs job
	case $url in
	*/actions/runs/*/job/*) jobs=${url##*/job/} ;;
	*/actions/runs/*)
		jobs=$(gh api "repos/$repo/actions/runs/${url##*/actions/runs/}/jobs" \
			--jq '.jobs[] | select(.conclusion == "failure") | .id' 2>/dev/null) || return 0
		;;
	*) return 0 ;;
	esac
	for job in $jobs; do
		[[ $job =~ ^[0-9]+$ ]] || continue
		while IFS= read -r note; do
			[ -n "$note" ] && lines+=("    ${note:0:300}")
		done < <(gh api "repos/$repo/check-runs/$job/annotations" \
			--jq '.[] | select(.annotation_level == "failure") | .message | gsub("\\s+"; " ")
			  | select(startswith("Process completed with exit code") | not)' 2>/dev/null | head -n "$notes")
	done
}

main
exit 0
