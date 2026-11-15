#!/usr/bin/env bash
# Builds one of the two trees GitHub Pages serves (ADR-0071 item 4) into
# OUT, which must be absolute. The site workflow runs it once for each, as
# separate steps, so a red run names the tree that failed.
#
#   scripts/site-pages.sh root OUT [LATEST [OLDER...]]
#   scripts/site-pages.sh next OUT [LATEST [OLDER...]]
#
# LATEST is the latest release's tag and OLDER the tags of the releases
# before it, newest first; give none while there is no release.
#   root  LATEST's own tree, built by its own scripts/site-build.sh with its
#         own Hugo pin, when that tree carries site/. Otherwise this
#         checkout, with the unreleased banner.
#   next  this checkout, served under /next/, with the unreleased banner.
# root empties OUT first, so it builds before next. Both trees carry the
# same Versions menu: the latest release, next, and each older release's
# files on GitHub.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
usage='usage: site-pages.sh root|next OUT [LATEST [OLDER...]]'
tree=${1:?$usage}
out=${2:?$usage}
shift 2
case $tree in root | next) ;; *) echo "$usage" >&2; exit 2 ;; esac
case $out in /*) ;; *) echo "site-pages: OUT must be absolute, not $out" >&2; exit 2 ;; esac
for tag in "$@"; do
	[[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "site-pages: $tag is not a release tag" >&2; exit 2; }
done
latest=${1:-}
[ $# -eq 0 ] || shift

base=$(sed -n 's|^baseURL: *||p' site/hugo.yaml)
repo=${SITE_REPO_URL:-https://github.com/enes-alatas/spool}
tmp=$(mktemp -d)
cleanup() {
	[ ! -d "$tmp/release" ] || git worktree remove --force "$tmp/release"
	rm -rf "$tmp"
}
trap cleanup EXIT

# released: LATEST's tree carries site/, so the root is that release's own.
released=
if [ -n "$latest" ]; then
	git rev-parse -q --verify "refs/tags/$latest^{commit}" > /dev/null ||
		git fetch --quiet --no-tags --depth=1 origin "+refs/tags/$latest:refs/tags/$latest"
	! git cat-file -e "$latest:site/hugo.yaml" 2> /dev/null || released=yes
fi
{
	if [ -n "$released" ]; then echo "$latest (latest) $base"; elif [ -n "$latest" ]; then echo "$latest (latest) $repo/tree/$latest"; fi
	echo "next (main) ${base}next/"
	for tag in "$@"; do echo "$tag $repo/tree/$tag"; done
} > "$tmp/versions"
banner="This describes main, which is not released yet: it may not match the release you run."

case $tree in
root)
	if [ -n "$released" ]; then
		echo "site-pages: the root is $latest's own site/"
		git worktree add --quiet --detach "$tmp/release" "$latest"
		(cd "$tmp/release" && SITE_VERSIONS=$tmp/versions bash scripts/site-build.sh "$out")
	else
		echo "site-pages: no release carries site/ yet; the root is this checkout, with the banner"
		SITE_VERSIONS=$tmp/versions SITE_BANNER=$banner bash scripts/site-build.sh "$out"
	fi
	;;
next)
	[ -z "$released" ] || banner="$banner [The $latest docs]($base) describe the latest release."
	SITE_VERSIONS=$tmp/versions SITE_BANNER=$banner SITE_BASEURL=${base}next/ bash scripts/site-build.sh "$out/next"
	;;
esac
