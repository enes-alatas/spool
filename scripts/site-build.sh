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
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
out=${1:-site/public}
pin=site/hugo.pin

pinned() { sed -n "s/^$1=//p" "$pin"; }
version=$(pinned version)
[ -n "$version" ] || { echo "site-build: no version in $pin" >&2; exit 2; }

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $(uname -m) in x86_64 | amd64) arch=amd64 ;; aarch64 | arm64) arch=arm64 ;; *) arch=$(uname -m) ;; esac
want=$(pinned "$os-$arch")

if [ -n "$want" ]; then
	hugo=.cache/hugo/$version/hugo
	if [ ! -x "$hugo" ]; then
		tmp=$(mktemp -d)
		trap 'rm -rf "$tmp"' EXIT
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
case $out in /*) dest=$out ;; *) dest=$PWD/$out ;; esac
"$hugo" --source site --destination "$dest" --cleanDestinationDir --minify --environment production
bash scripts/site-origins.sh "$out"
