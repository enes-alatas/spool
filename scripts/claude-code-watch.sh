#!/usr/bin/env bash
# Files a chore issue when Anthropic's apt repo serves a newer Claude Code
# than the workstation image pins (#1454).
#
# The pin, docker/workstation/claude-code.version, is a version the fleet
# has run (#1223), and moving it is a reviewed PR (#1235). Dependabot does
# not watch it, so a newer CLI, and any newer model its aliases map to,
# reached the fleet only when someone happened to notice. This reads the
# repo the Dockerfile installs from, compares, and asks for the bump.
#
# One issue per version, ever: each body carries a marker naming the version
# it asks for. If an issue holding it exists, open or closed, nothing is
# filed: a closed one is a bump done or declined. An open issue for an older
# version is retitled and rewritten to the newest, so one issue stays open.
# Every issue is listed rather than searched, because the search index lags
# a fresh issue (scripts/advisories.sh).
#
# The repo's index is read unsigned and with no credential. The worst a
# forged index can do is file an issue, and a version that isn't digits and
# dots is refused, so nothing from it but a version reaches GitHub.
#
# Usage (the workflow sets GH_TOKEN and GITHUB_REPOSITORY):
#   claude-code-watch.sh [--packages <file>] [--pin <file>] [--dry-run]
# --packages reads a saved Packages index instead of fetching it (the test
# uses it); --dry-run prints what it would file and writes nothing.
# Exit: 0 done (filed, updated, or nothing to do), 1 the check could not run.
set -uo pipefail

index_url=https://downloads.claude.ai/claude-code/apt/latest/dists/latest/main/binary-amd64/Packages
pin_file=docker/workstation/claude-code.version
packages=
dry_run=false
while [ "$#" -gt 0 ]; do
	case $1 in
	--packages) packages=${2:?--packages needs a file}; shift 2 ;;
	--pin) pin_file=${2:?--pin needs a file}; shift 2 ;;
	--dry-run) dry_run=true; shift ;;
	*) echo "usage: claude-code-watch.sh [--packages <file>] [--pin <file>] [--dry-run]" >&2; exit 1 ;;
	esac
done

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
version_re='^[0-9]+\.[0-9]+\.[0-9]+$'

if ! pin=$(tr -d '[:space:]' <"$pin_file") || ! [[ $pin =~ $version_re ]]; then
	echo "claude-code-watch: $pin_file holds no version ('${pin:-}')" >&2
	exit 1
fi

if [ -z "$packages" ]; then
	packages=$work/Packages
	if ! curl -fsS --max-time 60 -o "$packages" "$index_url"; then
		echo "claude-code-watch: could not fetch $index_url" >&2
		exit 1
	fi
fi

# Every claude-code stanza's version, its Debian revision dropped.
latest=$(awk '/^Package:/ {p = ($2 == "claude-code")} p && /^Version:/ {sub(/-[^-]*$/, "", $2); print $2}' "$packages" |
	grep -E "$version_re" | sort -V | tail -n1)
if [ -z "$latest" ]; then
	echo "claude-code-watch: the index lists no claude-code version; it is not the repo's Packages file" >&2
	exit 1
fi

if [ "$(printf '%s\n%s\n' "$pin" "$latest" | sort -V | tail -n1)" = "$pin" ]; then
	echo "claude-code-watch: the pin $pin is the newest the repo serves ($latest); nothing to file."
	exit 0
fi

marker="<!-- claude-code-watch $latest -->"
title="ci(workstation): bump the Claude Code pin from $pin to $latest"
cat >"$work/body" <<EOF
### What needs doing

The workstation image runs Claude Code $pin (\`docker/workstation/claude-code.version\`), and Anthropic's apt repo now serves $latest. Bump the pin in one PR, after a loop has run the new CLI, moving these together (the \`/release\` skill, #1235):

- [ ] \`docker/workstation/claude-code.version\`
- [ ] \`internal/claude.TestedVersion\`
- [ ] fakeclaude's \`--version\` line
- [ ] the stream-json check on the new build in \`cmd/fakeclaude/FIDELITY.md\`
- [ ] a row in \`internal/loop/models.go\` for each model id the new CLI's aliases resolve to that the table lacks

Release notes: https://github.com/anthropics/claude-code/blob/main/CHANGELOG.md

### Where it lives

\`docker/workstation/\`, \`internal/claude/\`, \`cmd/fakeclaude/\` and \`internal/loop/models.go\`. Elliot.

### Out of scope

Shipping it: the bump lands on \`main\` and reaches the fleet with the next release.

$marker

— Elliot · DevSecOps (filed by \`scripts/claude-code-watch.sh\`)
EOF

# Outside Actions, the repository the checkout's gh resolves to.
if ! repo=${GITHUB_REPOSITORY:-$(gh repo view --json nameWithOwner --jq .nameWithOwner)} || [ -z "$repo" ]; then
	echo "claude-code-watch: no repository: set GITHUB_REPOSITORY" >&2
	exit 1
fi

# number, state and asked-for version of every issue this script filed.
# The repository is public, so anyone can open an issue holding the marker;
# only one this workflow or a maintainer wrote counts, or a stranger's
# issue could silence the ask, or be rewritten by it (#1456, #1233).
if ! gh api --paginate "repos/$repo/issues?state=all&per_page=100" \
	--jq '.[] | select(.pull_request | not)
		| select(.user.login == "github-actions[bot]"
			or (.author_association | IN("OWNER", "MEMBER", "COLLABORATOR")))
		| (.body // "") as $b
		| ($b | capture("<!-- claude-code-watch (?<v>[0-9.]+) -->")) as $m
		| "\(.number)\t\(.state)\t\($m.v)"' >"$work/filed"; then
	echo "claude-code-watch: could not list the issues" >&2
	exit 1
fi

if awk -F'\t' -v v="$latest" '$3 == v {found = 1} END {exit !found}' "$work/filed"; then
	echo "claude-code-watch: $latest is already asked for ($(awk -F'\t' -v v="$latest" '$3 == v {print "#" $1 " " $2}' "$work/filed" | head -n1)); nothing to file."
	exit 0
fi

open=$(awk -F'\t' '$2 == "open" {print $1}' "$work/filed" | head -n1)
if [ -n "$open" ]; then
	echo "claude-code-watch: updating #$open to ask for $latest (pin $pin)."
	[ "$dry_run" = true ] && { cat "$work/body"; exit 0; }
	gh issue edit "$open" -R "$repo" --title "$title" --body-file "$work/body" || exit 1
else
	echo "claude-code-watch: filing: $title"
	[ "$dry_run" = true ] && { cat "$work/body"; exit 0; }
	gh issue create -R "$repo" --title "$title" --label chore --body-file "$work/body" || exit 1
fi
