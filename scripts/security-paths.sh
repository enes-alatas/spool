#!/usr/bin/env bash
# Which of a PR's changed paths need a security review (#742).
#
#   bash scripts/security-paths.sh <patterns-file> <files-file>
#
# <patterns-file> is .github/security-review-paths: one bash pattern per
# line, `#` comments and blank lines ignored. <files-file> lists the PR's
# changed paths, one per line. Prints each changed path that matches a
# pattern, then exits 0 when at least one did and 1 when none did; 2 is a
# usage error. A pattern is matched against the whole path, and `*` matches
# across `/`, so `internal/egress/*` covers everything under it.
set -uo pipefail

if [ "$#" -ne 2 ] || [ ! -r "$1" ] || [ ! -r "$2" ]; then
	echo "usage: bash scripts/security-paths.sh <patterns-file> <files-file>" >&2
	exit 2
fi

patterns=()
while IFS= read -r line || [ -n "$line" ]; do
	line=${line%%#*}
	line=${line//[[:space:]]/}
	[ -n "$line" ] && patterns+=("$line")
done <"$1"

# An empty list labels nothing, silently: a file emptied by mistake would
# turn the guard off with every PR still green.
if [ "${#patterns[@]}" -eq 0 ]; then
	echo "security-paths: $1 has no patterns" >&2
	exit 2
fi

matched=0
while IFS= read -r path || [ -n "$path" ]; do
	[ -n "$path" ] || continue
	for pattern in "${patterns[@]}"; do
		# Unquoted on the right: a pattern, not a string.
		if [[ $path == $pattern ]]; then
			echo "$path"
			matched=1
			break
		fi
	done
done <"$2"

[ "$matched" -eq 1 ]
