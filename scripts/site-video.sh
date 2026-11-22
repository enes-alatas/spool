#!/usr/bin/env bash
# Records the docs site's clips into site/static/media/, each a take of
# web/demo/record.mjs cut from its ready mark to its end mark, cropped,
# encoded for the web, and given a poster frame beside it:
#   home   team-at-work.mp4   the home page's team at work (#1693)
#   setup  setup.mp4          Quick start's setup page, done in turn (#1698)
#
#   bash scripts/site-video.sh [take…]   (make site-video; needs make build)
#
# With no take named it records both. Name one to retake it alone: each
# clip is committed, and one recorded on a faster machine is worth keeping.
#
# A clip shows at up to its width in CSS pixels, so it is recorded at device
# pixel ratio 2 to stay sharp on a high-density screen; SITE_VIDEO_DPR=1
# records a quick draft. A 2x capture runs at a fraction of a 1x one's frame
# rate, and capture rate is what makes a clip smooth or laggy. So a take
# plays SITE_VIDEO_SLOW times slower than life (default 5), and the encode
# plays it back that much faster: five times the frames of every move.
set -euo pipefail
cd "$(dirname "$0")/.."

# take, file name, the crop in the take's CSS pixels (w:h:x:y, of its
# 1280×720 window), and the clip's width in CSS pixels
clips=(
	"home team-at-work 820:520:100:60 820"
	"setup setup 960:660:160:40 720"
)
takes=("$@")
[ ${#takes[@]} -gt 0 ] || takes=(home setup)
clip_of() {
	local clip
	for clip in "${clips[@]}"; do
		[ "${clip%% *}" = "$1" ] && echo "$clip" && return
	done
	echo "site-video: no clip is made from a take named $1" >&2
	return 1
}
for take in "${takes[@]}"; do clip_of "$take" >/dev/null || exit 2; done

out=site/static/media
marks=web/demo/public/marks.json
mkdir -p web/demo/public "$out"
# record.mjs merges a DEMO_TAKES run into the marks already there
[ -f "$marks" ] || echo '{}' >"$marks"

(cd web/demo && npm install --silent)
dpr=${SITE_VIDEO_DPR:-2}
slow=${SITE_VIDEO_SLOW:-5}
DEMO_TAKES=$(
	IFS=,
	echo "${takes[*]}"
) DEMO_DPR=$dpr DEMO_SLOW=$slow bash scripts/fixture-hub.sh node demo/record.mjs

# the compositor's own ffmpeg, the one `npm run render` uses
ff=$(ls -d web/demo/node_modules/@remotion/compositor-*/ | head -1)
ffmpeg() { LD_LIBRARY_PATH="$ff" DYLD_LIBRARY_PATH="$ff" "$ff/ffmpeg" -loglevel error -y "$@"; }

for take in "${takes[@]}"; do
	read -r _ file crop width <<<"$(clip_of "$take")"
	IFS=: read -r cw ch cx cy <<<"$crop"
	# the marks are the slowed take's seconds; this ffmpeg has no setpts
	# filter, so -itsscale speeds the input up and the cut is made on its
	# scaled clock
	read -r from to < <(node -e '
	  const m = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8"))[process.argv[2]]
	  const n = Number(process.argv[3])
	  console.log(m.ready / n, m.end / n)' "$marks" "$take" "$slow")
	ffmpeg -itsscale "$(node -e 'console.log(1 / process.argv[1])' "$slow")" \
		-i "web/demo/public/$take.mp4" -ss "$from" -to "$to" -an -r 30 \
		-vf "crop=$((cw * dpr)):$((ch * dpr)):$((cx * dpr)):$((cy * dpr)),scale=$((width * dpr)):-2" \
		-c:v libx264 -preset slow -crf 24 -pix_fmt yuv420p -movflags +faststart \
		"$out/$file.mp4"
	ffmpeg -ss 0.3 -i "$out/$file.mp4" -frames:v 1 -q:v 3 "$out/$file.jpg"
	ls -l "$out/$file".*
done
