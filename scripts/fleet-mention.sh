#!/usr/bin/env bash
# Reads a GitHub body on stdin and prints the first fleet name it
# @-mentions (#630). Exit 1 when there is one, 0 when there is none, 2 when
# it can't tell. The names are .claude/hooks/fleet-names, the list the
# repository's PreToolUse hook refuses on: the loops share one GitHub
# identity, and their names and the people's chat handles belong to
# strangers there, so an @ before one pings whoever owns that account.
#
# This is the after-the-fact net for a body the hook never saw, so it
# matches only what GitHub turns into a ping: an @ inside a code span, a
# fenced block, an HTML comment or a URL pings nobody, and is skipped. An
# indented code block is not told apart and is read as text. The hook is
# stricter, since it reads a command, not rendered Markdown.
#
# Usage: fleet-mention.sh [names-file] < body
set -uo pipefail

names_file=${1:-$(dirname "${BASH_SOURCE[0]}")/../.claude/hooks/fleet-names}
[ -r "$names_file" ] || exit 2
names=$(grep -v '^#' "$names_file" | grep . | paste -sd'|' -)
[ -n "$names" ] || exit 2

# What GitHub renders as text, with code and comments taken out: a fenced
# block (closed only by a run of its opener's character at least as long,
# indented at most three spaces, as CommonMark has it) and an HTML comment,
# which may span lines. Inline code spans go after, line by line.
mention=$(awk '
	# fence_run prints the fence a line opens or closes with, or "" when
	# it has none: up to three spaces, then three or more ` or ~.
	function fence_run(line,    indent, c, n) {
		indent = 0
		while (substr(line, indent + 1, 1) == " ") indent++
		if (indent > 3) return ""
		c = substr(line, indent + 1, 1)
		if (c != "`" && c != "~") return ""
		n = 0
		while (substr(line, indent + n + 1, 1) == c) n++
		if (n < 3) return ""
		return substr(line, indent + 1, n)
	}
	{
		line = $0
		if (fence != "") {
			run = fence_run(line)
			rest = substr(line, index(line, run) + length(run))
			if (run != "" && substr(run, 1, 1) == substr(fence, 1, 1) && length(run) >= length(fence) && rest ~ /^[ \t]*$/)
				fence = ""
			next
		}
		if (!comment && (run = fence_run(line)) != "") { fence = run; next }
		out = ""
		while (line != "") {
			if (comment) {
				end = index(line, "-->")
				if (!end) { line = ""; break }
				line = substr(line, end + 3); comment = 0
			} else {
				start = index(line, "<!--")
				if (!start) { out = out line; line = ""; break }
				out = out substr(line, 1, start - 1)
				line = substr(line, start + 4); comment = 1
			}
		}
		print out
	}
' | sed -E 's/`[^`]*`//g' |
	grep -Eoi "(^|[^[:alnum:]_.+/-])@($names)([^[:alnum:]_-]|$)" | head -n 1 |
	sed -E 's/^[^@]*@//; s/[^[:alnum:]]+$//')
if [ -n "$mention" ]; then
	printf '%s\n' "$mention"
	exit 1
fi
exit 0
