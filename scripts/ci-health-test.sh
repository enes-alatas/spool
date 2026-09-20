#!/usr/bin/env bash
# Exercises the reporting step of .github/workflows/ci-health.yml.
#
# That step is inline in the YAML rather than a script under scripts/, because
# a script is only readable after actions/checkout and a broken checkout is
# the failure class the sentinel exists to survive (#203). Inline code is
# still code, so this harness extracts the block and runs it the way the
# runner does — `bash -e`, a stub `gh`, a fixture event on disk.
#
# Extracting rather than copying is the point: a copy would drift, and the
# test would go on passing against a workflow that no longer says this.
set -uo pipefail

cd "$(dirname "$0")/.."
root=$(pwd)
workflow=.github/workflows/ci-health.yml
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

# The `run: |` block of the reporting step, dedented to column zero.
awk '
  /^        run: \|$/ { taking = 1; next }
  taking {
    if ($0 !~ /^          / && $0 !~ /^[[:space:]]*$/) exit
    sub(/^          /, "")
    print
  }
' "$workflow" > "$work/step.sh"

if [ ! -s "$work/step.sh" ]; then
  fail 'could not extract the reporting step from the workflow'
  exit 1
fi
if ! grep -q 'issues/.*comments' "$work/step.sh"; then
  fail 'the extracted block does not look like the reporting step'
  exit 1
fi

stub=$(mktemp -d)
cat > "$stub/gh" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$CALLS"
for arg in "$@"; do
  case "$arg" in body=@*) cp "${arg#body=@}" "$SENT";; esac
done
# A read — the dispatch path fetching the run it was pointed at.
if [[ "$*" != *--method* ]]; then cat "${FETCH:-/dev/null}"; fi
STUB
chmod +x "$stub/gh"

# A completed run, in the shape the API and the event both use.
run_json() { # name conclusion branch
  # Built with jq, so a fixture branch name holding quotes is valid JSON —
  # the hostile-name case below is worthless if the payload it travels in is
  # malformed, because then the step fails on the fixture, not on the name.
  jq -nc --arg name "$1" --arg conclusion "$2" --arg branch "$3" '{
    name: $name, conclusion: $conclusion, head_branch: $branch,
    html_url: "https://github.com/o/r/actions/runs/42", event: "issues",
    head_sha: "0123456789abcdef", updated_at: "2026-09-20T12:00:00Z"
  }'
}

run() { # event_name payload_file
  local dir
  dir=$(mktemp -d)
  cp "$2" "$dir/event.json"
  : > "$dir/calls"
  # Run it from a scratch directory, as the runner runs it from the
  # workspace: the step writes its working files to the current directory,
  # and a harness that leaves those in the repo is one `git add -A` away
  # from committing them.
  ( cd "$dir" &&
    PATH="$stub:$PATH" CALLS="$dir/calls" SENT="$dir/sent" FETCH="${FETCH:-}" \
      GITHUB_REPOSITORY=o/r GITHUB_EVENT_NAME="$1" \
      GITHUB_EVENT_PATH="$dir/event.json" GITHUB_STEP_SUMMARY="$dir/summary" \
      bash -e "$work/step.sh" >"$dir/out" 2>"$dir/err" )
  STATUS=$?
  CALLS_FILE="$dir/calls"
  SENT_FILE="$dir/sent"
  ERR_FILE="$dir/err"
}

# The case the whole workflow exists for.
printf '{"workflow_run":%s}' "$(run_json secret-redact failure main)" > "$work/e.json"
run workflow_run "$work/e.json"
if [ "$STATUS" -ne 0 ]; then
  fail "a failure exits $STATUS, want 0 ($(cat "$ERR_FILE"))"
elif ! grep -q 'POST repos/o/r/issues/216/comments' "$CALLS_FILE"; then
  fail "a failure was not reported: $(cat "$CALLS_FILE")"
elif ! grep -q 'secret-redact' "$SENT_FILE"; then
  fail "the comment does not name the workflow: $(cat "$SENT_FILE")"
elif ! grep -q 'actions/runs/42' "$SENT_FILE"; then
  fail 'the comment does not link the run'
elif ! grep -q '0123456' "$SENT_FILE"; then
  fail 'the comment does not carry the head sha'
else
  pass 'a failed run is reported to the pinned issue'
fi

# The normal state. A sentinel that comments on success is a sentinel someone
# mutes, and then the failures are unread too.
printf '{"workflow_run":%s}' "$(run_json secret-redact success main)" > "$work/e.json"
run workflow_run "$work/e.json"
if [ "$STATUS" -ne 0 ] || [ -s "$CALLS_FILE" ]; then
  fail "a successful run exits $STATUS and called: $(cat "$CALLS_FILE")"
else
  pass 'a successful run says nothing'
fi

# Every conclusion, both ways round. Silence is an allowlist: the first
# version of this step reported `failure` alone, which made a run that timed
# out or never started a silent no-op — and a startup_failure is the workflow
# not running at all, the #203 shape this sentinel exists for.
#
# Cancelled and skipped stay silent deliberately: a superseded run is
# cancelled by design on every force-push (ci.yml's concurrency), and
# reporting those buries the real ones inside a week.
for conclusion in cancelled skipped neutral stale; do
  printf '{"workflow_run":%s}' "$(run_json secret-redact "$conclusion" main)" > "$work/e.json"
  run workflow_run "$work/e.json"
  if [ -s "$CALLS_FILE" ]; then
    fail "a $conclusion run was reported"
  else
    pass "a $conclusion run says nothing"
  fi
done

for conclusion in timed_out startup_failure action_required some_future_conclusion; do
  printf '{"workflow_run":%s}' "$(run_json secret-redact "$conclusion" main)" > "$work/e.json"
  run workflow_run "$work/e.json"
  if ! grep -q 'POST repos/o/r/issues/216/comments' "$CALLS_FILE"; then
    fail "a $conclusion run was not reported: $(cat "$CALLS_FILE")"
  elif ! grep -qF "$conclusion" "$SENT_FILE"; then
    fail "the comment does not name the conclusion $conclusion"
  else
    pass "a $conclusion run is reported"
  fi
done

# Self-noise: a dispatch can name any run id, including one of this
# workflow's own failures.
printf '{"workflow_run":%s}' "$(run_json ci-health failure main)" > "$work/e.json"
run workflow_run "$work/e.json"
if [ -s "$CALLS_FILE" ]; then
  fail 'the sentinel reported on itself'
else
  pass 'the sentinel does not report on itself'
fi

# The dispatch path (#209): the only way to prove the posting path, since a
# healthy repo never fires the real trigger.
FETCH="$work/fetched.json"
run_json needs-type failure main > "$FETCH"
export FETCH
printf '{"inputs":{"run_id":"42"}}' > "$work/e.json"
run workflow_dispatch "$work/e.json"
if [ "$STATUS" -ne 0 ]; then
  fail "a dispatched failure exits $STATUS ($(cat "$ERR_FILE"))"
elif ! grep -q 'api repos/o/r/actions/runs/42' "$CALLS_FILE"; then
  fail "the dispatch path did not fetch the run: $(cat "$CALLS_FILE")"
elif ! grep -q 'POST repos/o/r/issues/216/comments' "$CALLS_FILE"; then
  fail 'a dispatched failure was not reported'
else
  pass 'a dispatched run reports the run it was given'
fi

# Same path, healthy run: the rehearsal an author does when the run they
# picked turns out to have passed.
run_json needs-type success main > "$FETCH"
run workflow_dispatch "$work/e.json"
if [ "$STATUS" -ne 0 ] || grep -q POST "$CALLS_FILE"; then
  fail "a dispatched success exits $STATUS and posted: $(cat "$CALLS_FILE")"
else
  pass 'a dispatched successful run says nothing'
fi
unset FETCH

# A run that has not concluded yet — the ordinary slip on the dispatch path
# this PR makes mandatory: an id copied while the run is still going. It must
# not post (the comment would name no conclusion and still ask for a bug),
# and it must not pass either: a green run that proved nothing is what the
# author would link as evidence.
FETCH="$work/fetched.json"
jq -nc '{name: "secret-redact", conclusion: null, head_branch: "main", html_url: "https://github.com/o/r/actions/runs/42", event: "issues", head_sha: "0123456789abcdef", updated_at: "2026-09-20T12:00:00Z", id: 42}' > "$FETCH"
export FETCH
printf '{"inputs":{"run_id":"42"}}' > "$work/e.json"
run workflow_dispatch "$work/e.json"
if [ "$STATUS" -eq 0 ]; then
  fail 'a run that has not concluded passed — the dispatch proved nothing and said so quietly'
elif grep -q POST "$CALLS_FILE"; then
  fail "an unconcluded run was reported: $(cat "$SENT_FILE")"
elif ! grep -q 'has not concluded' "$ERR_FILE"; then
  fail "the failure does not say why: $(cat "$ERR_FILE")"
else
  pass 'a run that has not concluded reports nothing and fails loudly'
fi
unset FETCH

# A branch name is attacker-writable text — anyone who can open a PR picks
# one. It reaches the comment through jq, never through the shell, so a name
# that would close the quoting is data either way.
printf '{"workflow_run":%s}' "$(run_json secret-redact failure '"; rm -rf / #')" > "$work/e.json"
run workflow_run "$work/e.json"
if [ "$STATUS" -ne 0 ]; then
  fail "a hostile branch name broke the step: $(cat "$ERR_FILE")"
elif ! grep -qF 'rm -rf' "$SENT_FILE"; then
  fail 'the branch name did not reach the comment as data'
else
  pass 'a hostile branch name is data, not shell'
fi

if [ "$failures" -gt 0 ]; then
  printf '\n%s check(s) failed\n' "$failures"
  exit 1
fi
printf '\nall checks passed\n'
