#!/bin/sh
# Build the site, film it, and encode the README hero animations.
# Runs inside the image built from Containerfile; see the README for the
# podman invocation. Writes to tools/out/.
set -eu

cd /work

echo "== building the site =="
npm ci --no-audit --no-fund
npm run build

cd tools
echo "== installing the renderer =="
npm install --no-audit --no-fund

rm -rf frames out
mkdir -p out

for v in a b; do
	echo "== filming variant $v =="
	node capture.mjs --variant "$v" --out "frames/$v"
done

for v in a b; do
	echo "== encoding variant $v =="

	# GIF: 256 colours is plenty for flat vector art, but a per-frame palette
	# costs size, so use one diff-aware palette for the whole clip. Bayer
	# dithering keeps flat fills flat instead of sprinkling them with noise,
	# which both looks better here and compresses far smaller.
	ffmpeg -y -loglevel error -framerate 20 -i "frames/$v/f%05d.png" \
		-vf "fps=12.5,scale=900:-2:flags=lanczos,split[s0][s1];\
[s0]palettegen=stats_mode=diff:max_colors=192[p];\
[s1][p]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle" \
		-loop 0 "out/hero-$v.gif"

	# WebP: full colour, higher resolution, a fraction of the size.
	ffmpeg -y -loglevel error -framerate 20 -i "frames/$v/f%05d.png" \
		-vf "scale=1100:-2:flags=lanczos" \
		-c:v libwebp_anim -lossless 0 -q:v 72 -compression_level 6 \
		-preset drawing -loop 0 "out/hero-$v.webp"
done

echo "== done =="
ls -lh out
for f in out/*; do
	printf '%s  ' "$f"
	ffprobe -v error -select_streams v:0 -show_entries stream=width,height,nb_frames \
		-of csv=p=0 "$f" || true
done
