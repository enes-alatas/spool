#!/usr/bin/env bash
# Exercises scripts/gate-annotate.sh: a failing gate gets exactly one
# escaped `::error` line, a passing one none, and the exit code and stdin
# pass through.
set -uo pipefail

cd "$(dirname "$0")/.."
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

# run <name> <stdin> CMD...: output in $work/<name>.out, exit code in $rc.
run() {
  local name=$1 input=$2; shift 2
  printf '%s' "$input" | bash scripts/gate-annotate.sh "$@" > "$work/$name.out" 2>&1
  rc=$?
}
errors() { grep -c '^::error' "$work/$1.out"; }

run pass '' 'my gate' -- bash -c 'echo fine'
if ((rc == 0)) && [[ $(errors pass) == 0 ]] && grep -qx fine "$work/pass.out"; then
  pass "a passing gate exits 0 with no annotation"
else fail "passing gate: rc=$rc, $(errors pass) annotations"; fi

run red '' 'my gate' -- bash -c 'echo first; echo "bad: thing" >&2; exit 3'
line=$(grep '^::error' "$work/red.out")
if ((rc == 3)) && [[ $(errors red) == 1 ]] &&
  [[ $line == '::error title=my gate::first%0Abad: thing' ]]; then
  pass "a failing gate keeps its exit code and annotates stdout + stderr"
else fail "failing gate: rc=$rc line=$line"; fi

run title '' 'a: b, c' -- false
line=$(grep '^::error' "$work/title.out")
if [[ $line == '::error title=a%3A b%2C c::exited 1 and printed nothing' ]]; then
  pass "the title is escaped as a property; a silent gate says so"
else fail "title escaping: $line"; fi

run escape '' t -- bash -c 'printf "100%%\r\n::warning::x\nnext\n"; exit 1'
line=$(grep '^::error' "$work/escape.out")
if [[ $(errors escape) == 1 && $line == '::error title=t::100%25%0D%0Anext' ]]; then
  pass "% and CR are escaped, and a workflow command line is not copied"
else fail "escaping: $line"; fi

run own '' t -- bash -c 'echo "::error title=x::already said"; exit 1'
if ((rc == 1)) && [[ $(errors own) == 1 ]]; then
  pass "a gate that printed its own ::error gets no second one"
else fail "own annotation: rc=$rc, $(errors own) annotations"; fi

run stdin $'piped line\n' t -- bash -c 'cat; exit 1'
if grep -q '^::error title=t::piped line$' "$work/stdin.out"; then
  pass "stdin reaches the gate"
else fail "stdin: $(cat "$work/stdin.out")"; fi

GATE_ANNOTATE_LINES=2 run tail '' t -- bash -c 'seq 1 5; exit 1'
if grep -q '^::error title=t::4%0A5$' "$work/tail.out"; then
  pass "only the last lines are kept"
else fail "tail: $(grep '^::error' "$work/tail.out")"; fi

run usage '' t --
if ((rc == 2)); then pass "a missing command is a usage error"
else fail "usage: rc=$rc"; fi

((failures == 0)) || { echo "$failures failed"; exit 1; }
echo "all passed"
