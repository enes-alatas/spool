#!/usr/bin/env bash
# Exercises scripts/queue.sh and scripts/queue-tree.sh (#819).
#
# The queue's workflow fires on main's pushes and on label events, which no
# PR's CI runs, so these are what a PR can see of it: the two writing steps
# against a stub `gh` and fixture API answers, and the tree step against a
# real git repository standing in for origin.
set -uo pipefail

cd "$(dirname "$0")/.."
root=$(pwd)
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

# The stub answers from files under $FIX and logs every write to $CALLS:
#   main            main's sha
#   pulls.json      the open PRs
#   pull-N.json     one PR
#   status-SHA.json a commit's combined status
#   issue.json      the noticeboard issue (a PATCH of it lands in patched)
# A `--jq` filter is applied with the real jq, as gh would.
mkdir -p "$work/bin"
cat > "$work/bin/gh" <<'STUB'
#!/usr/bin/env bash
shift # api
filter=. method=GET path="" fields=()
while [ $# -gt 0 ]; do
  case $1 in
    --jq) filter=$2; shift ;;
    --method) method=$2; shift ;;
    -f) fields+=("$2"); shift ;;
    *) path=$1 ;;
  esac
  shift
done
if [ "$method" = PATCH ]; then
  # an issue body is many lines: kept whole, and logged as one
  printf '%s' "${fields[0]#body=}" > "$FIX/patched"
  printf 'PATCH %s\n' "${path##*/}" >> "$CALLS"
  exit 0
fi
if [ "$method" != GET ]; then
  printf '%s %s %s\n' "$method" "${path##*/}" "${fields[*]}" >> "$CALLS"
  # delete-fails: the DELETE answers 404, as when the label is already off
  if [ "$method" = DELETE ] && [ -e "$FIX/delete-fails" ]; then
    echo 'gh: Label does not exist (HTTP 404)' >&2
    exit 1
  fi
  exit 0
fi
# labels-fails: the labels read answers 5xx (here, not in the case below,
# whose exit the pipe into jq would swallow)
if [[ $path == */issues/*/labels ]] && [ -e "$FIX/labels-fails" ]; then
  echo 'gh: Server Error (HTTP 502)' >&2
  exit 1
fi
case $path in
  */issues/*/labels) cat "$FIX/labels.json" 2>/dev/null || echo '[]' ;;
  */commits/main) printf '{"sha":"%s"}' "$(cat "$FIX/main")" ;;
  */commits/*/status) cat "$FIX/status-$(basename "$(dirname "$path")").json" 2>/dev/null || echo '{"statuses":[]}' ;;
  */pulls\?*) cat "$FIX/pulls.json" ;;
  */issues/*) cat "$FIX/issue.json" ;;
  */pulls/*) cat "$FIX/pull-${path##*/}.json" ;;
  *) echo "stub gh: no fixture for $path" >&2; exit 1 ;;
esac | jq -r "$filter"
STUB
chmod +x "$work/bin/gh"
export PATH="$work/bin:$PATH" GITHUB_REPOSITORY=owner/spool

# pr NUMBER SHA LABELS REPO AUTO: one open PR, as the API lists it, set to
# auto-merge unless AUTO is "off".
pr() {
  jq -nc --argjson n "$1" --arg sha "$2" --arg labels "$3" --arg repo "${4:-owner/spool}" --arg auto "${5:-on}" \
    '{number: $n, state: "open", node_id: "PR_\($n)", head: {sha: $sha, repo: {full_name: $repo}},
      auto_merge: (if $auto == "on" then {merge_method: "rebase"} else null end),
      labels: ($labels | split(",") | map(select(. != "")) | map({name: .}))}'
}
# has SHA STATE DESCRIPTION [URL]: SHA already carries that queue status.
has() {
  jq -nc --arg s "$2" --arg d "$3" --arg u "${4:-}" '{statuses: [{context: "queue", state: $s, description: $d, target_url: (if $u == "" then null else $u end)}]}' > "$FIX/status-$1.json"
}
fresh() {
  export FIX="$work/fix-$1" CALLS="$work/calls-$1"
  mkdir -p "$FIX"
  : > "$CALLS"
  echo m0000000000aaaa > "$FIX/main"
}
expect() { # name file pattern: the file holds a line matching pattern
  if grep -qE "$3" "$2"; then pass "$1"; else fail "$1"; printf '  in %s:\n' "$2"; sed 's/^/    /' "$2"; fi
}
refuse() { # name file pattern: no line matches
  if grep -qE "$3" "$2"; then fail "$1"; sed 's/^/    /' "$2"; else pass "$1"; fi
}

# 1. The oldest queued PR is the head and is tested; the next waits behind
# it; an unqueued PR is marked pending; a fork's PR is left alone.
fresh order
jq -s . <(pr 7 s7 queue) <(pr 5 s5 queue,docs) <(pr 6 s6 "") <(pr 4 s4 queue other/fork) > "$FIX/pulls.json"
bash scripts/queue.sh pick > "$work/out-order"
expect 'the oldest queued PR is the one tested' "$work/out-order" '^pr=5$'
expect 'its head is the sha tested' "$work/out-order" '^head=s5$'
expect 'on the main read at the start' "$work/out-order" '^base=m0000000000aaaa$'
expect 'the head is marked testing' "$CALLS" '^POST s5 state=pending context=queue description=testing on main@m000000$'
expect 'the next waits behind it' "$CALLS" '^POST s7 state=pending context=queue description=queued at 2, behind #5$'
expect 'an unqueued PR is marked not queued' "$CALLS" '^POST s6 state=pending .*not queued'
refuse 'a fork PR is never touched' "$CALLS" '^POST s4 '

# 2. A head already green on this main is left green and not tested again.
fresh green
jq -s . <(pr 5 s5 queue) <(pr 7 s7 queue) > "$FIX/pulls.json"
has s5 success 'tested on main@m000000'
bash scripts/queue.sh pick > "$work/out-green"
if [ -s "$work/out-green" ]; then fail 'a head green on this main is not tested again'; else pass 'a head green on this main is not tested again'; fi
refuse 'and its green stays' "$CALLS" '^POST s5 '
expect 'the next still waits behind it' "$CALLS" '^POST s7 .*behind #5$'

# 3. A head green on an older main is tested again.
fresh stale
jq -s . <(pr 5 s5 queue) > "$FIX/pulls.json"
has s5 success 'tested on main@0ld0000'
bash scripts/queue.sh pick > "$work/out-stale"
expect 'a head green on an older main is tested again' "$work/out-stale" '^pr=5$'
expect 'and goes back to pending' "$CALLS" '^POST s5 state=pending .*testing on main@m000000$'

# 4. A red head stays red, and the queue moves past it.
fresh red
jq -s . <(pr 5 s5 queue) <(pr 7 s7 queue) > "$FIX/pulls.json"
has s5 failure 'failed on main@m000000: see the run'
bash scripts/queue.sh pick > "$work/out-red"
expect 'the next PR becomes the head' "$work/out-red" '^pr=7$'
refuse 'the red one is left red' "$CALLS" '^POST s5 '

# 5. A green PR taken out of the queue does not keep its green.
fresh out
jq -s . <(pr 5 s5 "") > "$FIX/pulls.json"
has s5 success 'tested on main@m000000'
bash scripts/queue.sh pick > /dev/null
expect 'a PR out of the queue goes back to pending' "$CALLS" '^POST s5 state=pending .*not queued'

# 6. A status already reading the same is not posted again.
fresh same
jq -s . <(pr 5 s5 queue) <(pr 7 s7 queue) > "$FIX/pulls.json"
has s7 pending 'queued at 2, behind #5'
bash scripts/queue.sh pick > /dev/null
refuse 'an unchanged status is not posted again' "$CALLS" '^POST s7 '

# 7. An empty queue tests nothing.
fresh empty
echo '[]' > "$FIX/pulls.json"
bash scripts/queue.sh pick > "$work/out-empty"
if [ -s "$work/out-empty" ] || [ -s "$CALLS" ]; then fail 'an empty queue tests and posts nothing'; else pass 'an empty queue tests and posts nothing'; fi

# 8. A labelled PR the operator has not set to auto-merge is not queued: the
# head is always a PR that merges on green (review of #822).
fresh noauto
jq -s . <(pr 5 s5 queue owner/spool off) <(pr 7 s7 queue) > "$FIX/pulls.json"
bash scripts/queue.sh pick > "$work/out-noauto"
expect 'a PR without auto-merge is not the head' "$work/out-noauto" '^pr=7$'
expect 'and says what it waits for' "$CALLS" '^POST s5 state=pending .*waiting for auto-merge$'
refuse 'nothing waits behind it' "$CALLS" 'behind #5'

# 9. A lower number queued later does not take the head from a PR that is
# green, or under test, on this main.
for held in 'success tested on main@m000000' 'pending testing on main@m000000'; do
  fresh "held-${held%% *}"
  jq -s . <(pr 3 s3 queue) <(pr 5 s5 queue) > "$FIX/pulls.json"
  has s5 "${held%% *}" "${held#* }"
  bash scripts/queue.sh pick > "$work/out-held"
  refuse "a head ${held%% *} on this main keeps the head" "$work/out-held" '^pr=3$'
  expect "and the newcomer waits behind it (${held%% *})" "$CALLS" '^POST s3 state=pending .*queued at 2, behind #5$'
done

# The report: green or red only when nothing moved under the test.
report() { # name result: runs the report for #5 at s5 on m0000000000aaaa
  PR=5 HEAD=s5 BASE=m0000000000aaaa RESULT=$2 bash scripts/queue.sh report > "$work/report-$1" 2>&1
}
fresh pass
pr 5 s5 queue > "$FIX/pull-5.json"
report pass success
expect 'a passing test turns the PR green' "$CALLS" '^POST s5 state=success context=queue description=tested on main@m000000$'
fresh failed
pr 5 s5 queue > "$FIX/pull-5.json"
report failed failure
expect 'a failing test turns it red' "$CALLS" '^POST s5 state=failure '
fresh moved
pr 5 s5 queue > "$FIX/pull-5.json"
echo m1111111111bbbb > "$FIX/main"
report moved success
refuse 'a pass on a main that has since moved is not posted' "$CALLS" 'POST'
fresh pushed
pr 5 s5b queue > "$FIX/pull-5.json"
report pushed success
refuse 'a pass on a head that has since moved is not posted' "$CALLS" 'POST'
fresh unqueued
pr 5 s5 "" > "$FIX/pull-5.json"
report unqueued success
refuse 'a pass on a PR taken out of the queue is not posted' "$CALLS" 'POST'
fresh cancelled
pr 5 s5 queue > "$FIX/pull-5.json"
report cancelled cancelled
refuse 'a cancelled test posts nothing' "$CALLS" 'POST'
fresh noauto-report
pr 5 s5 queue owner/spool off > "$FIX/pull-5.json"
report noauto-report success
refuse 'a pass on a PR whose auto-merge was turned off is not posted' "$CALLS" 'POST'

# The noticeboard (#834): the order, read from the statuses, numbers only.
board() { BOARD=833 RUN_URL=https://run/9 bash scripts/queue.sh render > "$work/board-$1" 2>&1; }
fresh board
jq -s . <(pr 5 s5 queue) <(pr 6 s6 queue) <(pr 7 s7 queue) <(pr 8 s8 queue owner/spool off) <(pr 9 s9 queue) <(pr 10 s10 "") <(pr 11 s11 queue other/fork) > "$FIX/pulls.json"
has s5 pending 'testing on main@abc1234' https://github.com/owner/spool/actions/runs/1
has s6 pending 'queued at 3, behind #5'
has s7 pending 'queued at 2, behind #5'
has s8 pending 'not queued: waiting for auto-merge'
has s9 failure 'failed on main@abc1234: see [this](https://evil) @someone'
has s10 pending 'not queued: add the queue label to merge'
has s11 pending 'testing on main@m000000'
jq -n '{body: "placeholder"}' > "$FIX/issue.json"
board order
expect 'the board is rewritten' "$CALLS" '^PATCH 833$'
expect 'it names the PR under test, with its run' "$FIX/patched" '^- #5 on main@abc1234 \(\[run\]\(https://github.com/owner/spool/actions/runs/1\)\)$'
expect 'the queue in order' "$FIX/patched" '^2\. #7$'
expect 'the queue in order, the next' "$FIX/patched" '^3\. #6$'
if [ "$(grep -n '^2\. #7$' "$FIX/patched" | cut -d: -f1)" -lt "$(grep -n '^3\. #6$' "$FIX/patched" | cut -d: -f1)" ]; then pass 'second before third'; else fail 'second before third'; fi
expect 'a PR waiting for auto-merge' "$FIX/patched" '^- #8$'
expect 'a red PR, with where it failed' "$FIX/patched" '^- #9: failed on main@abc1234$'
refuse 'no text of a status beyond what the queue writes' "$FIX/patched" 'evil|@someone'
refuse 'an unqueued PR is not listed' "$FIX/patched" '#10'
refuse 'a fork PR is not listed' "$FIX/patched" '#11'
expect 'it says when and by which run' "$FIX/patched" '^Updated .* UTC by \[this run\]\(https://run/9\)\.$'
cp "$FIX/patched" "$work/board-body"
: > "$CALLS"
jq -n --rawfile b "$work/board-body" '{body: ($b | sub("Updated [^\n]*$"; "Updated 2000-01-01 00:00 UTC."))}' > "$FIX/issue.json"
board same
refuse 'the same order, read later, is not written again' "$CALLS" PATCH
jq -n --rawfile b "$work/board-body" '{body: ($b | gsub("\n"; "\r\n"))}' > "$FIX/issue.json"
board crlf
refuse 'nor when GitHub stored it with CRLF' "$CALLS" PATCH
fresh board-empty
echo '[]' > "$FIX/pulls.json"
jq -n '{body: "old"}' > "$FIX/issue.json"
board empty
expect 'an empty queue says so' "$FIX/patched" '^Nothing is queued\.$'
fresh board-green
jq -s . <(pr 5 s5 queue) > "$FIX/pulls.json"
has s5 success 'tested on main@abc1234'
jq -n '{body: "old"}' > "$FIX/issue.json"
board green
expect 'a green PR is shown landing' "$FIX/patched" '^- #5, green on main@abc1234$'
refuse 'and not as nothing queued' "$FIX/patched" 'Nothing is queued'

fresh board-hostile
jq -s . <(pr 5 s5 queue) <(pr 6 s6 queue) > "$FIX/pulls.json"
has s5 pending 'testing on main@zzzzzzz' 'https://evil.example/actions/runs/1'
has s6 pending 'queued at 2) [x](https://evil), behind #5'
jq -n '{body: "old"}' > "$FIX/issue.json"
board hostile
expect 'a sha that is not one shows as ?' "$FIX/patched" '^- #5 on main@\?$'
refuse 'a run link off this repository is dropped' "$FIX/patched" 'evil'
refuse 'a place that is not a number is dropped' "$FIX/patched" '#6'

# The unqueue: a push to a queued PR takes it out, label and auto-merge.
unqueue() { PR=5 bash scripts/queue.sh unqueue > "$work/unqueue-$1" 2>&1; }
fresh pushed-queued
pr 5 s5b queue > "$FIX/pull-5.json"
unqueue pushed-queued
expect 'a push takes the label off' "$CALLS" '^DELETE queue $'
expect 'and turns auto-merge off' "$CALLS" '^POST graphql id=PR_5 query=mutation.*disablePullRequestAutoMerge'
expect 'and marks the new head not queued' "$CALLS" '^POST s5b state=pending .*pushed after queueing'
expect 'and asks for a queue run' "$work/unqueue-pushed-queued" '^unqueued=true$'
if [ "$(sed -n 1p "$CALLS" | cut -d' ' -f1)" = DELETE ]; then pass 'the label goes before auto-merge'; else fail 'the label goes before auto-merge'; fi
fresh pushed-plain
pr 5 s5b "" owner/spool off > "$FIX/pull-5.json"
unqueue pushed-plain
refuse 'a push to an unqueued PR removes nothing' "$CALLS" '^(DELETE|POST graphql)'
refuse 'and asks for no queue run' "$work/unqueue-pushed-plain" unqueued
expect 'and marks it not queued' "$CALLS" '^POST s5b state=pending .*add the queue label'
fresh pushed-auto
pr 5 s5b "" > "$FIX/pull-5.json"
unqueue pushed-auto
refuse 'no label, no label removal' "$CALLS" '^DELETE'
expect 'auto-merge alone is still turned off' "$CALLS" '^POST graphql '
# The label is taken off by someone else between the read and the delete
# (#1155): the 404 is no failure, and the rest of the unqueue goes on.
fresh pushed-raced
pr 5 s5b queue > "$FIX/pull-5.json"
touch "$FIX/delete-fails"
if unqueue pushed-raced; then pass 'a label already off is no failure'; else fail 'a label already off is no failure'; fi
expect 'and auto-merge is still turned off' "$CALLS" '^POST graphql '
expect 'and the head is still marked not queued' "$CALLS" '^POST s5b state=pending .*pushed after queueing'
expect 'and a queue run is still asked for' "$work/unqueue-pushed-raced" '^unqueued=true$'
fresh pushed-stuck
pr 5 s5b queue > "$FIX/pull-5.json"
touch "$FIX/delete-fails"
echo '[{"name":"queue"}]' > "$FIX/labels.json"
if unqueue pushed-stuck; then fail 'a label the delete left on fails the unqueue'; else pass 'a label the delete left on fails the unqueue'; fi
refuse 'and marks nothing' "$CALLS" '^POST s5b'
fresh pushed-unread
pr 5 s5b queue > "$FIX/pull-5.json"
touch "$FIX/delete-fails" "$FIX/labels-fails"
if unqueue pushed-unread; then fail 'a label that cannot be read back fails the unqueue'; else pass 'a label that cannot be read back fails the unqueue'; fi
refuse 'and marks nothing either' "$CALLS" '^POST s5b'

# The tree: a real origin with main and a PR ref, cloned the way
# actions/checkout leaves a job.
git_() { git -c user.name=t -c user.email=t@localhost -c init.defaultBranch=main "$@"; }
origin="$work/origin"
git_ init -q "$origin"
(
  cd "$origin"
  printf 'a\nb\nc\n' > f
  git_ add f && git_ commit -qm base
  git_ checkout -qb pr
  printf 'a\nb\nC\n' > f && git_ commit -qam 'the pr'
  git update-ref refs/pull/1/head HEAD
  git_ checkout -qb clash main
  printf 'A\nb\nc\n' > f && git_ commit -qam 'main moves, apart'
  git_ checkout -q main
  git_ merge -q --ff-only clash
  git_ checkout -qb clash2 main~1
  printf 'a\nb\nX\n' > f && git_ commit -qam 'main moves, onto the same line'
  git update-ref refs/heads/conflict HEAD
)
head=$(git -C "$origin" rev-parse refs/pull/1/head)
base=$(git -C "$origin" rev-parse main)
conflict=$(git -C "$origin" rev-parse conflict)
clone="$work/clone"
git clone -q --depth 1 "file://$origin" "$clone"

if (cd "$clone" && bash "$root/scripts/queue-tree.sh" 1 "$head" "$base") > "$work/tree" 2>&1 &&
  [ "$(cat "$clone/f")" = $'A\nb\nC' ]; then
  pass 'the queued PR is tested rebased onto main'
else
  fail 'the queued PR is tested rebased onto main'; sed 's/^/    /' "$work/tree"
fi
if (cd "$clone" && bash "$root/scripts/queue-tree.sh" 1 "$base" "$base") > "$work/tree-moved" 2>&1; then
  fail 'a PR whose head moved since the pick is refused'
else
  pass 'a PR whose head moved since the pick is refused'
fi
git -C "$origin" update-ref refs/heads/main "$conflict"
if (cd "$clone" && bash "$root/scripts/queue-tree.sh" 1 "$head" "$conflict") > "$work/tree-conflict" 2>&1; then
  fail 'a PR that does not rebase cleanly fails'
else
  expect 'a PR that does not rebase cleanly fails, saying so' "$work/tree-conflict" 'does not rebase cleanly'
fi

if [ "$failures" -gt 0 ]; then
  printf '\n%s check(s) failed\n' "$failures"
  exit 1
fi
printf '\nall checks passed\n'
