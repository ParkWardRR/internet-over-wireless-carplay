#!/bin/sh
# Experiment A of docs/ap-signalling.md: can we control what an AP advertises,
# and prove it from the air rather than from a config file?
#
#   scripts/ap-signal-test.sh trials            # what is runnable
#   scripts/ap-signal-test.sh config A          # show the composed hostapd config
#   sudo scripts/ap-signal-test.sh run A [secs] # AP up, capture, verify, AP down
#   scripts/ap-signal-test.sh verify <pcap> A   # re-verify a capture on its own
#   sudo scripts/ap-signal-test.sh down         # clean up after an aborted run
#
# Needs two radios: AP_IFACE runs hostapd, MON_IFACE captures in monitor mode.
# They must be different devices — a single radio cannot transmit a beacon and
# capture it. The point of the second radio is that this script never reports
# what it configured, only what ieaudit found in the capture.
#
# Config: secrets/ap-test.conf (from test-ap/hostapd-base.conf.example, with a
# real wpa_passphrase). Captures go to captures/, which is not tracked.
#
# This runs no part of carplayd and joins no car. It refuses to use a radio
# that is currently associated to anything, so it cannot take a car's radio out
# from under the daemon mid-drive.
set -eu
PATH=$PATH:/usr/sbin:/sbin

HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/.." && pwd)
BASE=${AP_TEST_CONF:-$ROOT/secrets/ap-test.conf}
TRIALS=$ROOT/test-ap/trials
CAPDIR=${CAPTURE_DIR:-$ROOT/captures}
STATE=${AP_TEST_STATE:-/run/ap-signal-test}
AP_IFACE=${AP_IFACE:-ap0}
MON_IFACE=${MON_IFACE:-mon0}
SECS=${SECS:-90}

die() { echo "error: $*" >&2; exit 1; }
say() { echo "ap-signal-test: $*"; }
need_root() { [ "$(id -u)" -eq 0 ] || die "run with sudo"; }

trial_file() {
	f=$TRIALS/$1-*.conf
	# shellcheck disable=SC2086
	set -- $f
	[ -f "$1" ] || die "no runnable trial '$1' (see 'trials'; C and D are blocked on purpose)"
	echo "$1"
}

cmd_trials() {
	echo "runnable:"
	for f in "$TRIALS"/*.conf; do
		[ -f "$f" ] || continue
		n=$(basename "$f" .conf)
		echo "  ${n%%-*}  $(head -1 "$f" | sed 's/^# *//')"
	done
	for f in "$TRIALS"/*.blocked; do
		[ -f "$f" ] || continue
		echo "blocked: $(basename "$f") — see the file and docs/ap-signalling.md gate 3"
	done
}

# compose writes base + trial delta to $STATE/hostapd.conf.
compose() {
	t=$(trial_file "$1")
	[ -f "$BASE" ] || die "missing $BASE — copy test-ap/hostapd-base.conf.example and set wpa_passphrase"
	grep -q '^wpa_passphrase=CHANGE-ME' "$BASE" && die "$BASE still has the placeholder passphrase"
	grep -qE '^(interworking|internet|access_network_type|vendor_elements)=' "$BASE" &&
		die "$BASE sets an advertisement option; those belong in the trial file, not the constant baseline"
	mkdir -p "$STATE"
	chmod 700 "$STATE"
	umask 077
	{
		sed "s|^interface=.*|interface=$AP_IFACE|" "$BASE"
		echo
		echo "# --- trial $1: $(basename "$t") ---"
		cat "$t"
	} >"$STATE/hostapd.conf"
	echo "$STATE/hostapd.conf"
}

cmd_config() {
	f=$(compose "$1")
	sed 's/^wpa_passphrase=.*/wpa_passphrase=<redacted>/' "$f"
}

ap_channel() { sed -n 's/^channel=\([0-9]*\).*/\1/p' "$STATE/hostapd.conf" | head -1; }
ap_bssid() { cat "/sys/class/net/$AP_IFACE/address"; }

ieaudit_bin() {
	if [ -n "${IEAUDIT:-}" ]; then echo "$IEAUDIT"; return; fi
	if [ -x "$STATE/ieaudit" ]; then echo "$STATE/ieaudit"; return; fi
	command -v go >/dev/null || die "need Go to build ieaudit, or set IEAUDIT=<path to a built binary>"
	mkdir -p "$STATE"
	(cd "$ROOT/recon/ieaudit" && go build -o "$STATE/ieaudit" .) >&2
	echo "$STATE/ieaudit"
}

# refuse a radio that is doing something else; a car link must not be disturbed.
check_idle() {
	iface=$1
	[ -e "/sys/class/net/$iface" ] || die "no interface $iface (set AP_IFACE / MON_IFACE)"
	if iw dev "$iface" link 2>/dev/null | grep -q '^Connected to'; then
		die "$iface is associated to a network; stop whatever owns it (carplayd?) or pick another radio"
	fi
	[ "$AP_IFACE" != "$MON_IFACE" ] || die "AP_IFACE and MON_IFACE must be different radios"
}

cmd_down() {
	need_root
	[ -f "$STATE/hostapd.pid" ] && kill "$(cat "$STATE/hostapd.pid")" 2>/dev/null || true
	[ -f "$STATE/tcpdump.pid" ] && kill "$(cat "$STATE/tcpdump.pid")" 2>/dev/null || true
	rm -f "$STATE/hostapd.pid" "$STATE/tcpdump.pid"
	ip link set "$MON_IFACE" down 2>/dev/null || true
	say "stopped"
}

cmd_run() {
	need_root
	trial=$1
	[ $# -ge 2 ] && SECS=$2
	conf=$(compose "$trial")
	check_idle "$AP_IFACE"
	check_idle "$MON_IFACE"
	ch=$(ap_channel)
	[ -n "$ch" ] || die "no channel= in the baseline config"
	mkdir -p "$CAPDIR"
	chmod 700 "$CAPDIR"
	stamp=$(date -u +%Y%m%dT%H%M%SZ)
	pcap=$CAPDIR/trial-$trial-$stamp.pcap
	log=$CAPDIR/trial-$trial-$stamp.hostapd.log
	trap 'cmd_down >/dev/null 2>&1 || true' EXIT INT TERM

	say "monitor radio $MON_IFACE to channel $ch"
	ip link set "$MON_IFACE" down
	iw dev "$MON_IFACE" set type monitor
	ip link set "$MON_IFACE" up
	iw dev "$MON_IFACE" set channel "$ch"

	# Snap length well past a management frame: a cut beacon looks exactly
	# like an AP that stopped sending the element, and ieaudit flags it.
	tcpdump -i "$MON_IFACE" -s 0 -U -w "$pcap" \
		'type mgt and (subtype beacon or subtype probe-resp)' >/dev/null 2>&1 &
	echo $! >"$STATE/tcpdump.pid"

	say "hostapd, trial $trial"
	hostapd -B -P "$STATE/hostapd.pid" -f "$log" "$conf" || {
		echo "--- hostapd log ---" >&2
		tail -20 "$log" >&2 2>/dev/null || true
		die "hostapd refused to start. 'unknown configuration item' for interworking/internet means this build lacks CONFIG_INTERWORKING=y"
	}
	grep -q 'unknown configuration item' "$log" 2>/dev/null &&
		die "hostapd ignored an advertisement option; see $log"

	say "capturing for ${SECS}s on channel $ch, BSSID $(ap_bssid | sed 's/\(..:..:..\):.*/\1:xx:xx:xx/')"
	say "to get probe responses, scan now: toggle the phone's Wi-Fi, or 'iw dev <other> scan' from a third host"
	sleep "$SECS"

	cmd_down >/dev/null
	trap - EXIT INT TERM
	say "capture: $pcap"
	verify "$pcap" "$trial"
}

want_internet() {
	t=$(trial_file "$1")
	v=$(sed -n 's/^internet=\([01]\).*/\1/p' "$t" | head -1)
	[ -n "$v" ] || die "$t has no internet= line"
	echo "$v"
}

verify() {
	pcap=$1
	trial=$2
	want=$(want_internet "$trial")
	bin=$(ieaudit_bin)
	bssid=""
	[ -e "/sys/class/net/$AP_IFACE/address" ] && bssid=$(ap_bssid)
	say "verifying trial $trial (expect internet=$want)"
	if [ -n "$bssid" ]; then
		"$bin" check "$pcap" --internet "$want" --bssid "$bssid"
	else
		"$bin" check "$pcap" --internet "$want" --ssid "$(sed -n 's/^ssid=//p' "$BASE" | head -1)"
	fi
}

cmd_verify() {
	[ $# -eq 2 ] || die "usage: verify <pcap> <trial>"
	compose "$2" >/dev/null
	verify "$1" "$2"
}

case "${1:-}" in
trials) cmd_trials ;;
config) shift; [ $# -eq 1 ] || die "usage: config <trial>"; cmd_config "$1" ;;
run) shift; [ $# -ge 1 ] || die "usage: run <trial> [seconds]"; cmd_run "$@" ;;
verify) shift; cmd_verify "$@" ;;
down) cmd_down ;;
*)
	sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
	exit 1
	;;
esac
