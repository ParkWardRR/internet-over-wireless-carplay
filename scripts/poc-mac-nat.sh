#!/bin/sh
# P2 proof of concept: the MacBook joins the car's CarPlay Wi-Fi as a second
# client and offers the iPhone an IPv6 route to the internet.
#
# iOS shows no IP/router settings for CarPlay networks, and the car's DHCP
# hands out no router. So the route comes from IPv6 instead: the Mac sends
# router advertisements (rtadvd) on the car Wi-Fi with a ULA prefix, a short
# router lifetime and RDNSS, and NAT66es that prefix out of the wired uplink
# (home has native IPv6). The car's IPv4 setup is left alone. It also adds
# 172.16.224.254 + NAT44 for the proven manual-IPv4 setup (Bimmerfest 2022),
# in case iOS exposes IP settings for the CarPlay network.
# On the iPhone: Settings > Wi-Fi > the car network > Check For Internet Access ON.
#
#   sudo scripts/poc-mac-nat.sh up       # join car Wi-Fi, RA + NAT66
#   sudo scripts/poc-mac-nat.sh status   # did the iPhone take an address? NAT states
#   sudo scripts/poc-mac-nat.sh down     # undo (the Mac stays on the car Wi-Fi)
#
# Needs secrets/carplay.env (gitignored): CAR_SSID, CAR_PSK.
# Optional: UPLINK=en7 to pick the wired interface by hand.
#
# pf rules go into the anchor com.apple/carplay-poc, which /etc/pf.conf
# already includes via nat-anchor "com.apple/*", so macOS's own rules stay.
set -eu

ANCHOR=com.apple/carplay-poc
STATE=/tmp/carplay-poc
HERE=$(cd "$(dirname "$0")/.." && pwd)
CAR_NET=172.16.224.0/24          # NBT EVO CarPlay DHCP range (iPhone usually .100), no gateway
GW4=172.16.224.254               # IPv4 gateway alias for a manual iPhone config (same as the Bimmerfest LTE-router setup)
PREFIX=fd5c:ba1e:f32:1::        # ULA /64 advertised on the car Wi-Fi
MYV6=${PREFIX}1
DNS6A=2001:4860:4860::8888
DNS6B=2606:4700:4700::1111

die() { echo "error: $*" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || die "run with sudo"

wifi_if() {
	networksetup -listallhardwareports | awk '/^Hardware Port: Wi-Fi/{getline; print $2; exit}'
}

# The first non-Wi-Fi interface holding a home-LAN address.
find_uplink() {
	wifi=$1
	for i in $(ifconfig -l); do
		case $i in lo*|"$wifi"|utun*|awdl*|llw*|bridge*|gif*|stf*|anpi*|ap*) continue ;; esac
		ip=$(ipconfig getifaddr "$i" 2>/dev/null || true)
		case $ip in 172.16.*) echo "$i"; return 0 ;; esac
	done
	return 1
}

v6_default() { route -n get -inet6 default 2>/dev/null | awk -v k="$1:" '$1==k{print $2}'; }

up() {
	[ -f "$HERE/secrets/carplay.env" ] || die "missing $HERE/secrets/carplay.env (CAR_SSID, CAR_PSK)"
	. "$HERE/secrets/carplay.env"
	[ -n "${CAR_SSID:-}" ] && [ -n "${CAR_PSK:-}" ] || die "CAR_SSID and CAR_PSK must be set"
	[ ! -f "$STATE/env" ] || die "already up; run down first"

	WIFI=$(wifi_if); [ -n "$WIFI" ] || die "no Wi-Fi interface"
	UPLINK=${UPLINK:-$(find_uplink "$WIFI" || true)}
	[ -n "$UPLINK" ] || die "no wired interface with a 172.16.x address: plug in the Ethernet adapter first (or set UPLINK=)"
	ifconfig "$UPLINK" | grep -q 'inet6 2' || die "$UPLINK has no global IPv6 address, so NAT66 has nowhere to go"
	GW6=$(v6_default gateway); GW6IF=$(v6_default interface)
	[ "$GW6IF" = "$UPLINK" ] || die "IPv6 default route is via '${GW6IF:-none}', not $UPLINK"
	echo "Wi-Fi: $WIFI   uplink: $UPLINK   IPv6 gateway: $GW6"

	echo "Joining \"$CAR_SSID\"..."
	networksetup -setairportnetwork "$WIFI" "$CAR_SSID" "$CAR_PSK" || die "join failed (wrong PSK, or the car refuses a second client?)"
	i=0; MYIP=
	while [ $i -lt 30 ]; do
		MYIP=$(ipconfig getifaddr "$WIFI" 2>/dev/null || true)
		case $MYIP in ""|169.254.*) sleep 1; i=$((i+1)) ;; *) break ;; esac
	done
	echo "Car DHCP gave the Mac: ${MYIP:-nothing}  (router offered: $(ipconfig getoption "$WIFI" router 2>/dev/null || echo none))"

	mkdir -p "$STATE"
	printf 'WIFI=%s\nUPLINK=%s\nGW6=%s\n' "$WIFI" "$UPLINK" "$GW6" > "$STATE/env"
	date -u +%FT%TZ > "$STATE/started"

	ifconfig "$WIFI" inet6 "$MYV6" prefixlen 64 alias
	ifconfig "$WIFI" alias "$GW4" netmask 255.255.255.255
	sysctl -w net.inet6.ip6.forwarding=1 >/dev/null
	sysctl -w net.inet.ip.forwarding=1 >/dev/null
	# With forwarding on, macOS stops taking RAs, so pin the uplink's default route.
	sleep 1
	if [ "$(v6_default interface)" != "$UPLINK" ]; then
		route -n add -inet6 default "$GW6" >/dev/null && echo "ADDED_V6DEF=1" >> "$STATE/env"
	fi

	printf 'nat on %s inet6 from %s/64 to any -> (%s)\nnat on %s inet from %s to any -> (%s)\n' \
		"$UPLINK" "$PREFIX" "$UPLINK" "$UPLINK" "$CAR_NET" "$UPLINK" | pfctl -a "$ANCHOR" -N -f - 2>/dev/null
	TOKEN=$(pfctl -E 2>&1 | awk '/Token/{print $NF}')
	echo "TOKEN=$TOKEN" >> "$STATE/env"

	# Short router lifetime: once the car leaves, the iPhone drops the default
	# route within ~1 min. Long valid lifetime so an address CarPlay might be
	# using is never removed mid-drive (deprecated addresses keep sessions).
	cat > "$STATE/rtadvd.conf" <<EOF
$WIFI:\\
	:maxinterval#20:mininterval#6:rltime#60:\\
	:addr="$PREFIX":prefixlen#64:pltime#1800:vltime#86400:\\
	:rdnssaddrs#2:rdnssaddr0="$DNS6A":rdnssaddr1="$DNS6B":rdnsslifetime#40:
EOF
	rtadvd -s -c "$STATE/rtadvd.conf" -p "$STATE/rtadvd.pid" "$WIFI"
	sleep 1
	[ -s "$STATE/rtadvd.pid" ] || die "rtadvd did not start (check: log show --last 1m --predicate 'process == \"rtadvd\"')"

	cat <<EOF

Ready: advertising $PREFIX/64 + DNS on $WIFI, NAT66 + NAT44 out of $UPLINK.
On the iPhone: Settings > Wi-Fi > "$CAR_SSID" > Check For Internet Access ON.
If iOS offers IP settings for it (Settings > Wi-Fi > Edit > Known Networks),
the proven IPv4 way also works: Configure IP Manual 172.16.224.5 / 255.255.255.0,
Router $GW4, DNS 1.1.1.1.
Then start CarPlay and try a Google Maps search + route (keep cellular as is).
  sudo $0 status   # the iPhone should appear with an ${PREFIX%::}:... address
  sudo $0 down     # undo
EOF
}

status() {
	if [ ! -f "$STATE/env" ]; then echo "not up"; exit 0; fi
	. "$STATE/env"
	echo "since $(cat "$STATE/started")  wifi=$WIFI  uplink=$UPLINK"
	echo "car DHCP IPv4 on Mac: $(ipconfig getifaddr "$WIFI" 2>/dev/null || echo none)"
	echo "rtadvd: $( [ -s "$STATE/rtadvd.pid" ] && kill -0 "$(cat "$STATE/rtadvd.pid")" 2>/dev/null && echo running || echo NOT running)"
	echo "ip6 forwarding=$(sysctl -n net.inet6.ip6.forwarding)  v6 default via $(v6_default interface)"
	HU=$(arp -n 172.16.224.1 2>/dev/null | awk '{print $4}')
	echo "clients on $PREFIX/64 (iPhone = SLAAC address; head unit 172.16.224.1 = ${HU:-unknown}):"
	ndp -an | awk -v p="${PREFIX%::}:" -v w="$WIFI" -v hu="$HU" 'index($1,p)==1 && $3==w {print "  " $1, $2, ($2==hu ? "<- HEAD UNIT took an address" : "")}'
	echo "NAT66 states: $(pfctl -s state 2>/dev/null | grep -c "${PREFIX%::}:" || true)   NAT44 states from 172.16.224.x: $(pfctl -s state 2>/dev/null | grep -c " 172\.16\.224\." || true)"
	echo "other car-net neighbours (IPv4):"; arp -an -i "$WIFI" 2>/dev/null | sed 's/^/  /'
}

down() {
	[ -f "$STATE/env" ] && . "$STATE/env" || true
	[ -s "$STATE/rtadvd.pid" ] && kill "$(cat "$STATE/rtadvd.pid")" 2>/dev/null || true
	pfctl -a "$ANCHOR" -F all 2>/dev/null || true
	[ -n "${TOKEN:-}" ] && pfctl -X "$TOKEN" 2>/dev/null || true
	sysctl -w net.inet6.ip6.forwarding=0 >/dev/null
	sysctl -w net.inet.ip.forwarding=0 >/dev/null
	[ -n "${WIFI:-}" ] && ifconfig "$WIFI" inet6 "$MYV6" -alias 2>/dev/null || true
	[ -n "${WIFI:-}" ] && ifconfig "$WIFI" -alias "$GW4" 2>/dev/null || true
	[ "${ADDED_V6DEF:-0}" = 1 ] && route -n delete -inet6 default >/dev/null 2>&1 || true
	rm -rf "$STATE"
	echo "Undone: rtadvd stopped, NAT removed, forwarding off, ULA + $GW4 aliases removed."
	echo "The Mac stays on the car Wi-Fi until you switch networks."
}

case ${1:-} in
	up) up ;;
	status) status ;;
	down) down ;;
	*) echo "usage: sudo $0 up|status|down" >&2; exit 2 ;;
esac
