#!/bin/sh
# P2 proof of concept: the MacBook joins the car's CarPlay Wi-Fi as a second
# client and NATs it to the home LAN over a wired adapter. The iPhone then
# uses the Mac as its router on the car network.
#
#   sudo scripts/poc-mac-nat.sh up       # join car Wi-Fi, forward + NAT
#   sudo scripts/poc-mac-nat.sh status   # what's set, NAT states from the car
#   sudo scripts/poc-mac-nat.sh down     # undo everything except the Wi-Fi join
#
# Needs secrets/carplay.env (gitignored):
#   CAR_SSID='BMW12345 CarPlay'
#   CAR_PSK='...'
# Optional: UPLINK=en7 to pick the wired interface by hand.
#
# NAT rules go into the anchor com.apple/carplay-poc, which /etc/pf.conf
# already includes via nat-anchor "com.apple/*", so macOS's own rules stay.
set -eu

ANCHOR=com.apple/carplay-poc
STATE=/tmp/carplay-poc
HERE=$(cd "$(dirname "$0")/.." && pwd)
DNS=172.16.1.69

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

ip2int() { echo "$1" | awk -F. '{print ($1*16777216)+($2*65536)+($3*256)+$4}'; }
int2ip() { echo "$1" | awk '{printf "%d.%d.%d.%d", int($1/16777216)%256, int($1/65536)%256, int($1/256)%256, $1%256}'; }
masklen() { echo "$1" | awk -F. '{n=0; for(i=1;i<=4;i++){v=$i; while(v>0){n+=v%2; v=int(v/2)}} print n}'; }

up() {
	[ -f "$HERE/secrets/carplay.env" ] || die "missing $HERE/secrets/carplay.env (CAR_SSID, CAR_PSK)"
	. "$HERE/secrets/carplay.env"
	[ -n "${CAR_SSID:-}" ] && [ -n "${CAR_PSK:-}" ] || die "CAR_SSID and CAR_PSK must be set"

	WIFI=$(wifi_if); [ -n "$WIFI" ] || die "no Wi-Fi interface"
	UPLINK=${UPLINK:-$(find_uplink "$WIFI" || true)}
	[ -n "$UPLINK" ] || die "no wired interface with a 172.16.x address: plug in the Ethernet adapter first (or set UPLINK=)"
	echo "Wi-Fi: $WIFI   uplink: $UPLINK ($(ipconfig getifaddr "$UPLINK"))"

	echo "Joining \"$CAR_SSID\"..."
	networksetup -setairportnetwork "$WIFI" "$CAR_SSID" "$CAR_PSK" || die "join failed (wrong PSK, or the car refuses a second client?)"

	i=0; MYIP=
	while [ $i -lt 30 ]; do
		MYIP=$(ipconfig getifaddr "$WIFI" 2>/dev/null || true)
		case $MYIP in ""|169.254.*) sleep 1; i=$((i+1)) ;; *) break ;; esac
	done
	case $MYIP in ""|169.254.*) die "no DHCP address from the car after 30 s (got '${MYIP:-none}')" ;; esac
	MASK=$(ipconfig getoption "$WIFI" subnet_mask)
	ROUTER=$(ipconfig getoption "$WIFI" router 2>/dev/null || true)
	NET=$(int2ip $(( $(ip2int "$MYIP") & $(ip2int "$MASK") )))
	CAR_NET="$NET/$(masklen "$MASK")"
	echo "Car network: $CAR_NET   Mac on it: $MYIP   car offers router: ${ROUTER:-none}"

	DEF=$(route -n get default 2>/dev/null | awk '/interface:/{print $2}')
	[ "$DEF" = "$UPLINK" ] || echo "warning: default route is via ${DEF:-nothing}, not $UPLINK. NAT will still work, but this Mac's own traffic may break."

	sysctl -w net.inet.ip.forwarding=1 >/dev/null
	printf 'nat on %s inet from %s to any -> (%s)\n' "$UPLINK" "$CAR_NET" "$UPLINK" | pfctl -a "$ANCHOR" -N -f - 2>/dev/null
	TOKEN=$(pfctl -E 2>&1 | awk '/Token/{print $NF}')
	mkdir -p "$STATE"
	printf 'WIFI=%s\nUPLINK=%s\nCAR_NET=%s\nMYIP=%s\nTOKEN=%s\n' "$WIFI" "$UPLINK" "$CAR_NET" "$MYIP" "$TOKEN" > "$STATE/env"
	date -u +%FT%TZ > "$STATE/started"

	cat <<EOF

Ready. On the iPhone, Settings > Wi-Fi > (i) next to "$CAR_SSID":
  Configure IP  > Manual: keep the IP address and Subnet Mask it shows now,
                  set Router = $MYIP
  Configure DNS > Manual: $DNS
Then start CarPlay and try a Google Maps search + route.
Watch traffic: sudo $0 status     Undo: sudo $0 down (and set the iPhone back to Automatic)
EOF
}

status() {
	if [ ! -f "$STATE/env" ]; then echo "not up"; exit 0; fi
	. "$STATE/env"
	echo "since $(cat "$STATE/started")  wifi=$WIFI ($(ipconfig getifaddr "$WIFI" 2>/dev/null || echo no-ip))  uplink=$UPLINK  car_net=$CAR_NET"
	echo "forwarding=$(sysctl -n net.inet.ip.forwarding)"
	echo "rules:"; pfctl -a "$ANCHOR" -s nat 2>/dev/null | sed 's/^/  /'
	echo "NAT states from the car net: $(pfctl -s state 2>/dev/null | grep -c "$(echo "$CAR_NET" | cut -d. -f1-3)\." || true)"
}

down() {
	[ -f "$STATE/env" ] && . "$STATE/env" || true
	pfctl -a "$ANCHOR" -F all 2>/dev/null || true
	[ -n "${TOKEN:-}" ] && pfctl -X "$TOKEN" 2>/dev/null || true
	sysctl -w net.inet.ip.forwarding=0 >/dev/null
	rm -rf "$STATE"
	echo "NAT removed, forwarding off. The Mac stays on the car Wi-Fi until you switch networks."
	echo "iPhone: set the car network's Configure IP and DNS back to Automatic."
}

case ${1:-} in
	up) up ;;
	status) status ;;
	down) down ;;
	*) echo "usage: sudo $0 up|status|down" >&2; exit 2 ;;
esac
