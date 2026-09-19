#!/usr/bin/env bash
# Exercises scripts/secret-redact.sh against crafted bodies.
#
# Every value here is invented — the token shapes are structurally valid and
# cryptographically worthless. The scanner's own files are exempt from the
# PR scan outright, which is what lets this file hold values that do not read
# as synthetic; proving the synthetic allowance is not a hole needs one.
#
# Runs in CI beside the redaction workflow's own rules, so a change to the
# shared pattern list has to prove it still takes out what it claimed.
set -uo pipefail

cd "$(dirname "$0")/.."
REDACT=scripts/secret-redact.sh
MARKER='`<redacted by secret-redact>`'
failures=0

# Invented values. The digits and letters are arbitrary and deliberately not
# in any repeated or sequential run, so they do not trip the allowance.
TG='7196384205:BQFgtZmKcrudPwvbxNhelisoqWTLGDMYjx'
GH='ghp_BQFgtZmKcrudPwvbxNhelisoqWTLGDMYjx'

check() { # name want_exit body expect_substring...
  local name="$1" want="$2" body="$3" out status
  shift 3
  out=$(printf '%s\n' "$body" | bash "$REDACT" 2>/dev/null)
  status=$?
  if [ "$status" -ne "$want" ]; then
    printf 'FAIL %s: exit %s, want %s\n' "$name" "$status" "$want"
    failures=$((failures + 1))
    return
  fi
  local want_text
  for want_text in "$@"; do
    if [[ "$out" != *"$want_text"* ]]; then
      printf 'FAIL %s: output does not contain %q\n' "$name" "$want_text"
      failures=$((failures + 1))
      return
    fi
  done
  printf 'ok   %s\n' "$name"
}

check 'a token in a body is replaced' 1 "here is the token: $TG" "$MARKER"
check 'a github token in a body is replaced' 1 "auth failed with $GH" "$MARKER"

# The point of the exercise: what comes out is what gets written back to a
# public artefact, so the value must not survive anywhere in it.
out=$(printf 'token: %s\n' "$TG" | bash "$REDACT" 2>/dev/null)
if [[ "$out" == *"$TG"* ]]; then
  printf 'FAIL the redacted body still holds the value\n'
  failures=$((failures + 1))
else
  printf 'ok   the redacted body never holds the value\n'
fi

# ...and neither does anything else it prints. stderr carries a count, which
# is why it exists at all: a workflow needs to know it acted.
err=$(printf 'token: %s\n' "$TG" | bash "$REDACT" 2>&1 >/dev/null)
if [[ "$err" == *"$TG"* ]] || [ "$err" != "1" ]; then
  printf 'FAIL stderr was %q, want the count 1 and nothing else\n' "$err"
  failures=$((failures + 1))
else
  printf 'ok   stderr carries the count and not the value\n'
fi

# The rest of the body has to come back untouched, or the guard costs more
# than the leak: people write around a tool that mangles their prose.
check 'the surrounding text is preserved' 1 \
  "The bridge rejected $TG with a 401, see internal/telegram/bridge.go:47" \
  'The bridge rejected ' ' with a 401, see internal/telegram/bridge.go:47'

# Two values on one line: walking the line means the second is not left
# behind by the first.
check 'two values on one line are both replaced' 1 \
  "old $TG new $GH" "old $MARKER new $MARKER"

# There is no path tier for a body — a comment is not a test file — but the
# value tier applies, because an issue about this scanner quotes the shapes
# it looks for. #151's own body carries two.
check 'an obviously synthetic value is left alone' 0 \
  'the shape is 12345678:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA' \
  '12345678:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'

check 'a fixture-marked value is left alone' 0 \
  'we test with ghp_fixtureBQFgtZmKcrudPwvbxNhe' \
  'ghp_fixtureBQFgtZmKcrudPwvbxNhe'

check 'prose with no credential is untouched' 0 \
  'The workstation is down and the loop cannot reach its clone.' \
  'The workstation is down and the loop cannot reach its clone.'

# The marker holds no credential shape, so the workflow's own edit does not
# fire it a second time. Without this the redaction is an edit loop.
redacted=$(printf 'token: %s\n' "$TG" | bash "$REDACT" 2>/dev/null)
printf '%s\n' "$redacted" | bash "$REDACT" >/dev/null 2>&1
if [ $? -ne 0 ]; then
  printf 'FAIL re-scanning a redacted body reports another hit\n'
  failures=$((failures + 1))
else
  printf 'ok   a redacted body is stable under a second pass\n'
fi

# Line structure is what a reader navigates a long body by; losing it would
# be a second kind of damage on top of the edit.
multi=$(printf 'first\ntoken: %s\nthird\n' "$TG" | bash "$REDACT" 2>/dev/null)
if [ "$(printf '%s\n' "$multi" | wc -l)" -ne 3 ]; then
  printf 'FAIL a three-line body came back as %s lines\n' "$(printf '%s\n' "$multi" | wc -l)"
  failures=$((failures + 1))
else
  printf 'ok   line structure survives\n'
fi

if [ "$failures" -gt 0 ]; then
  printf '\n%s check(s) failed\n' "$failures"
  exit 1
fi
printf '\nall checks passed\n'
