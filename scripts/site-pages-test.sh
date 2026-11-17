#!/usr/bin/env bash
# Exercises scripts/site-pages.sh (ADR-0071 item 4) the way the site
# workflow runs it, in a clone of this checkout's site, so no tag it makes
# reaches this repository: no release, a latest release carrying no site/,
# and one carrying it.
set -uo pipefail

cd "$(git rev-parse --show-toplevel)"
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }
has() { if grep -qF -- "$3" "$2"; then pass "$1"; else fail "$1 (no '$3' in ${2#"$work"/})"; fi; }
lacks() { if grep -qF -- "$3" "$2"; then fail "$1 ('$3' in ${2#"$work"/})"; else pass "$1"; fi; }

# The clone takes the working tree's site and scripts, so what is tested is
# what is on disk, committed or not; the Hugo download is shared.
repo=$work/repo
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.invalid GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.invalid
git clone --quiet --shared . "$repo"
rm -rf "$repo/site" && cp -R site "$repo/site" && rm -rf "$repo/site/public"
cp scripts/site-*.sh "$repo/scripts/"
mkdir -p .cache && ln -s "$PWD/.cache" "$repo/.cache"
git -C "$repo" add -A site scripts
git -C "$repo" commit --quiet --allow-empty -m site
git -C "$repo" tag v0.0.1 \
	"$(git -C "$repo" commit-tree -m 'no site' "$(git -C "$repo" mktree < /dev/null)")"
git -C "$repo" tag v0.0.2 HEAD

# pages NAME TAG... builds both trees as the workflow does, into $work/NAME.
pages() {
	local name=$1
	shift
	if (cd "$repo" && bash scripts/site-pages.sh root "$work/$name" "$@" && bash scripts/site-pages.sh next "$work/$name" "$@") > "$work/$name.log" 2>&1; then
		pass "$name: both trees build"
	else
		fail "$name: both trees build"
		sed 's/^/    /' "$work/$name.log" | tail -20
	fi
}

banner='which is not released yet'
gh=https://github.com/enes-alatas/spool/tree

pages none
has 'none: the root carries the banner' "$work/none/index.html" "$banner"
has 'none: /next/ carries the banner' "$work/none/next/index.html" "$banner"
has 'none: /next/ is served under /next/' "$work/none/next/index.html" 'href=/spool/next/'
has 'none: the menu offers next' "$work/none/index.html" '>next (main)<'

pages unsited v0.0.1 v0.0.0
has 'unsited: the root is main, with the banner' "$work/unsited/index.html" "$banner"
has 'unsited: the latest links to its files' "$work/unsited/index.html" "$gh/v0.0.1"
has 'unsited: an older release links to its files' "$work/unsited/next/index.html" "$gh/v0.0.0"

pages sited v0.0.2 v0.0.1
has 'sited: the log names the release' "$work/sited.log" "the root is v0.0.2's own site/"
lacks 'sited: the root has no banner' "$work/sited/index.html" "$banner"
has 'sited: the root is the latest in the menu' "$work/sited/index.html" '>v0.0.2 (latest)<'
has '/next/ points to the release' "$work/sited/next/index.html" 'The v0.0.2 docs'
has 'sited: an older release links to its files' "$work/sited/index.html" "$gh/v0.0.1"
if [ "$(git -C "$repo" worktree list | wc -l)" -eq 1 ]; then pass 'sited: no release worktree is left'; else fail 'sited: no release worktree is left'; fi

if (cd "$repo" && bash scripts/site-pages.sh root "$work/bad" 'v1;id') > /dev/null 2>&1; then fail 'a tag that is no release tag is refused'; else pass 'a tag that is no release tag is refused'; fi
if (cd "$repo" && bash scripts/site-pages.sh root relative) > /dev/null 2>&1; then fail 'a relative OUT is refused'; else pass 'a relative OUT is refused'; fi

if [ "$failures" -gt 0 ]; then
	printf '\n%s check(s) failed\n' "$failures"
	exit 1
fi
printf '\nall checks passed\n'
