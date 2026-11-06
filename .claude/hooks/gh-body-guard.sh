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
# Plain sh and awk: a workstation has no jq or Python. The two pr-guards
# rules below (#1371) run CI's own bash scripts, with git and perl, which a
# clone has; a call they can't judge goes through for CI to judge.
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

# Two of pr-guards' checks, run before the call rather than minutes after it
# (#1371): the commit rules at `git push`, and a feat PR's other half at
# `gh pr create`/`edit`. CI stays the authority, since a hook only runs where
# it is set up; this is the early copy. Both run CI's own scripts, read at
# the hook's ref, so the rules have one text. Anything the hook can't work
# out, it lets through for CI to judge.
# repo_file prints one of the repository's files at the hook's ref.
repo_file() {
	if [ -n "$repo" ]; then
		git -C "$repo" show "$ref:$1"
	else
		cat "$here/../../$1"
	fi
}
# this_repo: the call runs in a clone of this repository, whose rules these
# are, and not in some other project.
slug() { sed -E 's#\.git$##; s#^.*[:/]([^/:]+/[^/:]+)$#\1#'; }
this_repo() {
	ours=$(git -C "${repo:-$here}" remote get-url origin 2>/dev/null | slug) || return 1
	theirs=$(git remote get-url origin 2>/dev/null | slug) || return 1
	[ -n "$ours" ] && [ "$ours" = "$theirs" ]
}

# A push, found among the call's words (quotes kept together), after any
# `-c <key=value>` and `-C <dir>`: the credential-helper push is
# `git -c credential.helper='!gh auth git-credential' push` (#1372). It
# prints the -C directory and what is pushed: the refspec's source after
# the remote, empty for HEAD, `-` for a deletion.
pushed=$(printf '%s\n' "$command" | LC_ALL=C awk '
{ s = s $0 "\n" }
END {
	n = 0; w = ""; inw = 0; q = ""
	for (k = 1; k <= length(s); k++) {
		c = substr(s, k, 1)
		if (q != "") { if (c == q) q = ""; else w = w c; continue }
		if (c == "\"" || c == "\047") { q = c; inw = 1; continue }
		if (c ~ /[ \t\n]/) { if (inw) { words[++n] = w; w = ""; inw = 0 } continue }
		w = w c; inw = 1
	}
	if (inw) words[++n] = w
	for (i = 1; i <= n; i++) {
		if (words[i] !~ /(^|\/)git$/) continue
		j = i + 1; dir = ""
		while (j < n && (words[j] == "-c" || words[j] == "-C")) {
			if (words[j] == "-C") dir = words[j + 1]
			j += 2
		}
		if (words[j] != "push") continue
		m = 0; src = ""
		for (j = j + 1; j <= n; j++) {
			x = words[j]
			if (x ~ /^(&&|\|\|?|;)$/ || x ~ /^[0-9]*>/) break
			if (x == "--delete" || x == "-d") { src = "-"; break }
			if (x ~ /^-/) continue
			if (++m == 2) {
				sub(/;$/, "", x); sub(/^\+/, "", x); sub(/:.*/, "", x)
				src = (x == "" ? "-" : x); break
			}
		}
		printf "push\t%s\t%s\n", dir, src
		exit
	}
}')
if [ -n "$pushed" ]; then
	dir=$(printf '%s\n' "$pushed" | cut -f2)
	src=$(printf '%s\n' "$pushed" | cut -f3)
	[ -n "$src" ] || src=HEAD
	# In the clone the push runs in; another project's clone, or a -C
	# directory that isn't there, goes through.
	said=$(
		[ "$src" != - ] || exit 0
		if [ -n "$dir" ]; then cd "$dir" 2>/dev/null || exit 0; fi
		this_repo || exit 0
		repo_file scripts/commit-subjects.sh | bash -s -- --git "origin/main..$src" 2>/dev/null
		[ $? -eq 1 ] && exit 3
		exit 0
	)
	if [ $? -eq 3 ]; then
		printf 'this push carries commits that pr-guards will refuse (#1371):\n%s\n' "$said" >&2
		exit 2
	fi
fi

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

# A feat PR that changes one half names its other half (#634): pr-guards'
# other-half check, on the body about to be sent (#1371). The hook checks
# the line's form; whether #n is an issue is CI's lookup, so here any #n
# passes. The PR's files are what this branch changes against origin/main,
# so an edit that names a PR (a number, URL or branch) is skipped: its files
# need the API, and HEAD's would be some other PR's (#1372).
pr_body='(^|[^[:alnum:]_./-])gh([[:space:]]+(-R|--repo)[[:space:]]+[^[:space:]]+)?[[:space:]]+pr[[:space:]]+(create|edit)([[:space:]]|$)'
named_pr='(^|[^[:alnum:]_./-])gh([[:space:]]+(-R|--repo)[[:space:]]+[^[:space:]]+)?[[:space:]]+pr[[:space:]]+edit[[:space:]]+[^-[:space:]]'
if printf '%s\n' "$command" | grep -Eq "$pr_body" &&
	! printf '%s\n' "$command" | grep -Eq "$named_pr" && this_repo; then
	title=$(printf '%s\n' "$command" | LC_ALL=C sed -nE 's/.*(^|[[:space:]])(--title|-t)[=[:space:]]+["'"'"']?([^"'"'"']*).*/\3/p' | head -n 1)
	case $title in
	feat:* | feat\(* | feat!*)
		scratch=$(mktemp -d) || exit 0
		git diff --name-only origin/main...HEAD > "$scratch/files" 2>/dev/null
		printf '%s\n' "$bodies" > "$scratch/body"
		said=$({ echo 'any_number() { echo issue; }'; repo_file scripts/other-half.sh; } |
			OTHER_HALF_LOOKUP=any_number bash -s -- "$title" "$scratch/body" "$scratch/files" 2>&1)
		status=$?
		rm -rf "$scratch"
		if [ "$status" -eq 1 ]; then
			printf '%s\n' "$said" >&2
			exit 2
		fi
		;;
	esac
fi
exit 0
