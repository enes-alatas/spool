#!/usr/bin/env bash
# Fails when Spool's built docs site would load anything from another
# origin (ADR-0071): a script, a stylesheet, an image, a font, a frame or
# a fetch. A link a reader follows is not a load, and neither is a URL in
# text.
#
#   scripts/site-origins.sh DIR [ORIGIN]
#
# DIR is the built site. ORIGIN is the site's own, which may load from
# itself; by default the scheme and host of site/hugo.yaml's baseURL.
# What counts as a load:
#   HTML and SVG  src, srcset, poster, data and xlink:href attributes; a
#                 plain href on SVG's <image>, <use>, <feImage> and
#                 <script>; and a <link> whose rel fetches (anything but
#                 canonical, alternate, author, license, me, next and prev)
#   CSS           url(...) and @import, in .css files and in HTML
#   JS            import(...), import ... from, a bare import "...",
#                 importScripts(...), fetch(...), new Worker(...),
#                 new WebSocket(...), new EventSource(...) and
#                 sendBeacon(...)
# ws:// and wss:// count as well as http(s):// and //.
# Not counted: an attribute with spaces around its =, a tag split across
# lines (the build is --minify, which writes neither), and a URL put
# together at run time rather than written as one string.
# Each hit is printed as FILE: URL, and the exit is 1 if there is any.
set -euo pipefail

dir=${1:?usage: site-origins.sh DIR [ORIGIN]}
origin=${2:-$(sed -n 's|^baseURL: *\(https\{0,1\}://[^/]*\).*|\1|p' "$(git rev-parse --show-toplevel)/site/hugo.yaml")}
[ -d "$dir" ] || { echo "site-origins: no directory $dir" >&2; exit 2; }

# loads FILE prints every absolute URL FILE loads, one per line.
loads() {
	local file=$1
	case $file in
	*.html | *.htm | *.svg | *.xml)
		grep -oE '<[a-zA-Z][^>]*>' "$file" | grep -oiE '[[:space:]](src|srcset|poster|data|xlink:href)=("[^"]*"|'"'"'[^'"'"']*'"'"'|[^[:space:]>]+)' |
			sed -E 's/^[[:space:]<]*[^=]*=//; s/^["'"'"']//; s/["'"'"']$//' | tr ',' '\n' | sed -E 's/^[[:space:]]+//; s/[[:space:]].*//'
		grep -oiE '<(image|use|feimage|script)[[:space:]][^>]*>' "$file" | grep -oiE '[[:space:]]href=("[^"]*"|'"'"'[^'"'"']*'"'"'|[^[:space:]>]+)' |
			sed -E 's/^[[:space:]]*href=//I; s/["'"'"']//g'
		grep -oiE '<link[^>]*>' "$file" | while IFS= read -r tag; do
			rel=$(printf '%s' "$tag" | grep -oiE 'rel=("[^"]*"|'"'"'[^'"'"']*'"'"'|[^[:space:]>]+)' | sed -E 's/^rel=//I; s/["'"'"']//g' | tr '[:upper:]' '[:lower:]')
			case " $rel " in
			" canonical " | " alternate " | " author " | " license " | " me " | " next " | " prev ") continue ;;
			esac
			printf '%s' "$tag" | grep -oiE 'href=("[^"]*"|'"'"'[^'"'"']*'"'"'|[^[:space:]>]+)' | sed -E 's/^href=//I; s/["'"'"']//g'
		done
		;;
	esac
	case $file in
	*.html | *.htm | *.css | *.svg)
		grep -oiE 'url\([[:space:]]*["'"'"']?[^)"'"'"']+' "$file" | sed -E 's/^url\([[:space:]]*["'"'"']?//I'
		grep -oiE '@import[[:space:]]+(url\()?["'"'"']?[^"'"'"'); ]+' "$file" | sed -E 's/^@import[[:space:]]+(url\()?["'"'"']?//I'
		;;
	esac
	case $file in
	*.html | *.htm | *.js | *.mjs)
		grep -oE '(import[[:space:]]*\(?|from[[:space:]]*|importScripts[[:space:]]*\(|fetch[[:space:]]*\(|new[[:space:]]+(Worker|WebSocket|EventSource)[[:space:]]*\(|sendBeacon[[:space:]]*\()[[:space:]]*["'"'"'`][^"'"'"'`]+' "$file" |
			sed -E 's/^[^"'"'"'`]*["'"'"'`]//'
		;;
	esac
}

hits=0
while IFS= read -r -d '' file; do
	while IFS= read -r url; do
		case $url in
		https://* | http://* | wss://* | ws://* | //*) ;;
		*) continue ;;
		esac
		case $url in "$origin" | "$origin"/*) continue ;; esac
		printf '%s: %s\n' "${file#"$dir"/}" "$url"
		hits=$((hits + 1))
	done < <(loads "$file" || true)
done < <(find "$dir" -type f -print0)

if [ "$hits" -gt 0 ]; then
	echo "site-origins: $hits load(s) from another origin than $origin (ADR-0071)" >&2
	exit 1
fi
echo "site-origins: nothing in $dir loads from another origin than $origin"
