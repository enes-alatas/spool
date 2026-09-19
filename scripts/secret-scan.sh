#!/usr/bin/env bash
# Fails when a change adds a credential-shaped string.
#
# On 2026-09-18 a working bot token reached an issue body verbatim, quoted as
# evidence, and nothing caught it (#146). This is the mechanical half of the
# answer: a scan of what a change *adds*, so a secret has to survive a check
# nobody has to remember to perform.
#
# It reads added lines only. A deletion cannot leak anything the history does
# not already hold, and scanning context lines would make every later PR
# inherit an older one's failure.
#
# Usage:
#   scripts/secret-scan.sh [BASE_REF]        # scan BASE_REF...HEAD (default origin/main)
#   git diff -U0 | scripts/secret-scan.sh --stdin
#
# Output is deliberately not the secret. A scanner that prints what it found
# turns a private leak into a public one, since a CI log is an artefact of its
# own; at most eight characters of a match are shown, per the fleet
# credential rule.
#
# One awk pass, no per-line subprocesses: a scanner that costs a fork per line
# is one somebody eventually moves off the critical path.
set -uo pipefail

scan() {
  awk '
  # Judges the matched value, never the line around it. A line-level test
  # launders a real token through any nearby word: `fixtureFor(<real token>)`
  # would pass, and the helper name need not even be deliberate.
  function synthetic(s,   i, run, seq, step, here, prev) {
    # What a hand-written fixture reads like and a real credential does not.
    # Three markers, because a fixture author reaches for all three: a word
    # that says so, one character four times over, or six characters running
    # up or down the alphabet. A real token has none of them — 4-in-a-row or
    # a 6-long run in 32 random characters is a rounding error away from
    # never — which is what keeps this an allowance and not a hole.
    # The word test folds; the run and sequence tests do not need to, and
    # reading the real text keeps them honest. Uppercase is how an env or
    # YAML fixture is actually written — FIXTURE_, FAKE_ — and this very
    # round brought screaming-snake into scope.
    if (tolower(s) ~ /fixture|synthetic|fake|dummy|example|placeholder|redacted|not-a-real/) return 1
    run = 1
    seq = 1
    for (i = 2; i <= length(s); i++) {
      here = ord[substr(s, i, 1)]
      prev = ord[substr(s, i - 1, 1)]
      if (substr(s, i, 1) == substr(s, i - 1, 1)) {
        if (++run >= 4) return 1
      } else {
        run = 1
      }
      if (here && prev && (here - prev == 1 || here - prev == -1)) {
        if (here - prev == step) {
          if (++seq >= 6) return 1
        } else {
          seq = 2
          step = here - prev
        }
      } else {
        seq = 1
        step = 0
      }
    }
    return 0
  }

  BEGIN {
    # awk has no ord(); this is one, over the characters a credential can be
    # written in.
    chars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
    for (i = 1; i <= length(chars); i++) ord[substr(chars, i, 1)] = i

    # One list, so a second scanner cannot drift from this one. Written in
    # lowercase: every line is folded before matching, so a class of "A-Za-z"
    # would only say twice what "a-z" already says.
    name[1] = "telegram-bot-token"; regex[1] = "[0-9]{8,}:[a-z0-9_-]{30,}"
    name[2] = "anthropic-key";      regex[2] = "sk-ant-[a-z0-9_-]{16,}"
    name[3] = "github-token";       regex[3] = "gh[pousr]_[a-z0-9]{20,}"
    name[4] = "bearer-header";      regex[4] = "bearer[ \t]+[a-z0-9._-]{24,}"
    # No word boundary before the name: `botToken`, `apiKey` and
    # `TG_BOT_TOKEN` are how a credential is actually spelled in this tree,
    # and every one of them ends in a word this matches. The operator is one
    # or two characters so Go`s `:=` counts alongside `=` and YAML`s `:`.
    name[5] = "assigned-secret";    regex[5] = "(token|secret|password|passwd|api[_-]?key)[ \t]*[:=]{1,2}[ \t]*[\"'"'"']?[a-z0-9._-]{24,}"
    patterns = 5

    # Matches here are allowed *if the value is obviously synthetic*. The
    # scanner and its own test have to contain the shapes they look for, and a
    # fixture is required to be fake already. Not a blanket: a fixture holding
    # something that does not read as synthetic still fails, which is the case
    # that actually costs someone a rotation.
    # Two tiers, because they are exempt for different reasons.
    #
    # This scanner and its test are exempt outright: the test has to feed the
    # scanner a value that does *not* read as synthetic, since proving the
    # allowance is not a hole is half of what it checks. One file, named, and
    # the only place in the tree where that is true.
    exempt_always = "^scripts/secret-scan"
    # Test files and fixtures are exempt only when the value reads as fake.
    # That is the case that actually costs someone a rotation: a fixture is
    # where a real token gets pasted "just to see it work".
    exempt_if_fake = "(^itest/|^cmd/fakeclaude/|_test\\.go$|\\.test\\.tsx?$)"
    hits = 0
  }

  /^\+\+\+ b\// { path = substr($0, 7); next }
  /^(\+\+\+|---|diff --git|index )/ { next }
  /^@@/ {
    # @@ -a,b +c,d @@ — c is the first added lines number
    if (match($0, /\+[0-9]+/)) line = substr($0, RSTART + 1, RLENGTH - 1) + 0
    next
  }
  /^\+/ {
    content = substr($0, 2)
    # Matched against a lowercased copy so an identifier`s case cannot hide a
    # credential. tolower preserves length, so RSTART and RLENGTH still index
    # the original — the excerpt is the real text, the match is case-blind.
    folded = tolower(content)
    for (i = 1; i <= patterns; i++) {
      if (match(folded, regex[i])) {
        if (path ~ exempt_always) continue
        # synthetic() uses only `~` and substr(), neither of which disturbs
        # RSTART/RLENGTH — so the excerpt printed below is still this match.
        # A future match() inside it would silently misalign both.
        if (path ~ exempt_if_fake && synthetic(substr(content, RSTART, RLENGTH))) continue
        printf "%s:%d: %s — %.8s…\n", path, line, name[i], substr(content, RSTART, RLENGTH)
        hits++
      }
    }
    line++
    next
  }

  END { exit hits > 0 ? 1 : 0 }
  '
}

if [ "${1:---range}" = '--stdin' ]; then
  scan
  status=$?
else
  git diff -U0 "${1:-origin/main}...HEAD" | scan
  status=$?
fi

if [ "$status" -ne 0 ]; then
  cat >&2 <<'MSG'

A credential shape was added by this change. If the value is real, treat it as
an incident: rotate it first, then remove it from the branch history, and tell
the operator privately. Do not paste it into the PR, an issue, or this log.

If it is a test fixture, move it into a test file (`*_test.go`, `itest/`,
`*.test.ts`) and make the value itself obviously synthetic — repeated
characters, sequential ones, or a word like "fixture" inside the value. A
helper name or a comment beside it does not count, or any nearby word would
launder a real one. Production code is never exempt.
MSG
fi
exit "$status"
