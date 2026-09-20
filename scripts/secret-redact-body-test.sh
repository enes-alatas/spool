#!/usr/bin/env bash
# Exercises scripts/secret-redact-body.sh — the workflow's whole step — against
# fixture event payloads and a stub `gh`.
#
# It runs the script the way Actions runs it, `bash -e`, because that is the
# difference that mattered: the first version of this step passed every check
# I ran it through with plain `bash` and, on the runner, exited at the scan
# itself — a hit is reported by exiting 1, and under -e that is a failure
# (#207). A harness that does not reproduce the runner's flags is a harness
# that agrees with you.
#
# The stub records the API calls rather than making them, so the assertions
# are about what the step *would* do: which endpoint, with which body, and —
# the one that matters on a failure — whether the incident note goes out.
set -uo pipefail

cd "$(dirname "$0")/.."
root=$(pwd)
failures=0

# Invented, structurally valid, cryptographically worthless.
TG='7196384205:BQFgtZmKcrudPwvbxNhelisoqWTLGDMYjx'

stub=$(mktemp -d)
trap 'rm -rf "$stub"' EXIT
cat > "$stub/gh" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$CALLS"
if [ "${GH_FAILS:-}" = "PATCH" ] && [[ "$*" == *PATCH* ]]; then exit 1; fi
for arg in "$@"; do
  case "$arg" in body=@*) cp "${arg#body=@}" "$SENT";; esac
done
STUB
chmod +x "$stub/gh"

# One event payload, in the shape GitHub writes to $GITHUB_EVENT_PATH.
event() { # kind body
  case "$1" in
    issues)
      printf '{"issue":{"number":206,"body":%s}}\n' "$(printf '%s' "$2" | jq -Rs .)"
      ;;
    issue_comment)
      printf '{"issue":{"number":206},"comment":{"id":991,"body":%s}}\n' "$(printf '%s' "$2" | jq -Rs .)"
      ;;
    pull_request_review_comment)
      printf '{"pull_request":{"number":204},"comment":{"id":77,"body":%s}}\n' "$(printf '%s' "$2" | jq -Rs .)"
      ;;
  esac
}

run() { # kind body -> sets STATUS, CALLS file, SENT file
  local dir
  dir=$(mktemp -d)
  event "$1" "$2" > "$dir/event.json"
  CALLS="$dir/calls" SENT="$dir/sent" : > "$dir/calls"
  # `bash -e`, as the runner invokes a `run:` block.
  PATH="$stub:$PATH" CALLS="$dir/calls" SENT="$dir/sent" \
    GH_FAILS="${GH_FAILS:-}" GITHUB_REPOSITORY=o/r GITHUB_EVENT_NAME="$1" \
    GITHUB_EVENT_PATH="$dir/event.json" \
    bash -e "$root/scripts/secret-redact-body.sh" >"$dir/out" 2>"$dir/err"
  STATUS=$?
  CALLS_FILE="$dir/calls"
  SENT_FILE="$dir/sent"
  ERR_FILE="$dir/err"
}

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

# The case the drill caught: a hit must redact and notify, under `bash -e`.
run issues "here is the token: $TG"
if [ "$STATUS" -ne 0 ]; then
  fail "a hit exits $STATUS, want 0 ($(cat "$ERR_FILE"))"
elif [ "$(wc -l < "$CALLS_FILE")" -ne 2 ]; then
  fail "a hit made $(wc -l < "$CALLS_FILE") API calls, want 2"
elif ! grep -q 'PATCH repos/o/r/issues/206' "$CALLS_FILE"; then
  fail "a hit did not PATCH the issue body: $(cat "$CALLS_FILE")"
elif ! grep -q 'POST repos/o/r/issues/206/comments' "$CALLS_FILE"; then
  fail 'a hit did not post the incident note'
elif grep -qF "$TG" "$SENT_FILE"; then
  fail 'the body written back still holds the value'
else
  pass 'a hit is redacted and notified'
fi

# The no-hit paths, where acting at all would be the bug: the workflow's own
# edit fires it again, and an issue can be opened with no body.
run issues 'nothing credential-shaped here'
if [ "$STATUS" -ne 0 ] || [ -s "$CALLS_FILE" ]; then
  fail "a clean body exits $STATUS with $(wc -l < "$CALLS_FILE") calls, want 0 and 0"
else
  pass 'a clean body is left alone'
fi

run issues ''
if [ "$STATUS" -ne 0 ] || [ -s "$CALLS_FILE" ]; then
  fail 'an empty body was acted on'
else
  pass 'an empty body is left alone'
fi

run issues "already \`<redacted by secret-redact>\` here"
if [ "$STATUS" -ne 0 ] || [ -s "$CALLS_FILE" ]; then
  fail 'a redacted body was redacted again — the edit loops'
else
  pass 'an already-redacted body does not loop'
fi

# Each event kind writes back to its own endpoint. A comment PATCHed as an
# issue would rewrite the wrong text, which is worse than not acting.
run issue_comment "here is the token: $TG"
if ! grep -q 'PATCH repos/o/r/issues/comments/991' "$CALLS_FILE"; then
  fail "an issue comment went to the wrong endpoint: $(cat "$CALLS_FILE")"
else
  pass 'an issue comment is written back as a comment'
fi

run pull_request_review_comment "here is the token: $TG"
if ! grep -q 'PATCH repos/o/r/pulls/comments/77' "$CALLS_FILE"; then
  fail "a review comment went to the wrong endpoint: $(cat "$CALLS_FILE")"
elif ! grep -q 'POST repos/o/r/issues/204/comments' "$CALLS_FILE"; then
  fail "a review comment's note went to the wrong thread: $(cat "$CALLS_FILE")"
else
  pass "a review comment is written back, its note on the pull request"
fi

# A failed write must not be followed by a note saying the value is gone.
GH_FAILS=PATCH run issues "here is the token: $TG"
unset GH_FAILS
if [ "$STATUS" -eq 0 ]; then
  fail 'a failed PATCH reported success'
elif grep -q 'POST' "$CALLS_FILE"; then
  fail 'a failed PATCH still posted the note'
else
  pass 'a failed write posts no note'
fi

# The guard failing is not a hit: a truncated body must never be written back.
chmod 000 scripts/secret-rules.awk
run issues "here is the token: $TG"
chmod 644 scripts/secret-rules.awk
if [ "$STATUS" -eq 0 ] || [ -s "$CALLS_FILE" ]; then
  fail 'an unreadable rules file was treated as a hit'
else
  pass 'a broken scanner writes nothing'
fi

# The call site, not the script. Defect 2 of #207 was one line of YAML: a
# body handed to a step through `env:` is printed in that step's "Run" group
# header, so the leak happened before this script ever ran and no test of the
# script can see it. What holds is an invariant over the workflow — the body
# is never named in an expression, because `${{ }}` is substituted into the
# file the runner echoes. Ids are fine and the concurrency group needs one;
# text is not.
workflow=.github/workflows/secret-redact.yml
if grep -nE '\$\{\{[^}]*\.body' "$workflow"; then
  fail 'the workflow interpolates a body into itself — it will be echoed in the log'
else
  pass 'the workflow never interpolates a body'
fi

# The same defect wearing a different hat: anything in the step's env map is
# echoed, so the step that handles bodies is allowed exactly one variable.
step_env=$(awk '/take any credential shape/, /run:/' "$workflow" | grep -oE '^ +[A-Z_]+:' | tr -d ' :' | paste -sd, -)
if [ "$step_env" != "GH_TOKEN" ]; then
  fail "the redaction step's env is [$step_env], want GH_TOKEN only"
else
  pass "the redaction step passes only its token through the environment"
fi

if [ "$failures" -gt 0 ]; then
  printf '\n%s check(s) failed\n' "$failures"
  exit 1
fi
printf '\nall checks passed\n'
