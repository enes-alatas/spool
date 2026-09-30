#!/usr/bin/env bash
# Renders the demo in chunks of 260 frames with two browsers each, then
# joins them. A full-parallel render in one piece runs a browser per core
# and died on a 12 GB host (#447). A rerun after a crash skips the chunks
# already on disk and resumes rather than starting over.
set -euo pipefail
cd "$(dirname "$0")"

# The composition's length, read from its own definition so a re-cut
# needs no edit here.
total=$(npx remotion compositions src/index.ts 2>/dev/null | awk '$1 == "SpoolDemo" { print $4 }')
if [ -z "$total" ]; then
	echo "render.sh: could not read the SpoolDemo composition's length" >&2
	exit 1
fi
step=260
mkdir -p out/chunks
# Chunks are reused only for the inputs they were rendered from: a new
# recording, an edited composition or a Remotion bump starts the set over.
inputs=$(find src public package-lock.json -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | cut -d' ' -f1)
if [ "$(cat out/chunks/.inputs 2>/dev/null)" != "$inputs" ]; then
	rm -f out/chunks/*.mp4
	echo "$inputs" >out/chunks/.inputs
fi
for ((first = 0; first < total; first += step)); do
	last=$((first + step - 1))
	((last >= total)) && last=$((total - 1))
	chunk=out/chunks/$(printf %05d "$first").mp4
	[ -s "$chunk" ] && continue
	npx remotion render src/index.ts SpoolDemo "$chunk.part.mp4" --frames="$first-$last" --concurrency=2
	mv "$chunk.part.mp4" "$chunk"
done

# The chunks share one encoder setup, so they join without re-encoding.
(cd out/chunks && ls [0-9][0-9][0-9][0-9][0-9].mp4 | sed "s/.*/file '&'/") >out/chunks/list.txt
ffmpeg=$(ls node_modules/@remotion/compositor-*/ffmpeg | head -1)
"$ffmpeg" -v error -y -f concat -safe 0 -i out/chunks/list.txt -c copy out/spool-demo.mp4
echo "out/spool-demo.mp4"
