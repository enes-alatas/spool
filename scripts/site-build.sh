#!/usr/bin/env bash
# Builds Spool's docs site (ADR-0071) into OUT, with the Hugo site/hugo.pin
# names, then checks the output loads nothing from another origin.
#
#   scripts/site-build.sh [OUT]          (default: site/public)
#
# On linux-amd64 and linux-arm64 the pinned release is downloaded once into
# .cache/hugo/, and its archive is checked against the pin's SHA-256 before
# anything in it runs. Elsewhere, a `hugo` on PATH is used only when it is
# the pinned version.
#
# Three variables, all optional, are how scripts/site-pages.sh builds the
# published trees; a release's tree is built by its own copy of this script,
# so they stay as they are:
#   SITE_BASEURL   the address the tree is served from (default: hugo.yaml's)
#   SITE_BANNER    Markdown every page carries in a banner
#   SITE_VERSIONS  a file of "NAME URL" lines, made the navbar's Versions menu
#                  in that order
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
out=${1:-site/public}
pin=site/hugo.pin
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# yaml_string S prints S as a YAML double-quoted string.
yaml_string() {
	local s=${1//\\/\\\\}
	s=${s//\"/\\\"}
	printf '"%s"' "$s"
}

pinned() { sed -n "s/^$1=//p" "$pin"; }
version=$(pinned version)
[ -n "$version" ] || { echo "site-build: no version in $pin" >&2; exit 2; }

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $(uname -m) in x86_64 | amd64) arch=amd64 ;; aarch64 | arm64) arch=arm64 ;; *) arch=$(uname -m) ;; esac
want=$(pinned "$os-$arch")

if [ -n "$want" ]; then
	hugo=.cache/hugo/$version/hugo
	if [ ! -x "$hugo" ]; then
		archive=hugo_${version}_${os}-${arch}.tar.gz
		curl -fsSL -o "$tmp/$archive" "https://github.com/gohugoio/hugo/releases/download/v$version/$archive"
		got=$(sha256sum "$tmp/$archive" | cut -d' ' -f1)
		if [ "$got" != "$want" ]; then
			echo "site-build: $archive has SHA-256 $got, not the $want $pin names; refusing to run it" >&2
			exit 1
		fi
		tar -xzf "$tmp/$archive" -C "$tmp" hugo
		mkdir -p "$(dirname "$hugo")"
		mv "$tmp/hugo" "$hugo"
	fi
else
	hugo=$(command -v hugo || true)
	if [ -z "$hugo" ] || ! "$hugo" version | grep -q "hugo v$version[-+ ]"; then
		echo "site-build: no pinned download for $os-$arch; put Hugo $version on PATH (found: ${hugo:-none})" >&2
		exit 2
	fi
fi

"$hugo" version
configs=hugo.yaml
if [ -n "${SITE_BANNER:-}" ]; then
	printf 'params:\n  banner:\n    key: unreleased\n    message: %s\n' "$(yaml_string "$SITE_BANNER")" > "$tmp/banner.yaml"
	configs=$configs,$tmp/banner.yaml
fi
menu=menu.yaml
if [ -n "${SITE_VERSIONS:-}" ]; then
	{
		cat site/menu.yaml
		printf '    - name: Versions\n      identifier: versions\n      weight: 3\n'
		weight=0
		while IFS= read -r line; do
			[ -n "$line" ] || continue
			weight=$((weight + 1))
			printf '    - name: %s\n      parent: versions\n      weight: %d\n      url: %s\n' "$(yaml_string "${line% *}")" "$weight" "$(yaml_string "${line##* }")"
		done < "$SITE_VERSIONS"
	} > "$tmp/menu.yaml"
	menu=$tmp/menu.yaml
fi
configs=$configs,$menu

case $out in /*) dest=$out ;; *) dest=$PWD/$out ;; esac
"$hugo" --source site --config "$configs" ${SITE_BASEURL:+--baseURL "$SITE_BASEURL"} \
	--destination "$dest" --cleanDestinationDir --minify --environment production
bash scripts/site-origins.sh "$out"
