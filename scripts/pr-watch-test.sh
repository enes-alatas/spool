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

check "red checks and the queue's status are listed with their failures, lowest number first, and no fork's" \
	"CI is red on your open PRs (scripts/pr-watch.sh):
- #907 (feat/red): ci / itest https://github.com/o/r/actions/runs/5/job/51
    --- FAIL: TestOne (0.10s) one_test.go:9: a message over two lines
    --- FAIL: TestTwo (0.10s)
    --- FAIL: TestThree (0.10s)
- #907 (feat/red): ci / lint https://github.com/o/r/actions/runs/5/job/52
- #910 (fix/queued): queue: failed on main@c4af91e: see the run https://github.com/o/r/actions/runs/4
    --- FAIL: TestStream (21.31s) roles_test.go:214: the stream never carried \"x\"" \
	bash scripts/pr-watch.sh "$clone"

check "PR_WATCH_NOTES bounds the failures shown per job" \
	"CI is red on your open PRs (scripts/pr-watch.sh):
- #907 (feat/red): ci / itest https://github.com/o/r/actions/runs/5/job/51
    --- FAIL: TestOne (0.10s) one_test.go:9: a message over two lines
- #907 (feat/red): ci / lint https://github.com/o/r/actions/runs/5/job/52
- #910 (fix/queued): queue: failed on main@c4af91e: see the run https://github.com/o/r/actions/runs/4
    --- FAIL: TestStream (21.31s) roles_test.go:214: the stream never carried \"x\"" \
	env PR_WATCH_NOTES=1 bash scripts/pr-watch.sh "$clone"

git -C "$clone" branch -q -D feat/red fix/queued
check "a clone whose PRs are all green prints nothing" "" bash scripts/pr-watch.sh "$clone"

git -C "$clone" branch -q feat/red
check "a GitHub that fails prints nothing and exits 0" "" env STUB_FAIL=1 bash scripts/pr-watch.sh "$clone"
check "a directory that is no clone prints nothing and exits 0" "" bash scripts/pr-watch.sh "$work/bin"

if [ "$failures" -gt 0 ]; then
	printf '%d failed\n' "$failures"
	exit 1
fi
