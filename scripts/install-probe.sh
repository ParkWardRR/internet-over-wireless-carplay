#!/bin/sh
# Build CarNet Probe (ios/CarNetProbe) and install it on the iPhone.
# iOS builds run on the Mac (Xcode), not on build-host.
#
#   scripts/install-probe.sh [device-udid]   # default: the first paired physical iPhone
#
# Then on the phone: open CarNet Probe once and allow Local Network and Location.
set -eu

HERE=$(cd "$(dirname "$0")/.." && pwd)
DD=/tmp/carnetprobe-dd
APP=$DD/Build/Products/Debug-iphoneos/CarNetProbe.app

die() { echo "error: $*" >&2; exit 1; }
command -v xcodegen >/dev/null || die "needs xcodegen (brew install xcodegen)"

UDID=${1:-$(xcrun devicectl list devices 2>/dev/null | awk '/physical/ && /iPhone/ {for (i=1;i<=NF;i++) if ($i ~ /^[0-9A-F]{8}-[0-9A-F]{16}$/) {print $i; exit}}')}
[ -n "$UDID" ] || die "no paired iPhone found (xcrun devicectl list devices)"

cd "$HERE/ios/CarNetProbe"
xcodegen -q
xcodebuild -project CarNetProbe.xcodeproj -scheme CarNetProbe -destination 'generic/platform=iOS' \
	-derivedDataPath "$DD" -allowProvisioningUpdates -quiet build
xcrun devicectl device install app --device "$UDID" "$APP" \
	|| die "install failed: unlock the iPhone and keep it on the same Wi-Fi, or plug it in"
echo "Installed on $UDID. Auto-run probes: xcrun devicectl device process launch --device $UDID app.carplaywan.probe -runProbes"
