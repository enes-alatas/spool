#!/usr/bin/env bash
# pr-watch.sh [CLONE] prints what needs the loop on its open PRs, for a
# Claude Code UserPromptSubmit hook to put in front of its next turn (#904).
#
# A loop's PRs are the open ones whose head branch exists in its clone
# (CLONE, or the current directory), from this repo and never a fork: a
# fork's PR from its own `main` would match every clone, and its checks'
# names and annotations are a stranger's text in the loop's turn. Red is a
# failed check run, and a
# failed or errored status, the merge queue's `queue` among them. Each failed
# job comes with its first failure annotations, which CI's test steps write
# (scripts/test-failures.sh), so the loop reads which test broke without
# opening the run. A PR that conflicts with main is listed too (#1374): it
# runs only CodeQL until it is rebased, so its checks look sparse, not red.
#
# With PR_WATCH_MERGE_ASK=1, a reviewer's hook also lists every PR, the
# loop's or not, that has carried the `queue` label for over
# PR_WATCH_ASK_AFTER seconds (default 7200) with auto-merge still off: the
# merge ask is overdue, and one message with every link nudges for all of
# them. Each PR is listed once per PR_WATCH_ASK_EVERY seconds (default
# 21600), remembered in PR_WATCH_STATE (default
# ${XDG_STATE_HOME:-~/.local/state}/pr-watch), so a nudge sent is not asked
# for again at every turn.
#
# It prints nothing when nothing needs the loop, and exits 0 whatever
# happens: a UserPromptSubmit hook that exits 2 blocks the turn, and a GitHub
# that is down must not stop a loop from answering a person. It reads and
# writes nothing on GitHub beyond those reads. It adds no wake: it runs only
# inside a turn the loop is already taking.
#
# Hook it up in the loop's own ~/.claude/settings.json (CONVENTIONS.md,
# "Seeing your PRs' CI"), naming its clone and the variable its GitHub token
# is in. The hook runs the script as origin/main has it, never the checked-out
# branch's copy, so a PR under review can't change what runs at every turn:
#
#   {"hooks": {"UserPromptSubmit": [{"hooks": [{"type": "command", "timeout": 30,
#     "command": "GH_TOKEN=\"$GH_TOKEN_SPOOL\" bash -c \"$(git -C \"$HOME/spool\" show origin/main:scripts/pr-watch.sh)\" pr-watch \"$HOME/spool\""}]}]}}
#
# A reviewer puts PR_WATCH_MERGE_ASK=1 before `bash` in that command.
#
# PR_WATCH_REPO is the repo (default enes-alatas/spool); PR_WATCH_NOTES the
# annotations shown per failed job (default 3); PR_WATCH_NOW the time, in
# Unix seconds, for tests.
set -uo pipefail

clone=${1:-.}
repo=${PR_WATCH_REPO:-enes-alatas/spool}
notes=${PR_WATCH_NOTES:-3}
ask_after=${PR_WATCH_ASK_AFTER:-7200}
ask_every=${PR_WATCH_ASK_EVERY:-21600}
state=${PR_WATCH_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/pr-watch}

# One line per red check or status of an open PR, and one per PR that
# conflicts with main, as tab-separated fields: number, branch, what needs
# the loop, its link.
red_jq='.[] | select(.isCrossRepository | not) | .number as $n | .headRefName as $b |
  (if .mergeable == "CONFLICTING" then
    [$n, $b, "conflicts with main: rebase it (only CodeQL runs until then)", (.url // "")] | @tsv
  else empty end),
  ((.statusCheckRollup // [])[] |
  if .__typename == "CheckRun" then
    select(.conclusion == "FAILURE" or .conclusion == "TIMED_OUT" or .conclusion == "STARTUP_FAILURE")
    | [$n, $b, ((if (.workflowName // "") != "" then .workflowName + " / " else "" end) + .name), (.detailsUrl // "")]
  else
    select(.state == "FAILURE" or .state == "ERROR")
    | [$n, $b, (.context + ": " + (.description // "")), (.targetUrl // "")]
  end | @tsv)'

# One line per queued PR with auto-merge off: number, link.
queued_jq='.[] | select(.isCrossRepository | not)
  | select(any(.labels[]?; .name == "queue")) | select(.autoMergeRequest == null)
  | [.number, (.url // "")] | @tsv'

main() {
	local branches red out=()
	branches=$(git -C "$clone" for-each-ref --format='%(refname:short)' refs/heads 2>/dev/null) || return 0
	[ -n "$branches" ] || return 0
	red=$(gh pr list -R "$repo" --state open --limit 100 \
		--json number,headRefName,isCrossRepository,statusCheckRollup,mergeable,url --jq "$red_jq" 2>/dev/null) || red=

	local lines=() number branch what url
	if [ -n "$red" ]; then
		while IFS=$'\t' read -r number branch what url; do
			grep -qxF -- "$branch" <<<"$branches" || continue
			lines+=("$(printf -- '- #%s (%s): %s %s' "$number" "$branch" "$what" "$url")")
			annotations "$url"
		done < <(sort -t$'\t' -k1,1n -s <<<"$red")
	fi
	if [ ${#lines[@]} -gt 0 ]; then
		out+=('Your open PRs need you (scripts/pr-watch.sh):' "${lines[@]}")
	fi

	[ "${PR_WATCH_MERGE_ASK:-}" = 1 ] && merge_asks
	[ ${#out[@]} -gt 0 ] || return 0
	printf '%s\n' "${out[@]}"
}

# merge_asks appends the queued PRs whose merge ask is overdue to out.
merge_asks() {
	local queued now number url since asks=()
	queued=$(gh pr list -R "$repo" --state open --limit 100 \
		--json number,isCrossRepository,labels,autoMergeRequest,url --jq "$queued_jq" 2>/dev/null) || return 0
	[ -n "$queued" ] || return 0
	now=${PR_WATCH_NOW:-$(date +%s)}
	mkdir -p "$state" 2>/dev/null || return 0
	while IFS=$'\t' read -r number url; do
		[[ $number =~ ^[0-9]+$ ]] || continue
		# When the label last went on: a PR taken out and queued again
		# waits from its latest queueing.
		since=$(gh api "repos/$repo/issues/$number/events" --paginate \
			--jq '.[] | select(.event == "labeled" and .label.name == "queue") | .created_at' 2>/dev/null | tail -n 1)
		[ -n "$since" ] || continue
		since=$(date -u -d "$since" +%s 2>/dev/null) || continue
		((now - since > ask_after)) || continue
		local seen=0
		[ -f "$state/asked-$number" ] && seen=$(cat "$state/asked-$number" 2>/dev/null)
		[[ $seen =~ ^[0-9]+$ ]] || seen=0
		((now - seen >= ask_every)) || continue
		printf '%s\n' "$now" >"$state/asked-$number" 2>/dev/null
		asks+=("$(printf -- '- #%s queued %sh ago, auto-merge off %s' "$number" $(((now - since) / 3600)) "$url")")
	done < <(sort -t$'\t' -k1,1n <<<"$queued")
	[ ${#asks[@]} -gt 0 ] || return 0
	out+=('Merge asks overdue (scripts/pr-watch.sh): send the operator one message with every link:' "${asks[@]}")
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
