#!/usr/bin/env bash
# Reports test coverage of the Go lines a change adds or edits (#2).
#
# QUALITY.md asks for coverage of *changed lines* on each PR, informational
# and never a gate: a whole-repo percentage says nothing about the change in
# front of the reviewer, and a number that blocks a merge is a number people
# learn to game. This reads the diff and the profile `go test -coverprofile`
# already wrote, and says which changed statements ran and which did not.
#
# Usage:
#   git diff -U0 BASE HEAD | scripts/diff-coverage.sh cover.out
#
# Writes Markdown to stdout. A changed line counts only if it holds code and
# lies in a block the profile has. The profile alone can't say which lines
# hold code: `go test` writes one block per run of statements, spanning every
# line between them, comments and blank lines included. So the diff decides:
# a line whose text is blank, a `//` comment, or nothing but closing brackets
# (a lone `}`) is not coverable, and counts as neither covered nor missed. A
# `/* */` comment spanning lines is not recognised; the tree barely uses one.
# A declaration outside any block is not coverable either. A line that
# carries several blocks is covered when all of them ran, missed when none
# did, and partial otherwise: `if err != nil {` whose body never ran is
# partial, which is the branch the reviewer wants pointed at. Test files are
# left out; they are what runs, not what is run.
#
# The profile is tier 1's (`go test ./...`). A line only `itest` reaches
# reads as missed here, and the report says so rather than pretend otherwise.
#
# Stdlib only (ADR-0013): bash, git's diff, and POSIX awk.
set -euo pipefail

profile=${1:?usage: git diff -U0 BASE HEAD | diff-coverage.sh PROFILE}
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# The profile names files by import path; the diff names them from the repo
# root. The module line is what turns one into the other.
module=${DIFF_COVERAGE_MODULE:-$(awk '$1 == "module" { print $2; exit }' "$root/go.mod")}

rows=$(awk -v module="$module" '
  # The profile: one block per line, `path/file.go:L1.C1,L2.C2 stmts count`.
  FNR == NR {
    if (FNR == 1 && /^mode:/) next
    if (!match($1, /:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+$/)) next
    file = substr($1, 1, RSTART - 1)
    if (index(file, module "/") == 1) file = substr(file, length(module) + 2)
    split(substr($1, RSTART + 1), span, ",")
    split(span[1], from, "."); split(span[2], to, ".")
    if ($2 + 0 == 0) next
    for (line = from[1] + 0; line <= to[1] + 0; line++) {
      key = file SUBSEP line
      blocks[key]++
      if ($3 + 0 > 0) ran[key]++
    }
    next
  }
  # The diff, -U0: no context lines, so each `+` line is a changed line of
  # the new side, numbered on from its hunk header. The file header is the
  # `+++` right after a `---`, never a `+` line whose code starts with `++`.
  /^--- / { header = 1; next }
  header && /^\+\+\+ / {
    header = 0
    file = substr($0, 5)
    sub(/^b\//, "", file)
    keep = (file ~ /\.go$/ && file !~ /_test\.go$/)
    next
  }
  { header = 0 }
  /^@@ / {
    if (match($0, /\+[0-9]+/)) line = substr($0, RSTART + 1, RLENGTH - 1) + 0
    next
  }
  /^\+/ {
    code = substr($0, 2)
    if (keep) {
      touched[file SUBSEP line] = 1
      if (line > last[file]) last[file] = line
      if (code !~ /^[ \t]*(\/\/.*)?$/ && code !~ /^[ \t]*[})\]][})\],;]*[ \t]*$/)
        changed[file SUBSEP line] = 1
    }
    line++
  }
  END {
    for (file in last) {
      covered = partial = missed = 0
      gaps = ""; open = 0
      for (line = 1; line <= last[file] + 1; line++) {
        key = file SUBSEP line
        gap = 0
        if ((key in changed) && (key in blocks)) {
          if (ran[key] == blocks[key]) covered++
          else if (ran[key] > 0) { partial++; gap = 1 }
          else { missed++; gap = 1 }
        }
        # Consecutive gap lines collapse to a range. A changed line that
        # holds no code, or none the profile has, neither breaks a range nor
        # starts one; an unchanged line or one that ran ends it.
        if (gap) {
          if (!open) { start = line; open = 1 }
          end = line
        } else if (open && (!(key in touched) || ((key in changed) && (key in blocks)))) {
          gaps = gaps (gaps == "" ? "" : ", ") (start == end ? start : start "-" end)
          open = 0
        }
      }
      if (covered + partial + missed > 0)
        printf "%s\t%d\t%d\t%d\t%s\n", file, covered, partial, missed, gaps
    }
  }
' "$profile" -)

echo '## Diff coverage (informational, never a gate)'
echo
if [ -z "$rows" ]; then
  echo 'This change adds or edits no Go statement outside a test file.'
  exit 0
fi

sorted=$(sort <<<"$rows")
read -r covered partial missed < <(awk -F'\t' '{ c += $2; p += $3; m += $4 } END { print c, p, m }' <<<"$sorted")
total=$((covered + partial + missed))
echo "**$covered of $total** changed statement lines ran under tier 1 ($((covered * 100 / total))%)," \
  "$partial partly, $missed not at all."
echo
echo '| File | Ran | Partly | Not | Lines to look at |'
echo '|---|---:|---:|---:|---|'
awk -F'\t' '{ printf "| `%s` | %d | %d | %d | %s |\n", $1, $2, $3, $4, $5 }' <<<"$sorted"
echo
echo 'Tier 1 only (`go test ./...`): a line only `itest` exercises counts as not run.'
