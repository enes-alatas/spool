#!/usr/bin/env bash
# Exercises scripts/workflow-lint.sh against fixture workflows.
#
# Every fixture here is a defect that actually shipped, reduced to the
# smallest file that still holds it — so a rule that stops catching its
# incident fails a named test rather than quietly passing everything. The
# negative fixtures matter as much: a lint that fires on a correct workflow
# gets disabled within a week.
set -uo pipefail

cd "$(dirname "$0")/.."
root=$(pwd)
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

# name, expectation ("clean" or a substring of the finding), body on stdin.
check() {
  local name=$1 want=$2 file="$work/$1.yml" out status
  cat > "$file"
  out=$(bash "$root/scripts/workflow-lint.sh" "$file" 2>&1)
  status=$?
  if [ "$want" = clean ]; then
    if [ "$status" -ne 0 ]; then fail "$name: want clean, got: $out"; else pass "$name"; fi
  elif [ "$status" -eq 0 ]; then
    fail "$name: want a finding, got none"
  elif ! grep -qF "$want" <<<"$out"; then
    fail "$name: want a finding mentioning '$want', got: $out"
  else
    pass "$name"
  fi
}

# Rule 1 (#209) — an event-driven workflow nothing can run from a branch.
check event-driven-without-dispatch '#209' <<'YML'
name: x
on:
  issues:
    types: [opened]
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: true
YML

check event-driven-with-dispatch clean <<'YML'
name: x
on:
  issues:
    types: [opened]
  workflow_dispatch:
    inputs:
      issue:
        required: true
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: true
YML

# The flow form of `on:` is the same trigger written differently; a rule that
# only reads the block form would pass this file while it broke.
check event-driven-flow-form '#209' <<'YML'
name: x
on: [issues, push]
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: true
YML

# PR and push workflows are exercised by CI on every PR. Requiring a manual
# trigger on them would be noise, and noise is how a lint gets turned off.
check pr-only-needs-no-dispatch clean <<'YML'
name: x
on:
  pull_request:
  push:
    branches: [main]
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: true
YML

# Rule 2 (#203) — the exact shape that shipped in #200: two write scopes
# named, `contents` silently set to none, checkout fails on a private repo.
check checkout-without-contents '#203' <<'YML'
name: x
on:
  workflow_dispatch:
permissions:
  issues: write
  pull-requests: write
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: true
YML

# Naming the key is not granting the scope. Without this fixture, a rule that
# only checked for `contents:` at all would pass the whole suite: every other
# fixture that declares it declares it as `read`.
check checkout-with-contents-none '#203' <<'YML'
name: x
on:
  workflow_dispatch:
permissions:
  contents: none
  issues: write
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
YML

check checkout-with-contents clean <<'YML'
name: x
on:
  workflow_dispatch:
permissions:
  contents: read
  issues: write
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
YML

# No block at all is the default grant, which includes contents: read. A lint
# that fired here would fire on most of the workflows people write.
check checkout-with-no-block clean <<'YML'
name: x
on:
  workflow_dispatch:
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
YML

# A job-level block overrides the workflow-level one, in both directions:
# a good workflow grant does not save a job that drops contents...
check job-perms-drop-contents '#203' <<'YML'
name: x
on:
  workflow_dispatch:
permissions:
  contents: read
jobs:
  j:
    runs-on: ubuntu-latest
    permissions:
      issues: write
    steps:
      - uses: actions/checkout@v4
YML

# ...and a job that grants it is fine under a workflow block that does not.
check job-perms-add-contents clean <<'YML'
name: x
on:
  workflow_dispatch:
permissions:
  issues: write
jobs:
  j:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      issues: write
    steps:
      - uses: actions/checkout@v4
YML

# The grant is per job: a job that never checks out is not affected by its
# own empty grant, and must not be reported.
check no-checkout-no-finding clean <<'YML'
name: x
on:
  workflow_dispatch:
permissions:
  issues: write
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: gh issue edit 1 --add-label needs-type
YML

# Rule 3 (#207) — a body in `env:`. This is the one the class is named for:
# it looks like the careful way to do it, and the runner prints a step's env
# map in the log header, so the value is published either way.
check body-through-env '#207' <<'YML'
name: x
on:
  workflow_dispatch:
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - env:
          BODY: ${{ github.event.issue.body }}
        run: printf '%s' "$BODY" | scan
YML

# The injection form of the same value — straight into the command line.
check body-in-run '#207' <<'YML'
name: x
on:
  workflow_dispatch:
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: printf '%s' "${{ github.event.comment.body }}" | scan
YML

# Ids and numbers are not text an attacker writes; the concurrency group in
# the issue-guards workflow needs one. A rule that flagged every `github.event.*`
# would have to be waived there, and a waived rule is not a rule.
check ids-are-fine clean <<'YML'
name: x
on:
  workflow_dispatch:
concurrency:
  group: x-${{ github.event.comment.id || github.event.issue.number }}
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: true
YML

# Rule 0 — a file this parser cannot read must not be called clean. Each of
# these silenced every structural rule while reporting success, which is the
# failure mode the whole job exists to prevent, arriving through the job.
check quoted-on-key '#209' <<'YML'
name: x
"on":
  issues:
    types: [opened]
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: true
YML

check four-space-indent 'could not parse' <<'YML'
name: x
on:
    issues:
        types: [opened]
permissions:
    issues: write
jobs:
    j:
        runs-on: ubuntu-latest
        steps:
            - uses: actions/checkout@v4
YML

printf 'name: x\non:\n\tissues:\n\t\ttypes: [opened]\njobs:\n\tj:\n\t\truns-on: ubuntu-latest\n' \
  | check tab-indent 'could not parse'

# The same body, one function away — what a person writes while debugging a
# workflow, and the same value in the log either way.
check body-inside-tojson '#207' <<'YML'
name: x
on:
  workflow_dispatch:
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - env:
          E: ${{ toJSON(github.event) }}
        run: true
YML

# Rule 5 (#220) — the scopes a `gh` call needs. The shape that shipped: a
# workflow that asks the API for a run under a grant naming only `issues`,
# which is #203 wearing a different scope.
check gh-actions-without-scope '#220' <<'YML'
name: x
on:
  workflow_dispatch:
permissions:
  issues: write
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: gh api "repos/$R/actions/runs/$id" > run.json
YML

check gh-actions-with-scope clean <<'YML'
name: x
on:
  workflow_dispatch:
permissions:
  actions: read
  issues: write
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: gh api "repos/$R/actions/runs/$id" > run.json
YML

# The other two scopes, each under a grant that names the wrong one.
check gh-issues-without-scope '#220' <<'YML'
name: x
on:
  workflow_dispatch:
permissions:
  actions: read
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: gh issue edit 1 --add-label needs-type
YML

check gh-pr-without-scope '#220' <<'YML'
name: x
on:
  workflow_dispatch:
permissions:
  issues: write
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: gh api "repos/$R/pulls/comments/$id"
YML

# Only the scope has to be named, not the level: read versus write is a
# judgement about the call, and a rule that guessed it would cry wolf.
check gh-scope-read-is-enough clean <<'YML'
name: x
on:
  workflow_dispatch:
permissions:
  issues: read
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: gh issue view 1
YML

# No block at all is the repo's default grant, and `write-all` is everything:
# a rule that fired on either would fire on most workflows people write.
check gh-no-block-no-finding clean <<'YML'
name: x
on:
  workflow_dispatch:
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: gh api "repos/$R/actions/runs/$id"
YML

check gh-write-all-no-finding clean <<'YML'
name: x
on:
  workflow_dispatch:
permissions: write-all
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: gh api "repos/$R/actions/runs/$id"
YML

# The grant is per job, as in rule 2: a job that makes no call is not judged
# by another job's calls.
check gh-scope-is-per-job '#220' <<'YML'
name: x
on:
  workflow_dispatch:
jobs:
  quiet:
    runs-on: ubuntu-latest
    permissions:
      issues: write
    steps:
      - run: echo nothing
  loud:
    runs-on: ubuntu-latest
    permissions:
      issues: write
    steps:
      - run: gh api "repos/$R/actions/runs/$id"
YML

# Rule 4 (#210) — the sentinel's watch-list. This rule is about the set of
# files, so it is exercised against the repo rather than a fixture: drop a
# name from the list, and the workflow it names must be reported.
sentinel=.github/workflows/ci-health.yml
if [ ! -f "$sentinel" ]; then
  fail 'no ci-health.yml — rule 4 has nothing to check'
else
  cp "$sentinel" "$work/sentinel.keep"
  # A list naming a workflow this repo does not have, so every real one is
  # missing from it and the rule has to name the ones that exist.
  sed -i 's/^    workflows: \[.*$/    workflows: [not-a-workflow-here]/' "$sentinel"
  out=$(bash "$root/scripts/workflow-lint.sh" 2>&1)
  status=$?
  cp "$work/sentinel.keep" "$sentinel"
  if [ "$status" -eq 0 ]; then
    fail 'a workflow missing from the sentinel list was not reported'
  elif ! grep -q 'issue-guards` is missing' <<<"$out"; then
    fail "rule 4 reported something else: $out"
  else
    pass 'a workflow missing from the sentinel list is reported'
  fi
fi

# ...and the exemptions are real: `ci` reports itself on the PR, and a
# sentinel watching itself writes a loop into its own noticeboard. Neither
# appears in the list, and the clean run below proves neither is demanded.

# The repo's own workflows are the fixture that matters most: this is the
# check that runs in CI, so the rules and the files have to agree today.
out=$(bash "$root/scripts/workflow-lint.sh" 2>&1)
if [ $? -ne 0 ]; then
  fail "the repo's own workflows do not pass: $out"
else
  pass "the repo's own workflows pass"
fi

if [ "$failures" -gt 0 ]; then
  printf '\n%s check(s) failed\n' "$failures"
  exit 1
fi
printf '\nall checks passed\n'
