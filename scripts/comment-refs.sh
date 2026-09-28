#!/usr/bin/env bash
# Fails when a code comment cites an ADR section: "ADR-0032 item 4",
# "ADR-0017 §6", "ADR-0026's decision 4", "item 2 of ADR-0032", or a bare "§9"
# pointing into one, in any case.
#
# CONVENTIONS.md "Comments": a comment states its reason in place, and cites an
# ADR by bare number at most. A section number drifts when the ADR is amended
# and the comment is left pointing at the wrong item. Review alone let 53 of
# these through, so this check enforces it (#410).
#
# Only comment text is read: `//` and `/* */` in Go, TS, TSX and CSS. A
# citation split across two comment lines ("(ADR-0032" / "item 2)") is caught
# on the second. A section of an external spec ("RFC 9110 §7.6.1") is not an
# ADR and passes.
#
# Usage:
#   scripts/comment-refs.sh [file...]    # default: every tracked source file
# Exits 1 and prints file:line findings.
set -euo pipefail

files=("$@")
if [ "${#files[@]}" -eq 0 ]; then
  cd "$(dirname "$0")/.."
  mapfile -t files < <(git ls-files -- '*.go' '*.ts' '*.tsx' '*.css')
fi

awk '
  FNR == 1 { inblock = 0; prev = "" }

  # Where the last occurrence of s starts in text, or 0.
  function last(text, s,    at, n) {
    at = 0
    while ((n = index(substr(text, at + 1), s)) > 0) at += n
    return at
  }

  # The part of the line that is comment, or "" when there is none. A block
  # is open after the line when its last "/*" follows its last "*/", so
  # "/* a */ /* b" leaves the next line inside one.
  function comment(line,    i, j, text, opens, closes) {
    if (inblock) {
      text = line
    } else {
      i = index(line, "//"); j = index(line, "/*")
      if (i == 0 && j == 0) return ""
      if (i > 0 && (j == 0 || i < j)) return substr(line, i + 2)
      text = substr(line, j)
    }
    opens = last(text, "/*"); closes = last(text, "*/")
    if (opens > 0 || closes > 0) inblock = opens > closes
    return text
  }

  # A section written either way round ("ADR-0032 item 4", possessive too,
  # or "item 4 of ADR-0032"), or a bare "§n", in any case. The program is
  # single-quoted in the shell, so an apostrophe here is written \047.
  function cites(text,    section) {
    text = tolower(text)
    gsub(/rfc[ ]*[0-9]+[ ]*§[ ]*[0-9.]+/, "", text)
    section = "(§|items?|sections?|decisions?)[ ]*[0-9]"
    return text ~ ("adr-?[0-9]+(\047s)?,?[ ]*" section) ||
      text ~ (section "[0-9.]*[ ]+(of|in)[ ]+(the[ ]+)?adr-?[0-9]") ||
      text ~ /§[ ]*[0-9]/
  }

  {
    text = comment($0)
    gsub(/^[ \t*\/]+/, "", text)
    if (text == "") { prev = ""; next }
    if (cites(text) || (!cites(prev) && prev != "" && cites(prev " " text))) {
      printf "%s:%d: comment cites an ADR section; state the reason in place, or cite the ADR by bare number: %s\n", FILENAME, FNR, text
      found = 1
    }
    prev = text
  }

  END { exit found }
' "${files[@]}"
