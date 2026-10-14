#!/usr/bin/env bash
# Exercises scripts/test-failures.sh against go test output (#839).
#
# The fixtures are the shapes a red Go step prints: a failed test and its
# message, a failed subtest under its parent, a timeout, a build failure,
# more failures than a step may annotate, and a line written to end its
# annotation and start a command of its own.
set -uo pipefail

cd "$(dirname "$0")/.."
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

# name, want (the exact output); the log on stdin.
check() {
	local name=$1 want=$2 got
	cat >"$work/log"
	got=$(MAX=${MAX:-10} bash scripts/test-failures.sh "$work/log" "tier 2" 2>&1)
	if [[ $got == "$want" ]]; then
		pass "$name"
	else
		fail "$name"
		printf '  want: %s\n  got:  %s\n' "${want//$'\n'/$'\n        '}" "${got//$'\n'/$'\n        '}"
	fi
}

check "a green run annotates nothing" "" <<'LOG'
ok  	github.com/enes-alatas/spool/itest	339.385s
ok  	github.com/enes-alatas/spool/internal/runtime/docker	0.036s
LOG

check "a failed test is named with its first message" \
	"::error title=tier 2::--- FAIL: TestQueue (3.02s) queue_test.go:41: want 2 envelopes, got 1" <<'LOG'
--- FAIL: TestQueue (3.02s)
    queue_test.go:41: want 2 envelopes, got 1
    queue_test.go:44: and another
FAIL
FAIL	github.com/enes-alatas/spool/itest	339.385s
LOG

check "a failed test with no message is still named" \
	"::error title=tier 2::--- FAIL: TestQuiet (0.00s)" <<'LOG'
--- FAIL: TestQuiet (0.00s)
FAIL
LOG

check "a subtest is named, not its parent" \
	"::error title=tier 2::--- FAIL: TestRows/slack (1.10s) rows_test.go:9: boom" <<'LOG'
--- FAIL: TestRows (1.10s)
    --- FAIL: TestRows/slack (1.10s)
        rows_test.go:9: boom
FAIL
LOG

check "a parent with a message of its own keeps it" \
	$'::error title=tier 2::--- FAIL: TestRows (1.10s) rows_test.go:3: setup\n::error title=tier 2::--- FAIL: TestRows/slack (1.10s) rows_test.go:9: boom' <<'LOG'
--- FAIL: TestRows (1.10s)
    rows_test.go:3: setup
    --- FAIL: TestRows/slack (1.10s)
        rows_test.go:9: boom
LOG

check "a timeout is named with the tests it caught running" \
	"::error title=tier 2::panic: test timed out after 20m0s, running: TestDockerRows (20m0s), TestQueue/slack (19m58s)" <<'LOG'
panic: test timed out after 20m0s
	running tests:
		TestDockerRows (20m0s)
		TestQueue/slack (19m58s)

goroutine 1 [running]:
FAIL	github.com/enes-alatas/spool/itest	1200.012s
LOG

check "a build failure is named with its error" \
	$'::error title=tier 2::itest/edits_test.go:12:2: undefined: seedSlackLink\n::error title=tier 2::FAIL\tgithub.com/enes-alatas/spool/itest [build failed]' <<'LOG'
# github.com/enes-alatas/spool/itest
itest/edits_test.go:12:2: undefined: seedSlackLink
FAIL	github.com/enes-alatas/spool/itest [build failed]
LOG

check "a line cannot end its annotation or start a command" \
	"::error title=tier 2::--- FAIL: TestX (0.00s) x_test.go:1: 100%25 ::warning::not a command%0D::error::forged" < <(printf -- '--- FAIL: TestX (0.00s)\n    x_test.go:1: 100%% ::warning::not a command\r::error::forged\r\n')

check "a timeout at the end of the log is still named" \
	"::error title=tier 2::panic: test timed out after 20m0s, running: TestDockerRows (20m0s)" < <(printf 'panic: test timed out after 20m0s\n\trunning tests:\n\t\tTestDockerRows (20m0s)\n')

{ for i in 1 2 3 4; do printf -- '--- FAIL: Test%d (0.00s)\n' "$i"; done; } >"$work/many"
MAX=3 check "past the limit, the last says how many more" \
	$'::error title=tier 2::--- FAIL: Test1 (0.00s)\n::error title=tier 2::--- FAIL: Test2 (0.00s)\n::error title=tier 2::and 2 more: the job log has them all' <"$work/many"

if [[ $(bash scripts/test-failures.sh "$work/absent" "tier 2" 2>/dev/null; echo "exit $?") == "exit 0" ]]; then
	pass "a missing log annotates nothing and fails nothing"
else
	fail "a missing log annotates nothing and fails nothing"
fi

if ((failures)); then
	echo "$failures check(s) failed"
	exit 1
fi
echo "all checks passed"
