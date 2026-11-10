#!/usr/bin/env bash
# Whether an issue's body carries its form's headings (#1474). A form in
# .github/ISSUE_TEMPLATE/ prints a `### <label>` heading for each of its
# fields, in order; an issue filed with gh or the API skips the form, so its
# author copies the headings by hand. This reads them from the form, so the
# forms stay the only list, and says which are missing.
#
# The form is the one whose `labels:` names the issue's type label. An issue
# with no type label, or more than one, is the typing guard's to flag, and a
# `community` issue is typed at triage: both pass here.
#
# What is held:
#   - every required field's heading, in the form's order (the web form
#     prints an optional one with "_No response_", and a gh filing may leave
#     it out), as a line `### <label>`
#     outside a code fence; other headings may come between them;
#   - except a dropdown's: in a gh filing its value is a label, not a
#     heading, so the bug form's Severity is held as
#   - a `severity:*` label on a bug.
#
# Usage: issue-headings.sh <forms-dir> <labels-file> < body.md
#   labels-file: the issue's labels, one per line.
#        issue-headings.sh --types   prints the type labels, as an ERE
# Exit 0 when the body passes, 1 with one line per missing part on stdout,
# 2 on a usage error.
set -euo pipefail

# The type labels, one per form. The typing step reads the same list with
# `issue-headings.sh --types`, so it is written once.
types='bug|new feature|improvement|refactor|chore|security|documentation|question'
if [ "${1:-}" = --types ]; then
	echo "$types"
	exit 0
fi
forms=${1:-}
labels_file=${2:-}
if [ -z "$forms" ] || [ -z "$labels_file" ] || [ ! -d "$forms" ] || [ ! -f "$labels_file" ]; then
	echo "usage: issue-headings.sh <forms-dir> <labels-file> < body.md" >&2
	exit 2
fi
labels=$(cat "$labels_file")
labelled() { grep -qxF -- "$1" <<<"$labels"; }

body=$(cat)
labelled community && exit 0

# form_labels prints a form's `labels:` entries, one per line.
form_labels() {
	sed -nE 's/^labels:[[:space:]]*\[(.*)\][[:space:]]*$/\1/p' "$1" |
		tr ',' '\n' | sed -E 's/^[[:space:]]*["'\'']?//; s/["'\'']?[[:space:]]*$//' | grep .
}

form=""
n=0
for f in "$forms"/*.yml; do
	[ -f "$f" ] || continue
	case $(basename "$f") in config.yml) continue ;; esac
	while IFS= read -r l; do
		# Only a type label picks a form: the report form's `triage`, or any
		# label two forms share, would match a second form and skip the check.
		grep -qxE -- "($types)" <<<"$l" || continue
		if labelled "$l"; then
			form=$f
			n=$((n + 1))
		fi
	done < <(form_labels "$f" || true)
done
# No type, or two: the typing guard's case, not this one's.
[ "$n" -eq 1 ] || exit 0

# The form's field headings, in order: each body item's `label:` under its
# `attributes:`, when it has a `required: true` (a checkbox's may sit on an
# option, as the refactor form's Behavior contract does), skipping
# markdown blocks (which have none) and dropdowns.
# A checkbox option's `- label:` is deeper and is not a field.
headings=$(awk '
	function flush() { if (label != "" && req && kind != "dropdown" && kind != "markdown") print label; label = ""; kind = ""; req = 0 }
	/^  - type:/ { flush(); kind = $3; next }
	/^      +required:[[:space:]]*true/ { req = 1 }
	/^      label:/ && label == "" {
		label = $0
		sub(/^      label:[[:space:]]*/, "", label)
		sub(/[[:space:]]+$/, "", label)
		if (label ~ /^".*"$/ || label ~ /^\047.*\047$/) label = substr(label, 2, length(label) - 2)
	}
	END { flush() }
' "$form")

# The body's headings, outside code fences, with their marks stripped.
found=$(printf '%s\n' "$body" | tr -d '\r' | awk '
	/^[[:space:]]*(```|~~~)/ { fence = !fence; next }
	!fence && /^###[[:space:]]/ { h = $0; sub(/^###[[:space:]]+/, "", h); sub(/[[:space:]#]+$/, "", h); print h }
')

missing=()
rest=$found
while IFS= read -r h; do
	[ -n "$h" ] || continue
	# Find h among the headings after the last one matched.
	if at=$(grep -nxF -- "$h" <<<"$rest" | head -n 1 | cut -d: -f1) && [ -n "$at" ]; then
		rest=$(tail -n +"$((at + 1))" <<<"$rest")
	else
		missing+=("heading: ### $h")
	fi
done <<<"$headings"

if [ "$(basename "$form")" = bug.yml ] && ! grep -q '^severity:' <<<"$labels"; then
	missing+=("label: severity:* (critical, high, medium, low or info)")
fi

[ "${#missing[@]}" -eq 0 ] && exit 0
printf '%s\n' "${missing[@]}"
exit 1
