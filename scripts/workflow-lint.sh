#!/usr/bin/env bash
# Checks .github/workflows/*.yml for the ways a workflow has actually broken
# here. One rule per incident, and a comment naming it: these are not style
# preferences, they are defects that shipped, passed review, and were found
# by a human reading the Actions tab days later.
#
# The class this answers is narrower than "lint your YAML": a workflow that
# fires on issues, comments or a schedule is never exercised by PR CI, so a
# PR that breaks it is green (#209). Three of those got through in one week.
#
# Deliberately stdlib — grep, sed, awk, and a parser that understands this
# repo's house style (two-space indent, block mappings) rather than YAML in
# general. A third-party action here would be a dependency argued in a PR
# (CONVENTIONS.md); the rules are worth more than their generality.
#
# Usage:
#   scripts/workflow-lint.sh [file...]    # default: .github/workflows/*.yml
# Exits 1 and prints file:line findings when a rule fires.
set -euo pipefail

files=("$@")
if [ "${#files[@]}" -eq 0 ]; then
  # A glob with no match would otherwise be passed through literally.
  shopt -s nullglob
  files=(.github/workflows/*.yml .github/workflows/*.yaml)
fi

if [ "${#files[@]}" -eq 0 ]; then
  echo "workflow-lint: no workflow files to check" >&2
  exit 1
fi

# Rule 4 (#210): the sentinel's watch-list is complete.
#
# `workflow_run` takes an explicit `workflows:` list, so a workflow added to
# the repo and not added there fails on main into a tab nobody reads — the
# #203 outcome, reached through the thing built to report it. `ci` is excluded
# because a PR failure is already a red check its author is looking at, and
# `ci-health` because a sentinel watching itself writes a loop.
#
# It lives here rather than in the awk because it is the one rule about the
# set of files rather than about a file, and it is skipped when linting an
# explicit list: a fixture directory holding one workflow says nothing about
# what the repo's sentinel should watch.
sentinel=.github/workflows/ci-health.yml
if [ "$#" -eq 0 ] && [ -f "$sentinel" ]; then
  watched=$(awk '/^    workflows:/ { gsub(/[][,]/, " "); for (i = 2; i <= NF; i++) print $i }' "$sentinel")
  for file in "${files[@]}"; do
    name=$(awk '/^name:/ { print $2; exit }' "$file")
    case "$name" in ci | ci-health | "") continue;; esac
    if ! grep -qxF "$name" <<<"$watched"; then
      printf '%s:1: workflow `%s` is missing from the `workflows:` list here, so its failures on main are reported to nobody (#210). Add it, or say in this file why it is exempt.\n' "$sentinel" "$name"
      unwatched=$((${unwatched:-0} + 1))
    fi
  done
fi

findings=${unwatched:-0}
for file in "${files[@]}"; do
  out=$(awk -f "$(dirname "$0")/workflow-lint.awk" "$file")
  if [ -n "$out" ]; then
    printf '%s\n' "$out"
    findings=$((findings + $(printf '%s\n' "$out" | wc -l)))
  fi
done

if [ "$findings" -gt 0 ]; then
  printf '\n%s finding(s). Each line names the incident its rule prevents.\n' "$findings" >&2
  exit 1
fi
printf 'workflow-lint: %s file(s) clean\n' "${#files[@]}"
