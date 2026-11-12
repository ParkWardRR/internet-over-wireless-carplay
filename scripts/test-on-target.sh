#!/bin/sh
# Run the test suites on an arm64 target (the recorder Pi, the garage box),
# instead of only on the build host.
#
#   scripts/test-on-target.sh                   all suites on $TARGET
#   scripts/test-on-target.sh carplayd          one suite
#   TARGET=user@host scripts/test-on-target.sh
#
# The suites passing on an amd64 build host do not say much about the box: it is
# arm64, on a different kernel, with the real nl80211 and `iw` behind the code
# paths that shell out. The test binaries are cross-compiled on the build host
# (scripts/release.sh testbin) and copied over; nothing is built on the target,
# which has no Go toolchain and no business getting one.
#
# Tests must not read the source tree -- there is none on the target. The
# example-config test embeds its fixture for exactly this reason.
set -eu
cd "$(dirname "$0")/.."
TARGET=${TARGET:-user@recon-pi.local}
BUILD=${BUILD:-user@build-host.local}
REMOTE=${REMOTE:-builds/bmw-f32-carplay-wan}
WANT=${1:-all}
DEST=$(ssh "$TARGET" 'mktemp -d')

cleanup() { ssh "$TARGET" "rm -rf '$DEST'" >/dev/null 2>&1 || true; }
trap cleanup EXIT

rsync -a --delete --exclude .git/ --exclude secrets/ --exclude out/ ./ "$BUILD:$REMOTE/"
ssh "$BUILD" "cd $REMOTE && scripts/release.sh testbin"

fail=0
for name in carplayd carrecon carnetbridge; do
	[ "$WANT" = all ] || [ "$WANT" = "$name" ] || continue
	bin=$name.test-linux-arm64
	ssh "$BUILD" "cat $REMOTE/out/$bin" | ssh "$TARGET" "cat > '$DEST/$bin' && chmod +x '$DEST/$bin'"
	echo "===== $name on $TARGET ====="
	# -test.count=1 defeats the result cache, which would otherwise make a
	# second run on the target prove nothing. The output goes to a file first:
	# piping ssh into tail would report tail's exit status, so every suite would
	# look like it passed.
	log=$(mktemp)
	if ssh "$TARGET" "cd '$DEST' && ./$bin -test.count=1 ${TESTFLAGS:-}" > "$log" 2>&1; then
		tail -"${TAIL:-4}" "$log"
	else
		fail=1
		grep -E "^(---|\s+---) FAIL|^FAIL|panic:" "$log" | head -20 || tail -20 "$log"
		echo "  FAILED on target (full output: $log)"
		log=
	fi
	[ -n "$log" ] && rm -f "$log"
done
[ "$fail" = 0 ] && echo "all suites passed on $TARGET"
exit "$fail"
