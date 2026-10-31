#!/bin/sh
# This repository's PreToolUse hook (#630): the team's GitHub conventions,
# which are this team's rather than Spool's, so they live here and not in
# the hub's hook (internal/hook). A Bash call that sends a gh body is
# refused, with exit 2 and one line on stderr, when the body
#   - @-mentions a name in fleet-names: the loops share one GitHub identity,
#     and their names and the people's chat handles belong to strangers
#     there, so an @ pings whoever owns that account; or
#   - carries no signature line, `— Name · Role`: every loop posts as the
#     same GitHub user, and the signature is how a reader tells them apart.
# The bodies read are the command itself, which holds any --body, here-doc
# or variable it sets, and every body file it names that is there yet.
# Plain sh and awk: a workstation has no jq or Python.
#
# Claude Code reads .claude/settings.json only for a session started in a
# checkout. A loop's session starts in its home, so a loop runs this hook
# from its user settings instead, reading the script and its two files from
# a clone's origin/main (CONVENTIONS.md), here ~/spool:
#   {"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "sh -c \"$(git -C \"$HOME/spool\" show origin/main:.claude/hooks/gh-body-guard.sh)\" gh-body-guard \"$HOME/spool\""}]}]}}
# $1 is that clone, and $2 the ref, origin/main unless given.
here=$(dirname "$0")
repo=${1:-}
ref=${2:-origin/main}
# hook_file prints one of this hook's own files.
hook_file() {
	if [ -n "$repo" ]; then
		git -C "$repo" show "$ref:.claude/hooks/$1"
	else
		cat "$here/$1"
	fi
}
reader=$(hook_file hook-input.awk) || exit 0
input=$(cat)
command=$(printf '%s' "$input" | LC_ALL=C awk -v want=tool_input.command "$reader")
[ -n "$command" ] || exit 0
cwd=$(printf '%s' "$input" | LC_ALL=C awk -v want=cwd "$reader")
[ -n "$cwd" ] && cd "$cwd" 2>/dev/null

# Which gh calls send a body, and whether this one gives one.
issue_pr='(^|[^[:alnum:]_./-])gh([[:space:]]+(-R|--repo)[[:space:]]+[^[:space:]]+)?[[:space:]]+(issue|pr)[[:space:]]+(create|comment|edit|review|close|reopen)([[:space:]]|$)'
api='(^|[^[:alnum:]_./-])gh[[:space:]]+api([[:space:]]|$)'
if printf '%s\n' "$command" | grep -Eq "$issue_pr"; then
	given='(^|[[:space:]])(--body|-b|--body-file|-F|--comment|-c)([=[:space:]]|$)'
elif printf '%s\n' "$command" | grep -Eq "$api"; then
	given='(^|[[:space:]])(-f|-F|--field|--raw-field)[=[:space:]]*["'"'"']?body=|(^|[[:space:]])--input([=[:space:]]|$)'
else
	exit 0
fi
printf '%s\n' "$command" | grep -Eq -- "$given" || exit 0

# The body files named, read when they are there yet; one the command
# writes first is in the command's own text. A body or a body file given
# through a variable or $(…) resolves only when the call runs, so it is
# listed apart, as "?" and the word. A path the shell starts with ~/ is
# read where the shell finds it, in $HOME (#1194); a quoted one, or one
# after --body-file=, the shell leaves as it is, and so does the hook.
files=$(printf '%s\n' "$command" | LC_ALL=C awk '{
	for (i = 1; i <= NF; i++) {
		w = $i; f = ""
		next_word = $(i + 1)
		if ((w == "-F" || w == "--field") && next_word ~ /=/) {
			# gh api'"'"'s typed field: key=@file reads the file
			if (next_word ~ /=@/) { f = next_word; sub(/^[^@]*@/, "", f) }
		} else if (w == "--body-file" || w == "-F" || w == "--input") {
			f = next_word
			if (f ~ /^~\// && ENVIRON["HOME"] != "") f = ENVIRON["HOME"] substr(f, 2)
		}
		else if (w ~ /^--(body-file|input)=/) { f = w; sub(/^[^=]*=/, "", f) }
		else if (w ~ /^(--body|-b|--comment|-c)$/ && next_word ~ /^["'"'"']?[$`]/) print "?" next_word
		else if (w ~ /^--(body|comment)=["'"'"']?[$`]/) print "?" w
		gsub(/^["'"'"']|["'"'"';]$/, "", f)
		if (f ~ /[$`]/) print "?" f
		else if (f != "" && f != "-") print f
	}
}')
unread=$(printf '%s\n' "$files" | sed -n 's/^?//p' | head -n 1)
missing=$(printf '%s\n' "$files" | grep -v '^?' | while IFS= read -r f; do
	[ -n "$f" ] && [ ! -f "$f" ] && printf '%s\n' "$f"
done | head -n 1)
bodies=$(printf '%s\n' "$command"
	printf '%s\n' "$files" | grep -v '^?' | while IFS= read -r f; do
		[ -n "$f" ] && [ -f "$f" ] && head -c 1048576 "$f" && printf '\n'
	done)

names=$(hook_file fleet-names | grep -v '^#' | grep . | paste -sd'|' -)
[ -n "$names" ] || exit 0
mention=$(printf '%s\n' "$bodies" | grep -Eoi "(^|[^[:alnum:]_.+-])@($names)([^[:alnum:]_-]|$)" | head -n 1 |
	sed -E 's/^[^@]*@//; s/[^[:alnum:]]+$//')
if [ -n "$mention" ]; then
	echo "this gh body @-mentions $mention, which on GitHub pings whoever owns that account there, not the loop or person here; write the name without the @ (#630)." >&2
	exit 2
fi

if ! printf '%s\n' "$bodies" | LC_ALL=C grep -Eq '—[[:space:]]*[[:alpha:]][^·]*·[[:space:]]*[[:alpha:]]'; then
	if [ -n "$unread" ]; then
		echo "this gh body comes through $unread, which the hook can't read before the call runs; name the body file by a literal path so it can be checked (#630)." >&2
		exit 2
	fi
	if [ -n "$missing" ]; then
		echo "this gh body comes from $missing, which isn't there for the hook to read before the call runs; write the file in an earlier call, or name it by a path the hook can find (#1194)." >&2
		exit 2
	fi
	echo "this gh body has no signature line; every loop posts as the same GitHub user, so end it with \"— Name · Role\" (#630)." >&2
	exit 2
fi
exit 0
