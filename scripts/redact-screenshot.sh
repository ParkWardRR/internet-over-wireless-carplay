#!/usr/bin/env bash
# Redact rectangles in a screenshot before it goes in the repo.
#
#   redact-screenshot.sh in.png out.png WxH+X+Y [black:WxH+X+Y ...]
#
# A plain rectangle is downsampled to a few pixels and blown back up, so the
# original pixels are gone rather than merely smeared — a Gaussian blur over
# known text can be undone, a 5-pixel-wide mosaic cannot. Prefix a rectangle
# with `black:` to paint it out instead, which is tidier over black chrome.
set -euo pipefail

if [ "$#" -lt 3 ]; then
  sed -n '2,8p' "$0" >&2
  exit 2
fi

in=$1; out=$2; shift 2
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp "$in" "$tmp/work.png"

for arg in "$@"; do
  geom=${arg#black:}
  wh=${geom%%+*}
  if [ "$arg" != "$geom" ]; then
    read -r x y <<<"$(echo "${geom#*+}" | tr '+' ' ')"
    magick "$tmp/work.png" -fill black -draw \
      "rectangle $x,$y $((x + ${wh%%x*})),$((y + ${wh##*x}))" "$tmp/next.png"
    mv "$tmp/next.png" "$tmp/work.png"
    continue
  fi
  w=${wh%%x*}
  h=${wh##*x}
  magick "$tmp/work.png" \
    \( +clone -crop "$geom" +repage -resize 4% -resize "${w}x${h}!" \) \
    -geometry "+${geom#*+}" -composite "$tmp/next.png"
  mv "$tmp/next.png" "$tmp/work.png"
done

magick "$tmp/work.png" -strip "$out"
echo "wrote $out"
