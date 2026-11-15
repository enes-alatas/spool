#!/usr/bin/env bash
# Exercises scripts/site-origins.sh (ADR-0071): each kind of load from
# another origin fails the check and is named, and links, text and the
# site's own origin pass.
set -uo pipefail

cd "$(dirname "$0")/.."
failures=0
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
own=https://docs.example

fail() { printf 'FAIL %s\n' "$1"; failures=$((failures + 1)); }
pass() { printf 'ok   %s\n' "$1"; }

# site NAME FILE CONTENT writes a one-file site under $work/NAME.
site() { mkdir -p "$work/$1/$(dirname "$2")"; printf '%s\n' "$3" > "$work/$1/$2"; }
passes() {
	if bash scripts/site-origins.sh "$work/$1" "$own" > "$work/$1.out" 2>&1; then pass "$2"; else fail "$2"; sed 's/^/    /' "$work/$1.out"; fi
}
refuses() { # name label url: exit 1, and the url named
	if bash scripts/site-origins.sh "$work/$1" "$own" > "$work/$1.out" 2>&1; then
		fail "$2"; sed 's/^/    /' "$work/$1.out"
	elif grep -qF -- "$3" "$work/$1.out"; then pass "$2"
	else fail "$2 (exit 1 without naming $3)"; sed 's/^/    /' "$work/$1.out"; fi
}

site clean index.html '<html><head><link rel=canonical href=https://other.example/><link rel=alternate type=application/rss+xml href=https://other.example/feed.xml><link rel=stylesheet href=/spool/main.css><script src="/spool/main.js"></script><script src="https://docs.example/spool/own.js"></script></head><body><a href="https://github.com/enes-alatas/spool">GitHub</a><p>see https://example.com/page and src=https://text.example in prose</p><img src=logo.svg><svg><a href="https://other.example/x"><text>x</text></a></svg></body></html>'
site clean main.css 'body{background:url(/spool/bg.png)} @import "/spool/more.css";'
site clean main.js 'fetch("/spool/en.search-data.json"); const ns="http://www.w3.org/2000/svg";'
passes clean 'links, text, relative and own-origin loads pass'

site script index.html '<script src="https://cdn.example/lib.js"></script>'
refuses script 'a script from a CDN is refused' https://cdn.example/lib.js
site bare index.html '<script defer src=//cdn.example/lib.js></script>'
refuses bare 'an unquoted, scheme-relative src is refused' //cdn.example/lib.js
site srcset index.html '<img srcset="a.png 1x, https://img.example/b.png 2x" src=a.png>'
refuses srcset 'an image in a srcset is refused' https://img.example/b.png
site style index.html '<link rel=stylesheet href=https://fonts.example/css2>'
refuses style 'a stylesheet from another origin is refused' https://fonts.example/css2
site connect index.html "<link rel='preconnect' href='https://fonts.example'>"
refuses connect 'a preconnect is refused' https://fonts.example
site font main.css '@font-face{src:url("https://fonts.example/f.woff2")}'
refuses font 'a font in CSS is refused' https://fonts.example/f.woff2
site import main.css '@import url(https://cdn.example/x.css);'
refuses import 'a CSS @import is refused' https://cdn.example/x.css
site fetch main.js 'fetch("https://api.example/v1").then(r=>r)'
refuses fetch 'a fetch from JS is refused' https://api.example/v1
site dynamic main.js 'import("https://cdn.example/m.mjs")'
refuses dynamic 'a dynamic import is refused' https://cdn.example/m.mjs
site frame page/index.html '<iframe src="https://video.example/embed"></iframe>'
refuses frame 'a frame is refused, in a page below the root' https://video.example/embed
site svgimage pic.svg '<svg xmlns="http://www.w3.org/2000/svg"><image href="https://img.example/a.png"/></svg>'
refuses svgimage 'an SVG 2 <image href> is refused' https://img.example/a.png
site svguse index.html '<svg><use href="https://img.example/i.svg#g"></use></svg>'
refuses svguse 'an SVG <use href> is refused' https://img.example/i.svg#g
site feimage pic.svg "<svg><filter><feImage href='https://img.example/f.png'/></filter></svg>"
refuses feimage 'an SVG <feImage href> is refused' https://img.example/f.png
site svgscript pic.svg '<svg><script href=https://cdn.example/s.js></script></svg>'
refuses svgscript 'an SVG <script href> is refused' https://cdn.example/s.js
site sideeffect index.html '<script type=module>import "https://cdn.example/side.js"</script>'
refuses sideeffect 'a side-effect import is refused' https://cdn.example/side.js
site socket main.js 'new WebSocket("wss://live.example/e")'
refuses socket 'a WebSocket to wss:// is refused' wss://live.example/e
site beacon main.js 'navigator.sendBeacon("https://stats.example/b",d)'
refuses beacon 'a beacon is refused' https://stats.example/b
site events main.js 'new EventSource("https://live.example/s")'
refuses events 'an EventSource is refused' https://live.example/s

if bash scripts/site-origins.sh "$work/missing" "$own" > /dev/null 2>&1; then fail 'a missing directory is an error'; else pass 'a missing directory is an error'; fi

if [ "$failures" -gt 0 ]; then
	printf '\n%s check(s) failed\n' "$failures"
	exit 1
fi
printf '\nall checks passed\n'
