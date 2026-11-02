#!/bin/sh
# Build car-ie.swift into a tiny app bundle (macOS only shows raw Wi-Fi IEs to
# apps with Location access) and run it. Output goes to stdout.
#
#   scripts/car-ie.sh [--all]
#
# First run: allow Location for "CarIE" when macOS asks.
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
APP=${TMPDIR:-/tmp}/CarIE.app
OUT=$(mktemp)

mkdir -p "$APP/Contents/MacOS"
cat > "$APP/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
	<key>CFBundleIdentifier</key><string>app.carplaywan.carie</string>
	<key>CFBundleName</key><string>CarIE</string>
	<key>CFBundleExecutable</key><string>CarIE</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>NSLocationUsageDescription</key><string>macOS only shows Wi-Fi beacon details to apps with Location access.</string>
	<key>NSLocationWhenInUseUsageDescription</key><string>macOS only shows Wi-Fi beacon details to apps with Location access.</string>
</dict></plist>
EOF
if [ ! -x "$APP/Contents/MacOS/CarIE" ] || [ "$HERE/car-ie.swift" -nt "$APP/Contents/MacOS/CarIE" ]; then
	swiftc -O -o "$APP/Contents/MacOS/CarIE" "$HERE/car-ie.swift"
	codesign -s - -f "$APP" 2>/dev/null
fi
open -W -n --stdout "$OUT" --stderr "$OUT" "$APP" --args "$@"
cat "$OUT"
rm -f "$OUT"
