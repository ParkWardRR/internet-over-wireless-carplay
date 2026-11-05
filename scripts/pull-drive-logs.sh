#!/bin/sh
# Copy CarNet Probe's drive logs (Documents/drive-*.jsonl) from the iPhone
# into secrets/drive-logs/. They hold your position: never commit them.
#
#   scripts/pull-drive-logs.sh [device-udid]   # default: the first paired physical iPhone
set -eu
HERE=$(cd "$(dirname "$0")/.." && pwd)
UDID=${1:-$(xcrun devicectl list devices 2>/dev/null | awk '/physical/ && /iPhone/ {for (i=1;i<=NF;i++) if ($i ~ /^[0-9A-F]{8}-[0-9A-F]{16}$/) {print $i; exit}}')}
[ -n "$UDID" ] || { echo "no paired iPhone found" >&2; exit 1; }
OUT="$HERE/secrets/drive-logs"
mkdir -p "$OUT"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
xcrun devicectl device copy from --device "$UDID" --domain-type appDataContainer \
	--domain-identifier app.carplaywan.probe --source Documents --destination "$TMP" >/dev/null
n=0
for f in $(find "$TMP" -name 'drive-*.jsonl'); do
	cp "$f" "$OUT/" && n=$((n + 1))
done
echo "$n drive log(s) in $OUT"
ls -la "$OUT" | tail -n +2
