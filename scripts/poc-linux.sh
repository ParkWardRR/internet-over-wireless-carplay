#!/bin/sh
# P2 on a Linux box (garage-sbc, Banana Pi, Debian 12 + NetworkManager): the
# box sits in the car, wired to the garage LAN on eth0, and joins the car's
# CarPlay Wi-Fi on wlan0 as a second client. Same idea as poc-mac-nat.sh:
#
#   IPv6: radvd on wlan0 advertises a ULA /64 (60 s router lifetime, RDNSS);
#         ip6tables NAT66 out of eth0. No phone settings needed: tap
#         Check For Internet Access, then turn on Use For Internet.
#   IPv4: the box also holds 172.16.224.254 with NAT44 out of eth0, for the
#         proven manual setup (phone: 172.16.224.5/24, router .254, DNS 1.1.1.1).
#
#   sudo ./poc-linux.sh ie          # read the car's beacon (Apple Device IE flags)
#   sudo ./poc-linux.sh capture 120 # passive capture on wlan0 (needs `up` or a join)
#   sudo ./poc-linux.sh up          # join car Wi-Fi, RA + NAT66 + NAT44
#   sudo ./poc-linux.sh status      # iPhone seen? NAT counters
#   sudo ./poc-linux.sh down        # undo everything, leave the car Wi-Fi
#
# Reads CAR_SSID/CAR_PSK from ./carplay.env (or CARPLAY_ENV=path); keep it 0600.
# Optional: PREFIX6=<routed /64>:: to advertise a public prefix without NAT66.
# Everything lives in NM connection "carplay-poc", iptables chain CARPLAY_POC
# and /run/carplay-poc, so the box's other services are untouched.
set -eu
PATH=$PATH:/usr/sbin:/sbin

HERE=$(cd "$(dirname "$0")" && pwd)
ENV=${CARPLAY_ENV:-$HERE/carplay.env}
STATE=/run/carplay-poc
CON=carplay-poc
CHAIN=CARPLAY_POC
WIFI=${WIFI:-wlan0}
UPLINK=${UPLINK:-eth0}
CAR_NET=172.16.224.0/24
GW4=172.16.224.254
PREFIX=${PREFIX6:-fd5c:ba1e:f32:1::}
DNS6A=2001:4860:4860::8888
DNS6B=2606:4700:4700::1111

die() { echo "error: $*" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || die "run with sudo"
mask() { sed -E 's/[0-9]{5}/#####/g'; }   # SSID digits are the end of the VIN
load_env() {
	[ -f "$ENV" ] || die "missing $ENV (CAR_SSID, CAR_PSK)"
	. "$ENV"
	[ -n "${CAR_SSID:-}" ] && [ -n "${CAR_PSK:-}" ] || die "CAR_SSID and CAR_PSK must be set in $ENV"
}

# The car's beacon: Apple Device IE (OUI 00:a0:40 type 0, flag bit 11 =
# InternetAccess) and the 802.11u Interworking element.
ie() {
	load_env
	ip link set "$WIFI" up 2>/dev/null || true
	iw dev "$WIFI" scan 2>/dev/null | python3 -c '
import re, sys
ssid = sys.argv[1]
names = ["AirPlay","Unconfigured","MFiConfigV1","WakeOnWireless","InterferenceRobustness","PPPoEServer",
         "WPSCapable","WPSActive","AirPrint","iAPoverWiFi","CarPlay","InternetAccess","ACPConfigV1",
         "UserContact","2.4GHz","5GHz","PINConfig","HomeKit"]
blocks = re.split(r"\nBSS ", "\n" + sys.stdin.read())
hit = False
for b in blocks:
    if not re.search(r"\n\s*SSID: " + re.escape(ssid) + r"\s*\n", b):
        continue
    hit = True
    for k in ("freq", "signal", "DS Parameter set", "primary channel"):
        m = re.search(r"\n\s*" + k + r":?\s*(.*)", b)
        if m: print(f"{k}: {m.group(1).strip()}")
    for l in b.splitlines():
        if re.search(r"(RSN|WPA):|Pairwise|Group cipher|Authentication suites|Interworking|Network type|Internet", l):
            print(l.strip())
    for m in re.finditer(r"Vendor specific: OUI 00:a0:40, data: ([0-9a-f ]+)", b):
        d = bytes.fromhex(m.group(1).replace(" ", ""))
        print("apple-device-ie raw:", d.hex())
        if not d or d[0] != 0:
            continue
        i = 1
        while i + 2 <= len(d):
            sid, ln = d[i], d[i + 1]
            v = d[i + 2:i + 2 + ln]
            if sid == 0:
                bits = [n for n in range(len(v) * 8) if v[n // 8] & (0x80 >> (n % 8))]
                flags = [names[n] if n < len(names) else f"bit{n}" for n in bits]
                print("flags:", v.hex(), flags, "INTERNET BIT SET" if 11 in bits else "internet bit clear")
            elif sid in (1, 2, 3):
                print(["name", "manufacturer", "model"][sid - 1] + ":", v.decode("utf-8", "replace"))
            i += 2 + ln
if not hit:
    print("car network not found in scan: car off, or out of range?")
' "$CAR_SSID"
}

capture() {
	secs=${1:-120}
	mkdir -p "$HERE/captures"
	f="$HERE/captures/passive-$(date +%Y%m%d-%H%M%S).pcap"
	echo "capturing ${secs}s on $WIFI -> $f (RS/RA, mDNS, DHCP)"
	timeout "$secs" tcpdump -ni "$WIFI" -w "$f" 'icmp6 or udp port 5353 or udp port 67 or udp port 68' || true
	echo "router advertisements seen (type 134):"
	tcpdump -nr "$f" 'icmp6 and ip6[40] == 134' 2>/dev/null | head -10
	echo "DHCP server(s):"
	tcpdump -nr "$f" 'udp src port 67' 2>/dev/null | awk '{print $3}' | sort -u | head
}

up() {
	load_env
	[ ! -d "$STATE" ] || die "already up; run down first"
	ip -6 addr show dev "$UPLINK" scope global | grep -q inet6 || die "$UPLINK has no global IPv6 address"
	ip -6 route show default | grep -q "dev $UPLINK" || die "no IPv6 default route via $UPLINK"

	echo "Joining $(echo "$CAR_SSID" | mask) on $WIFI..."
	nmcli connection delete "$CON" >/dev/null 2>&1 || true
	nmcli connection add type wifi ifname "$WIFI" con-name "$CON" ssid "$CAR_SSID" \
		connection.autoconnect no \
		wifi-sec.key-mgmt wpa-psk wifi-sec.psk "$CAR_PSK" \
		ipv4.method auto ipv4.never-default yes ipv4.ignore-auto-dns yes ipv4.addresses "$GW4/24" \
		ipv6.method manual ipv6.addresses "${PREFIX}1/64" ipv6.never-default yes >/dev/null
	nmcli --wait 45 connection up "$CON" >/dev/null || { nmcli connection delete "$CON" >/dev/null 2>&1; die "join failed (wrong PSK? car off?)"; }

	# The car's DHCP lease proves we're on the car network (home is 172.16/16 too).
	i=0; LEASE=
	while [ $i -lt 30 ]; do
		LEASE=$(ip -4 -o addr show dev "$WIFI" dynamic | awk '{print $4}' | head -1)
		[ -n "$LEASE" ] && break; sleep 1; i=$((i+1))
	done
	case $LEASE in 172.16.224.*) ;; *) nmcli connection delete "$CON" >/dev/null 2>&1; die "no car DHCP lease on $WIFI (got '${LEASE:-none}')" ;; esac
	echo "Car DHCP gave the box: $LEASE; also holding $GW4 and ${PREFIX}1"

	mkdir -p "$STATE"
	printf 'PREFIX=%s\nFWD6=%s\nFWD4=%s\n' "$PREFIX" \
		"$(sysctl -n net.ipv6.conf.all.forwarding)" "$(sysctl -n net.ipv4.ip_forward)" > "$STATE/env"
	sysctl -qw net.ipv6.conf.all.forwarding=1 net.ipv4.ip_forward=1

	iptables -t nat -N "$CHAIN"
	iptables -t nat -A POSTROUTING -j "$CHAIN"
	iptables -t nat -A "$CHAIN" -s "$CAR_NET" -o "$UPLINK" -j MASQUERADE
	ip6tables -t nat -N "$CHAIN"
	ip6tables -t nat -A POSTROUTING -j "$CHAIN"
	case $PREFIX in fd*) ip6tables -t nat -A "$CHAIN" -s "${PREFIX}/64" -o "$UPLINK" -j MASQUERADE ;; esac

	# Short router lifetime: the iPhone drops the route within a minute of the
	# car leaving. Long valid lifetime so addresses never vanish mid-drive.
	cat > "$STATE/radvd.conf" <<EOF
interface $WIFI {
	AdvSendAdvert on;
	MinRtrAdvInterval 6;
	MaxRtrAdvInterval 20;
	AdvDefaultLifetime 60;
	prefix ${PREFIX}/64 {
		AdvOnLink on;
		AdvAutonomous on;
		AdvPreferredLifetime 1800;
		AdvValidLifetime 86400;
	};
	RDNSS $DNS6A $DNS6B {
		AdvRDNSSLifetime 40;
	};
};
EOF
	radvd -c -C "$STATE/radvd.conf" >/dev/null 2>&1 || die "radvd config check failed"
	radvd -C "$STATE/radvd.conf" -p "$STATE/radvd.pid" -m logfile -l "$STATE/radvd.log"
	sleep 1
	[ -s "$STATE/radvd.pid" ] || die "radvd did not start: $(tail -3 "$STATE/radvd.log" 2>/dev/null)"

	case $PREFIX in fd*) ;; *)
		echo "Routed prefix: OPNsense needs a static route $PREFIX/64 via $(ip -6 -o addr show dev "$UPLINK" scope global | awk '{print $4}' | cut -d/ -f1 | head -1) and a LAN rule for it." ;;
	esac
	cat <<EOF

Ready. iPhone, with CarPlay connected:
  IPv6 test: Settings > Wi-Fi > the car network > tap Check For Internet Access;
             if Use For Internet appears, turn it on. Then Maps + CarNet Probe.
  IPv4 test: Settings > Wi-Fi > Edit > Known Networks > the car network >
             Configure IP > Manual 172.16.224.5 / 255.255.255.0, Router $GW4,
             DNS 1.1.1.1. Put it back to Automatic afterwards.
  sudo $0 status
EOF
}

status() {
	[ -d "$STATE" ] || { echo "not up"; exit 0; }
	. "$STATE/env"
	echo "link: $(iw dev "$WIFI" link | awk '/SSID|freq|signal|tx bitrate/{$1=$1; printf "%s; ", $0}' | mask)"
	echo "addresses: $(ip -br addr show dev "$WIFI" | cut -c17-)"
	echo "radvd: $( [ -s "$STATE/radvd.pid" ] && kill -0 "$(cat "$STATE/radvd.pid")" 2>/dev/null && echo running || echo NOT running)"
	echo "forwarding v6=$(sysctl -n net.ipv6.conf.all.forwarding) v4=$(sysctl -n net.ipv4.ip_forward); v6 default: $(ip -6 route show default | head -1)"
	ip -6 route show default | grep -q "dev $UPLINK" || echo "  WARNING: no IPv6 default via $UPLINK, NAT66 has no way out"
	echo "IPv6 neighbours on ${PREFIX}/64 (iPhone = SLAAC address):"
	ip -6 neigh show dev "$WIFI" | grep -i "^${PREFIX%::}:" | sed 's/^/  /' || true
	echo "IPv4 neighbours on the car net:"
	ip -4 neigh show dev "$WIFI" | sed 's/^/  /'
	echo "NAT44 packets: $(iptables -t nat -L "$CHAIN" -v -n -x | awk 'NR>2{s+=$1} END{print s+0}')   NAT66 packets: $(ip6tables -t nat -L "$CHAIN" -v -n -x | awk 'NR>2{s+=$1} END{print s+0}')"
}

down() {
	[ -f "$STATE/env" ] && . "$STATE/env" || true
	[ -s "$STATE/radvd.pid" ] && kill "$(cat "$STATE/radvd.pid")" 2>/dev/null || true
	for t in iptables ip6tables; do
		$t -t nat -D POSTROUTING -j "$CHAIN" 2>/dev/null || true
		$t -t nat -F "$CHAIN" 2>/dev/null || true
		$t -t nat -X "$CHAIN" 2>/dev/null || true
	done
	sysctl -qw net.ipv6.conf.all.forwarding="${FWD6:-0}" net.ipv4.ip_forward="${FWD4:-0}"
	nmcli connection delete "$CON" >/dev/null 2>&1 || true
	rm -rf "$STATE"
	echo "Undone: radvd stopped, NAT removed, forwarding restored, left the car Wi-Fi."
}

case ${1:-} in
	ie) ie ;;
	capture) capture "${2:-120}" ;;
	up) up ;;
	status) status ;;
	down) down ;;
	*) echo "usage: sudo $0 ie|capture [secs]|up|status|down" >&2; exit 2 ;;
esac
