#!/usr/bin/env bash
# Fails when a change adds a credential-shaped string.
#
# On 2026-09-18 a working bot token reached an issue body verbatim, quoted as
# evidence, and nothing caught it (#146). This is the mechanical half of the
# answer: a scan of what a change *adds*, so a secret has to survive a check
# nobody has to remember to perform. The other half is scripts/secret-redact.sh,
# which guards the surface the incident actually happened on; both read their
# shapes from scripts/secret-rules.awk.
#
# Usage:
#   scripts/secret-scan.sh [BASE_REF]        # scan BASE_REF...HEAD (default origin/main)
#   git diff -U0 | scripts/secret-scan.sh --stdin
#
# One awk pass, no per-line subprocesses: a scanner that costs a fork per line
# is one somebody eventually moves off the critical path.
set -uo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

scan() {
  awk -f "$here/secret-rules.awk" -f "$here/secret-diff.awk"
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
