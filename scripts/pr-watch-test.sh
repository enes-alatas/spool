#!/usr/bin/env bash
# Exercises scripts/pr-watch.sh against the shapes GitHub answers with (#904).
#
# `gh` is stubbed: it applies the script's own --jq to the fixtures in
# testdata/pr-watch with jq, so the filters are tested as written, and
# nothing here reaches GitHub. The clone is a scratch repo whose branches
# say which PRs are the loop's.
set -uo pipefail

cd "$(dirname "$0")/.."
data=$PWD/scripts/testdata/pr-watch
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

mkdir -p "$work/bin"
cat >"$work/bin/gh" <<'STUB'
#!/usr/bin/env bash
[ -z "${STUB_FAIL:-}" ] || { echo "gh: HTTP 502" >&2; exit 1; }
expr= file=
for ((i = 1; i <= $#; i++)); do
	j=$((i + 1))
	[ "${!i}" != --jq ] || expr=${!j}
done
case "$1 $2" in
"pr list") file=$STUB_DATA/prs.json ;;
api*)
	case $2 in
	*/actions/runs/*/jobs) run=${2%/jobs}; file=$STUB_DATA/jobs-${run##*/}.json ;;
	*/check-runs/*/annotations) job=${2%/annotations}; file=$STUB_DATA/annotations-${job##*/}.json ;;
	*/issues/*/events) pr=${2%/events}; file=$STUB_DATA/events-${pr##*/}.json ;;
	esac
	;;
esac
[ -f "$file" ] || { jq -rn "[] | $expr"; exit 0; }
jq -r "$expr" "$file"
STUB
chmod +x "$work/bin/gh"
export PATH="$work/bin:$PATH" STUB_DATA="$data" PR_WATCH_REPO=o/r

# Every clone has a main, so the fork PR in the fixtures (#909), whose
# head is its own main, would match it by name alone.
clone=$work/clone
git init -q -b main "$clone"
git -C "$clone" -c user.name=t -c user.email=t@t commit -q --allow-empty -m init
for branch in feat/green fix/queued feat/red chore/no-checks; do
	git -C "$clone" branch "$branch"
done

# name, want (the exact output); the remaining arguments run the script.
check() {
	local name=$1 want=$2 got status
	shift 2
	got=$("$@" 2>&1)
	status=$?
	if [[ $got == "$want" && $status -eq 0 ]]; then
		pass "$name"
	else
		fail "$name"
		printf '  want: %s\n  got:  %s (exit %s)\n' "${want//$'\n'/$'\n        '}" "${got//$'\n'/$'\n        '}" "$status"
	fi
}

check "red checks, the queue's status and a conflict with main are listed, lowest number first, and no fork's or other loop's" \
	"Your open PRs need you (scripts/pr-watch.sh):
- #903 (chore/no-checks): conflicts with main: rebase it (only CodeQL runs until then) https://github.com/o/r/pull/903
- #907 (feat/red): ci / itest https://github.com/o/r/actions/runs/5/job/51
    --- FAIL: TestOne (0.10s) one_test.go:9: a message over two lines
    --- FAIL: TestTwo (0.10s)
    --- FAIL: TestThree (0.10s)
- #907 (feat/red): ci / lint https://github.com/o/r/actions/runs/5/job/52
- #910 (fix/queued): queue: failed on main@c4af91e: see the run https://github.com/o/r/actions/runs/4
    --- FAIL: TestStream (21.31s) roles_test.go:214: the stream never carried \"x\"" \
	bash scripts/pr-watch.sh "$clone"

check "PR_WATCH_NOTES bounds the failures shown per job" \
	"Your open PRs need you (scripts/pr-watch.sh):
- #903 (chore/no-checks): conflicts with main: rebase it (only CodeQL runs until then) https://github.com/o/r/pull/903
- #907 (feat/red): ci / itest https://github.com/o/r/actions/runs/5/job/51
    --- FAIL: TestOne (0.10s) one_test.go:9: a message over two lines
- #907 (feat/red): ci / lint https://github.com/o/r/actions/runs/5/job/52
- #910 (fix/queued): queue: failed on main@c4af91e: see the run https://github.com/o/r/actions/runs/4
    --- FAIL: TestStream (21.31s) roles_test.go:214: the stream never carried \"x\"" \
	env PR_WATCH_NOTES=1 bash scripts/pr-watch.sh "$clone"

git -C "$clone" branch -q -D feat/red fix/queued chore/no-checks
check "a clone whose PRs are all green and mergeable prints nothing" "" bash scripts/pr-watch.sh "$clone"

# Merge asks (#1374), at 2026-11-06T23:00Z. #912 was queued again at 20:00
# (3h), #905 at 22:30 (30m); #910 has auto-merge on; #909 is a fork's.
export PR_WATCH_NOW=1794006000 PR_WATCH_STATE=$work/state
check "without PR_WATCH_MERGE_ASK no merge ask is listed" "" bash scripts/pr-watch.sh "$clone"

check "a queued PR past 2h with auto-merge off is listed for the reviewer, from its latest queueing" \
	"Merge asks overdue (scripts/pr-watch.sh): send the operator one message with every link:
- #912 queued 3h ago, auto-merge off https://github.com/o/r/pull/912" \
	env PR_WATCH_MERGE_ASK=1 bash scripts/pr-watch.sh "$clone"

check "a merge ask already listed is not listed again within PR_WATCH_ASK_EVERY" "" \
	env PR_WATCH_MERGE_ASK=1 bash scripts/pr-watch.sh "$clone"

check "it is listed again once PR_WATCH_ASK_EVERY has passed" \
	"Merge asks overdue (scripts/pr-watch.sh): send the operator one message with every link:
- #905 queued 6h ago, auto-merge off https://github.com/o/r/pull/905
- #912 queued 9h ago, auto-merge off https://github.com/o/r/pull/912" \
	env PR_WATCH_MERGE_ASK=1 PR_WATCH_NOW=$((PR_WATCH_NOW + 21600)) bash scripts/pr-watch.sh "$clone"

git -C "$clone" branch -q chore/no-checks
rm -rf "$work/state"
check "the loop's own section comes first, then the merge asks" \
	"Your open PRs need you (scripts/pr-watch.sh):
- #903 (chore/no-checks): conflicts with main: rebase it (only CodeQL runs until then) https://github.com/o/r/pull/903
Merge asks overdue (scripts/pr-watch.sh): send the operator one message with every link:
- #912 queued 3h ago, auto-merge off https://github.com/o/r/pull/912" \
	env PR_WATCH_MERGE_ASK=1 bash scripts/pr-watch.sh "$clone"

check "a state directory it cannot write skips the merge asks and exits 0" "" \
	env PR_WATCH_MERGE_ASK=1 PR_WATCH_STATE=/dev/null/x bash -c 'git -C "$1" branch -q -D chore/no-checks; bash scripts/pr-watch.sh "$1"' _ "$clone"
unset PR_WATCH_NOW PR_WATCH_STATE

git -C "$clone" branch -q feat/red
check "a GitHub that fails prints nothing and exits 0" "" env STUB_FAIL=1 PR_WATCH_MERGE_ASK=1 bash scripts/pr-watch.sh "$clone"
check "a directory that is no clone prints nothing and exits 0" "" bash scripts/pr-watch.sh "$work/bin"

if [ "$failures" -gt 0 ]; then
	printf '%d failed\n' "$failures"
	exit 1
fi
