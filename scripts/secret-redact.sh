#!/usr/bin/env bash
# Reads a GitHub body on stdin and writes it back with every credential-shaped
# value replaced by a marker; exits 1 when it replaced something.
#
# This is the surface the 2026-09-18 incident happened on (#146): a working
# token pasted into an issue body as evidence. A body cannot be rejected the
# way a PR's diff can — it is already published by the time anything runs —
# so the guard removes the value from what people read, as fast as a workflow
# can, and tells the author what the edit does not undo.
#
# What it does not do, and the workflow's comment has to say so: editing a
# body does not erase its revisions. GitHub keeps the original in the edit
# history, so the value stays readable to anyone who opens it until someone
# deletes that revision — and it has been public regardless, so rotation is
# not optional. Redaction buys time and stops the casual reader; it is not a
# fix.
#
# "Someone", not "the author": deleting a revision is a web-UI action with no
# API behind it, so a loop that pastes a credential cannot carry out the first
# instruction the note gives it (#206). The note says that in as many words
# rather than leaving a loop to discover it, or to assume it was handled.
#
# Usage:
#   scripts/secret-redact.sh < body.md > redacted.md   # status 1 if changed
#
# stdout is the body and nothing else, so it can be piped straight into the
# API call that writes it back. The number of values replaced goes to stderr;
# the values themselves go nowhere.
set -uo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

awk -f "$here/secret-rules.awk" -f "$here/secret-body.awk" -v marker='`<redacted by secret-redact>`'
