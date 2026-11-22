#!/usr/bin/env bash
# Records the docs site's home clip (#1693): record.mjs's `home` take, cut
# from its ready mark to its end mark, cropped to the team card and encoded
# for the web into site/static/media/, with a poster frame beside it.
#
#   bash scripts/site-video.sh        (make site-video; needs make build)
#
# The clip shows at up to 820 CSS pixels, so it is recorded at device pixel
# ratio 2 to stay sharp on a high-density screen; SITE_VIDEO_DPR=1 records a
# quick draft. A 2x capture runs at a fraction of a 1x one's frame rate, and
# capture rate is what makes a clip smooth or laggy. So the take plays
# SITE_VIDEO_SLOW times slower than life (default 5), and the encode plays it
# back that much faster: five times the frames of every move.
set -euo pipefail
cd "$(dirname "$0")/.."

out=site/static/media
marks=web/demo/public/marks.json
mkdir -p web/demo/public "$out"
# record.mjs merges a DEMO_TAKES run into the marks already there
[ -f "$marks" ] || echo '{}' >"$marks"

(cd web/demo && npm install --silent)
dpr=${SITE_VIDEO_DPR:-2}
slow=${SITE_VIDEO_SLOW:-5}
DEMO_TAKES=home DEMO_DPR=$dpr DEMO_SLOW=$slow bash scripts/fixture-hub.sh node demo/record.mjs

# the compositor's own ffmpeg, the one `npm run render` uses
ff=$(ls -d web/demo/node_modules/@remotion/compositor-*/ | head -1)
ffmpeg() { LD_LIBRARY_PATH="$ff" DYLD_LIBRARY_PATH="$ff" "$ff/ffmpeg" -loglevel error -y "$@"; }
# the marks are the slowed take's seconds; this ffmpeg has no setpts filter,
# so -itsscale speeds the input up and the cut is made on its scaled clock
read -r from to < <(node -e '
  const m = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).home
  const n = Number(process.argv[2])
  console.log(m.ready / n, m.end / n)' "$marks" "$slow")

ffmpeg -itsscale "$(node -e 'console.log(1 / process.argv[1])' "$slow")" \
	-i web/demo/public/home.mp4 -ss "$from" -to "$to" -an -r 30 \
	-vf crop=$((820 * dpr)):$((520 * dpr)):$((100 * dpr)):$((60 * dpr)) \
	-c:v libx264 -preset slow -crf 24 -pix_fmt yuv420p -movflags +faststart \
	"$out/team-at-work.mp4"
ffmpeg -ss 0.3 -i "$out/team-at-work.mp4" -frames:v 1 -q:v 3 "$out/team-at-work.jpg"
ls -l "$out"/team-at-work.*
