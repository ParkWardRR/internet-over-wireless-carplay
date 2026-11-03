#!/bin/sh
# Make a Linux box (garage-sbc: Debian 12, NetworkManager, radvd, iptables) give
# the iPhone internet over the car's CarPlay Wi-Fi by itself, whenever the car
# is on. The hands-off version of poc-linux.sh `up`:
#
#   NM connection "carplay-car" (autoconnect, retries forever) joins the car's
#   CarPlay Wi-Fi on wlan0 when it appears. A NetworkManager dispatcher hook
#   then checks for a car DHCP lease (172.16.224.x), turns on forwarding, adds
#   NAT66/NAT44 out of eth0 and starts carplay-radvd.service (ULA /64, 60 s
#   router lifetime, RDNSS). On pre-down it stops radvd first, so the goodbye
#   RA (router lifetime 0) goes out while still associated, then removes NAT.
#
#   sudo ./carplay-box.sh install     # reads CAR_SSID/CAR_PSK from ./carplay.env
#   sudo ./carplay-box.sh status
#   sudo ./carplay-box.sh uninstall
#
# Interim box until an OpenWrt router replaces it (docs/openwrt-box.md).
set -eu
PATH=$PATH:/usr/sbin:/sbin

HERE=$(cd "$(dirname "$0")" && pwd)
ENV=${CARPLAY_ENV:-$HERE/carplay.env}
CON=carplay-car
WIFI=${WIFI:-wlan0}
UPLINK=${UPLINK:-eth0}
PREFIX=${PREFIX6:-fd5c:ba1e:f32:1::}
GW4=172.16.224.254
ETC=/etc/carplay-box
UNIT=/etc/systemd/system/carplay-radvd.service
HOOK=/etc/NetworkManager/dispatcher.d/90-carplay-box
PREHOOK=/etc/NetworkManager/dispatcher.d/pre-down.d/90-carplay-box

die() { echo "error: $*" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || die "run with sudo"

install() {
	[ -f "$ENV" ] || die "missing $ENV (CAR_SSID, CAR_PSK)"
	. "$ENV"
	[ -n "${CAR_SSID:-}" ] && [ -n "${CAR_PSK:-}" ] || die "CAR_SSID and CAR_PSK must be set"
	[ ! -d /run/carplay-poc ] || die "poc-linux.sh is up; run its down first"
	command -v radvd >/dev/null || die "radvd not installed (apt install radvd)"

	mkdir -p "$ETC"
	cat > "$ETC/radvd.conf" <<EOF
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
	RDNSS 2001:4860:4860::8888 2606:4700:4700::1111 {
		AdvRDNSSLifetime 40;
	};
};
EOF
	radvd -c -C "$ETC/radvd.conf" >/dev/null 2>&1 || die "radvd config check failed"

	cat > "$UNIT" <<EOF
[Unit]
Description=IPv6 router advertisements on the car's CarPlay Wi-Fi (started by NetworkManager)
After=network.target

[Service]
ExecStart=/usr/sbin/radvd -n -C $ETC/radvd.conf -p /run/carplay-radvd.pid -m stderr
Restart=on-failure
EOF
	systemctl daemon-reload

	cat > "$HOOK" <<EOF
#!/bin/sh
# Installed by carplay-box.sh. Routes for the iPhone while on the car's CarPlay Wi-Fi.
PATH=/usr/sbin:/usr/bin:/sbin:/bin
[ "\${CONNECTION_ID:-}" = "$CON" ] || exit 0
nat() {
	for t in iptables ip6tables; do
		\$t -t nat -N CARPLAY_BOX 2>/dev/null || true
		\$t -t nat -C POSTROUTING -j CARPLAY_BOX 2>/dev/null || \$t -t nat -A POSTROUTING -j CARPLAY_BOX
		\$t -t nat -F CARPLAY_BOX
	done
	iptables -t nat -A CARPLAY_BOX -s 172.16.224.0/24 -o $UPLINK -j MASQUERADE
	case $PREFIX in fd*) ip6tables -t nat -A CARPLAY_BOX -s ${PREFIX}/64 -o $UPLINK -j MASQUERADE ;; esac
}
unnat() {
	for t in iptables ip6tables; do
		\$t -t nat -D POSTROUTING -j CARPLAY_BOX 2>/dev/null || true
		\$t -t nat -F CARPLAY_BOX 2>/dev/null || true
		\$t -t nat -X CARPLAY_BOX 2>/dev/null || true
	done
}
case "\$2" in
up)
	# Home is 172.16/16 too: only route once the car's own DHCP has answered.
	lease=\$(ip -4 -o addr show dev "\$1" dynamic | awk '{print \$4}' | head -1)
	case \$lease in 172.16.224.*) ;; *) logger -t carplay-box "joined but no car lease on \$1 (\${lease:-none}); not routing"; exit 0 ;; esac
	sysctl -qw net.ipv6.conf.all.forwarding=1 net.ipv4.ip_forward=1
	nat
	systemctl start carplay-radvd.service
	logger -t carplay-box "car Wi-Fi up (\$lease): radvd + NAT on"
	;;
pre-down)
	systemctl stop carplay-radvd.service   # goodbye RA while still associated
	logger -t carplay-box "car Wi-Fi going down: goodbye RA sent"
	;;
down)
	systemctl stop carplay-radvd.service
	unnat
	sysctl -qw net.ipv6.conf.all.forwarding=0 net.ipv4.ip_forward=0
	logger -t carplay-box "car Wi-Fi down: radvd + NAT off"
	;;
esac
EOF
	chmod 755 "$HOOK"
	mkdir -p "$(dirname "$PREHOOK")"
	ln -sf "$HOOK" "$PREHOOK"

	nmcli connection delete "$CON" >/dev/null 2>&1 || true
	nmcli connection add type wifi ifname "$WIFI" con-name "$CON" ssid "$CAR_SSID" \
		connection.autoconnect yes connection.autoconnect-retries 0 \
		wifi-sec.key-mgmt wpa-psk wifi-sec.psk "$CAR_PSK" \
		ipv4.method auto ipv4.never-default yes ipv4.ignore-auto-dns yes ipv4.addresses "$GW4/24" \
		ipv6.method manual ipv6.addresses "${PREFIX}1/64" ipv6.never-default yes >/dev/null
	echo "Installed. garage-sbc joins the car's CarPlay Wi-Fi whenever it appears; radvd + NAT follow automatically."
	echo "Watch it: journalctl -t carplay-box -f"
}

status() {
	echo "connection: $(nmcli -t -f GENERAL.STATE connection show "$CON" 2>/dev/null | cut -d: -f2 || echo 'not installed') (autoconnect $(nmcli -g connection.autoconnect connection show "$CON" 2>/dev/null))"
	echo "wlan0: $(nmcli -t -f DEVICE,STATE dev | grep "^$WIFI:" | cut -d: -f2)  $(iw dev "$WIFI" link | awk '/freq|signal/{$1=$1; printf "%s; ", $0}')"
	echo "carplay-radvd: $(systemctl is-active carplay-radvd.service 2>/dev/null)"
	echo "forwarding v6=$(sysctl -n net.ipv6.conf.all.forwarding) v4=$(sysctl -n net.ipv4.ip_forward)"
	echo "iPhone(s) on ${PREFIX}/64:"; ip -6 neigh show dev "$WIFI" 2>/dev/null | grep -i "^${PREFIX%::}:" | awk '{print "  " $1, $NF}' || true
	echo "NAT66 connections: $(ip6tables -t nat -L CARPLAY_BOX -v -n -x 2>/dev/null | awk 'NR>2{s+=$1} END{print s+0}')  NAT44: $(iptables -t nat -L CARPLAY_BOX -v -n -x 2>/dev/null | awk 'NR>2{s+=$1} END{print s+0}')"
	echo "recent events:"; journalctl -t carplay-box -n 5 --no-pager -o short 2>/dev/null | sed 's/^/  /'
}

uninstall() {
	nmcli connection delete "$CON" >/dev/null 2>&1 || true
	systemctl stop carplay-radvd.service 2>/dev/null || true
	rm -f "$HOOK" "$PREHOOK" "$UNIT"
	systemctl daemon-reload
	for t in iptables ip6tables; do
		$t -t nat -D POSTROUTING -j CARPLAY_BOX 2>/dev/null || true
		$t -t nat -F CARPLAY_BOX 2>/dev/null || true
		$t -t nat -X CARPLAY_BOX 2>/dev/null || true
	done
	sysctl -qw net.ipv6.conf.all.forwarding=0 net.ipv4.ip_forward=0
	rm -rf "$ETC"
	echo "Uninstalled: connection, dispatcher hook, radvd unit and NAT removed."
}

case ${1:-} in
	install) install ;;
	status) status ;;
	uninstall) uninstall ;;
	*) echo "usage: sudo $0 install|status|uninstall" >&2; exit 2 ;;
esac
