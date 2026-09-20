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

findings=0
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
