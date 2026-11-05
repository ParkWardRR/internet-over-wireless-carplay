#!/bin/sh
# In-VM controller for the emulated garage. Runs on the OpenWrt firmware under
# test (the "box"); the cars, phones and the internet live in network namespaces
# on mac80211_hwsim radios:
#
#   hwsim0 box STA (root ns, managed by netifd like on the EWS377)
#   hwsim1 car A AP      ns carA     hwsim3 phone A   ns phoneA
#   hwsim2 car B AP      ns carB     hwsim4 phone B   ns phoneB
#   hwsim5 spare box radio (dual-STA experiments)
#   veth wan0 <-> inet0  ns inet: fake internet (captive.apple.com, DNS)
#
# Each car copies the F32's measured AP: 2.4 GHz, HT20, WPA+WPA2 mixed, TKIP
# group, 802.11u Interworking with Internet=0 (Private, venue Vehicular), DHCP
# 172.16.224.0/24 with no router and no DNS, head unit at .1.
#
# Every state change prints "EV <uptime> <event> <detail>"; the Go harness
# (emu/harness) turns those into timings.
#
#   emu.sh setup                    build the topology (once per boot)
#   emu.sh box-apply A|B            configure the box for one car (openwrt-carplay.sh)
#   emu.sh box-carplayd A [B]       run carplayd on the box instead
#   emu.sh car A|B on|off|power <dBm>|range in|out
#   emu.sh phone A|B join|leave|check|rs
#   emu.sh upstream down|up            the internet behind the box fails / returns
#   emu.sh isolation A|B               what phone X can reach (EV reach ...)
#   emu.sh capture start|stop          tcpdump RS/RA on the box's car link
#   emu.sh wait <condition> <timeout_s>   box-assoc|box-disassoc|phone-route A|B|phone-noroute A|B
set -u
STATE=/tmp/emu
mkdir -p "$STATE"

ev() { emuagent ev "$@"; }
die() { echo "error: $*" >&2; exit 1; }

ssid() { case $1 in A) echo "BMW00001 CarPlay" ;; B) echo "BMW00002 CarPlay" ;; esac; }
psk()  { case $1 in A) echo "Emu1Test2A" ;; B) echo "Emu3Test4B" ;; esac; }
chan() { case $1 in A) echo "${CAR_A_CHAN:-11}" ;; B) echo "${CAR_B_CHAN:-11}" ;; esac; }
lc()   { echo "$1" | tr AB ab; }
freq() { echo $(( 2407 + 5 * $(chan "$1") )); }
cidx() { [ "$1" = A ] && echo 1 || echo 2; }
# phy name for hwsim radio index $1 (survives moves between namespaces)
phy() {
	for ns in "" carA carB phoneA phoneB; do
		p=$( { [ -z "$ns" ] && ls "/sys/devices/virtual/mac80211_hwsim/hwsim$1/ieee80211" 2>/dev/null; } || true)
		[ -n "$p" ] && { echo "$p"; return; }
		if [ -n "$ns" ]; then
			p=$(ip netns exec "$ns" ls "/sys/devices/virtual/mac80211_hwsim/hwsim$1/ieee80211" 2>/dev/null)
			[ -n "$p" ] && { echo "$p"; return; }
		fi
	done
}
nsx() { ns=$1; shift; ip netns exec "$ns" "$@"; }
# Who hears whom (mac80211_hwsim "group" bitmask: radios hear each other only if
# their groups overlap). 1 = garage air around the box, 2 = car A cabin,
# 4 = car B cabin. A car in the garage is 1+cabin; driving out drops the 1, so
# the box loses it while the phone in the cabin stays connected.
setgroup() { echo "$2" > "/sys/kernel/debug/ieee80211/$(phy "$1")/hwsim/group"; }

setup() {
	[ -f "$STATE/setup.done" ] && { echo "already set up"; return; }
	for ns in carA carB phoneA phoneB inet; do ip netns add "$ns" 2>/dev/null; nsx "$ns" ip link set lo up; done

	# Radios into their namespaces, each with one clean netdev.
	move() { idx=$1 ns=$2 dev=$3; p=$(phy "$idx"); [ -n "$p" ] || die "no hwsim$idx"
		for d in "/sys/class/ieee80211/$p/device/net/"*; do [ -e "$d" ] && iw dev "${d##*/}" del; done
		iw phy "$p" set netns name "$ns"
		for d in $(nsx "$ns" ls "/sys/class/ieee80211/$p/device/net" 2>/dev/null); do nsx "$ns" iw dev "$d" del; done
		nsx "$ns" iw phy "$p" interface add "$dev" type managed; }
	move 1 carA car-a0; move 2 carB car-b0; move 3 phoneA ph-a0; move 4 phoneB ph-b0
	setgroup 0 1; setgroup 1 3; setgroup 2 5; setgroup 3 2; setgroup 4 4; setgroup 5 1

	# Fake internet behind the box's wan.
	ip link add wan0 type veth peer name inet0
	ip link set inet0 netns inet
	nsx inet ip addr add 198.51.100.1/24 dev inet0
	nsx inet ip -6 addr add 2001:db8:f00d::1/64 dev inet0 nodad
	nsx inet ip -6 addr add 2001:db8:ffff::1/128 dev lo
	nsx inet ip -6 addr add 2001:db8:ffff::53/128 dev lo
	nsx inet ip link set inet0 up
	mkdir -p "$STATE/www"
	printf '<HTML><HEAD><TITLE>Success</TITLE></HEAD><BODY>Success</BODY></HTML>\n' > "$STATE/www/hotspot-detect.html"
	nsx inet uhttpd -f -p "[2001:db8:ffff::1]:80" -h "$STATE/www" </dev/null >/dev/null 2>&1 &
	nsx inet dnsmasq --conf-file=/dev/null --no-resolv --no-hosts --keep-in-foreground --pid-file= \
		--listen-address=2001:db8:ffff::53 --bind-interfaces \
		--local=/apple.com/googleapis.com/ \
		--host-record=captive.apple.com,2001:db8:ffff::1 --host-record=maps.googleapis.com,2001:db8:ffff::1 \
		</dev/null >/dev/null 2>&1 &

	uci -q batch <<EOF
set network.wan=interface
set network.wan.device='wan0'
set network.wan.proto='static'
set network.wan.ipaddr='198.51.100.2'
set network.wan.netmask='255.255.255.0'
add_list network.wan.ip6addr='2001:db8:f00d::2/64'
set network.wan.ip6gw='2001:db8:f00d::1'
delete network.wan6
EOF
	uci commit network

	# Only the box radio is configured; the others now live in namespaces.
	cat > /etc/config/wireless <<EOF
config wifi-device 'radio0'
	option type 'mac80211'
	option path 'virtual/mac80211_hwsim/hwsim0'
	option band '2g'
	option channel 'auto'
	option htmode 'HT20'
	option country 'US'
EOF
	ip link set wan0 up
	/etc/init.d/network reload; sleep 2
	touch "$STATE/setup.done"
	ev setup_done
}

box_apply() {
	sh /root/carplay/openwrt-carplay.sh "$(ssid "$1")" "$(psk "$1")" >"$STATE/box-apply.log" 2>&1 || { cat "$STATE/box-apply.log"; die "openwrt-carplay.sh failed"; }
	[ -n "${SCAN_LIST:-}" ] && { uci set wireless.radio0.scan_list="$SCAN_LIST"; uci commit wireless; wifi reload; }
	ev box_applied "$1"
}

# The box runs carplayd instead of the netifd/odhcpd config: carplayd owns the
# radio (its own STA + wpa_supplicant), fw4 only does the zone and NAT66.
#   emu.sh box-carplayd A [B]      cars it should serve (A has priority)
box_carplayd() {
	[ $# -ge 1 ] || die "box-carplayd A [B]"
	uci set wireless.radio0.disabled='1'; uci commit wireless; wifi reload 2>/dev/null; sleep 1
	uci -q batch <<EOF
delete firewall.carplay
set firewall.carplay=zone
set firewall.carplay.name='carplay'
add_list firewall.carplay.device='phy0-sta0'
set firewall.carplay.input='ACCEPT'
set firewall.carplay.output='ACCEPT'
set firewall.carplay.forward='REJECT'
delete firewall.carplay_wan
set firewall.carplay_wan=forwarding
set firewall.carplay_wan.src='carplay'
set firewall.carplay_wan.dest='wan'
EOF
	wanz=$(uci show firewall | sed -n "s/^firewall\.\([^.]*\)\.name='wan'$/\1/p" | head -1)
	uci set "firewall.$wanz.masq6=1"; uci commit firewall; /etc/init.d/firewall restart 2>/dev/null
	{
		echo "phy = $(phy 0)"; echo "sta = phy0-sta0"; echo "dns = 2001:db8:ffff::53"
		echo "uplink = wan0"; echo "firewall = ${CARPLAYD_FIREWALL:-nft}"
		echo "events = $STATE/carplayd.events"; echo "hunt = ${CARPLAYD_HUNT:-1s}"
		for id in "$@"; do
			echo "[car $id]"; echo "ssid = $(ssid "$id")"; echo "psk = $(psk "$id")"; echo "channel = $(chan "$id")"
			[ "$id" = A ] && echo "priority = 1"
			[ -n "${CARPLAYD_DROP:-}" ] && echo "drop = $CARPLAYD_DROP"
		done
	} > /etc/carplayd.conf
	carplayd -c /etc/carplayd.conf -check >/dev/null || die "carplayd config invalid"
	(carplayd -c /etc/carplayd.conf >"$STATE/carplayd.log" 2>&1 </dev/null &)
	ev box_carplayd "$@"
}

# Robustness: kill carplayd (as a crash would) and start it again, as procd
# would; or kill only its wpa_supplicant.
carplayd_restart() {
	killall -9 carplayd 2>/dev/null; ev carplayd_killed
	(carplayd -c /etc/carplayd.conf >>"$STATE/carplayd.log" 2>&1 </dev/null &)
	ev carplayd_started
}
wpa_kill() {
	kill -9 "$(cat /var/run/carplayd/wpa.pid)" 2>/dev/null; ev wpa_killed
}

car() {
	id=$1 action=$2 ns=car$1 dev=car-$(lc "$1")0
	case $action in
	on)
		cat > "$STATE/hostapd-$id.conf" <<EOF
interface=$dev
driver=nl80211
ctrl_interface=$STATE/hostapd-$id
ssid=$(ssid "$id")
hw_mode=g
channel=$(chan "$id")
ieee80211n=1
beacon_int=100
wpa=3
wpa_key_mgmt=WPA-PSK
wpa_pairwise=TKIP CCMP
rsn_pairwise=TKIP CCMP
wpa_passphrase=$(psk "$id")
interworking=1
access_network_type=0
internet=0
venue_group=10
venue_type=1
EOF
		nsx "$ns" ip addr flush dev "$dev" 2>/dev/null
		nsx "$ns" hostapd -B -P "$STATE/hostapd-$id.pid" "$STATE/hostapd-$id.conf" >"$STATE/hostapd-$id.log" 2>&1 \
			|| { cat "$STATE/hostapd-$id.log"; die "hostapd $id failed"; }
		nsx "$ns" ip addr add 172.16.224.1/24 dev "$dev"
		# Signal = this radio's rx_rssi + tx power; pin tx power to 0 so
		# "car X signal N" sets what everyone hears directly. -45 dBm = parked.
		nsx "$ns" iw dev "$dev" set txpower fixed 0
		# The real NBT EVO took no address from the box's RA (P2); nor does this one.
		nsx "$ns" sysctl -qw "net.ipv6.conf.$dev.accept_ra=0"
		emuagent rssi "$(phy "$(cidx "$id")")" "${CAR_SIGNAL:--45}"
		nsx "$ns" dnsmasq --conf-file=/dev/null --no-resolv --no-hosts --port=0 --pid-file="$STATE/dnsmasq-$id.pid" \
			--interface="$dev" --bind-interfaces --dhcp-range=172.16.224.100,172.16.224.200,255.255.255.0,1h \
			--dhcp-option=3 --dhcp-option=6 --dhcp-leasefile="$STATE/leases-$id"
		ev car_on "$id"
		;;
	off)
		[ -f "$STATE/hostapd-$id.pid" ] && kill "$(cat "$STATE/hostapd-$id.pid")" 2>/dev/null
		[ -f "$STATE/dnsmasq-$id.pid" ] && kill "$(cat "$STATE/dnsmasq-$id.pid")" 2>/dev/null
		rm -f "$STATE/hostapd-$id.pid" "$STATE/dnsmasq-$id.pid"
		ev car_off "$id"
		;;
	range)
		idx=$(cidx "$id"); cabin=$( [ "$id" = A ] && echo 2 || echo 4 )
		case $3 in in) setgroup "$idx" $((cabin | 1)) ;; out) setgroup "$idx" "$cabin" ;; *) die "range in|out" ;; esac
		ev car_range "$id" "$3"
		;;
	signal)
		emuagent rssi "$(phy "$(cidx "$id")")" "$3" || die "signal -100..-1"
		ev car_signal "$id" "$3"
		;;
	power)
		nsx "$ns" iw dev "$dev" set txpower fixed "$(( $3 * 100 ))"
		ev car_power "$id" "$3"
		;;
	*) die "car $id on|off|power <dBm>" ;;
	esac
}

phone() {
	id=$1 action=$2 ns=phone$1 dev=ph-$(lc "$1")0
	case $action in
	join)
		mkdir -p "/etc/netns/$ns"
		echo "nameserver 2001:db8:ffff::53" > "/etc/netns/$ns/resolv.conf"
		# iPhones do "DAD with no delay" (SWIFTDAD in Apple's IPConfiguration), so no
		# DAD wait here either; otherwise the stand-in measures Linux, not the box.
		# Linux only drops expired RA routes on its 30 s route GC; iOS drops them when
		# the lifetime runs out. GC every second so drive-out timings are the box's.
		nsx "$ns" sysctl -qw net.ipv6.route.gc_interval=1
		nsx "$ns" sysctl -qw "net.ipv6.conf.$dev.accept_dad=0" "net.ipv6.conf.$dev.forwarding=0" "net.ipv6.conf.$dev.accept_ra=1" \
			"net.ipv6.conf.$dev.accept_ra_defrtr=1" "net.ipv6.conf.$dev.autoconf=1"
		# Real iPhones learn the channel from the car over Bluetooth, so scan only it.
		printf 'ctrl_interface=%s\nnetwork={\n ssid="%s"\n psk="%s"\n key_mgmt=WPA-PSK\n scan_freq=%s\n}\n' \
			"$STATE/wpa-ph$id" "$(ssid "$id")" "$(psk "$id")" "$(freq "$id")" > "$STATE/wpa-ph$id.conf"
		nsx "$ns" wpa_supplicant -B -i "$dev" -c "$STATE/wpa-ph$id.conf" -P "$STATE/wpa-ph$id.pid" >/dev/null 2>&1
		ev phone_join "$id"
		;;
	leave)
		[ -f "$STATE/wpa-ph$id.pid" ] && kill "$(cat "$STATE/wpa-ph$id.pid")" 2>/dev/null
		rm -f "$STATE/wpa-ph$id.pid"; nsx "$ns" ip addr flush dev "$dev" 2>/dev/null
		ev phone_leave "$id"
		;;
	rs) nsx "$ns" emuagent rs "$dev" ;;
	check)
		out=$(nsx "$ns" uclient-fetch -q -T 3 -O - http://captive.apple.com/hotspot-detect.html 2>/dev/null)
		case $out in *Success*) ev phone_internet "$id" ok ;; *) ev phone_internet "$id" fail; return 1 ;; esac
		;;
	*) die "phone $id join|leave|check|rs" ;;
	esac
}

# The ISP fails beyond the box: wan0 stays up and configured, but nothing
# answers (replies to the box's prefix are prohibited in the fake internet).
upstream() {
	case $1 in
	down) nsx inet ip -6 route add prohibit 2001:db8:f00d::/64 metric 1 ;;
	up) nsx inet ip -6 route del prohibit 2001:db8:f00d::/64 metric 1 ;;
	*) die "upstream down|up" ;;
	esac
	ev upstream "$1"
}

# What a phone on car X's Wi-Fi can open a TCP connection to. Only the
# internet should answer: not the box (SSH, web UI) on any of its addresses,
# not the home LAN behind the uplink, not the other car's network.
isolation() {
	id=$1 ns=phone$1 dev=ph-$(lc "$1")0
	pfx=fd5c:ba1e:f32:$(lc "$1")
	other=fd5c:ba1e:f32:$( [ "$id" = A ] && echo b || echo a )
	# A "home LAN" web server next to the box's uplink.
	[ -f "$STATE/home-www.pid" ] || { nsx inet uhttpd -f -p "[2001:db8:f00d::1]:8080" -h "$STATE/www" </dev/null >/dev/null 2>&1 & echo $! > "$STATE/home-www.pid"; sleep 1; }
	ll=$(nsx "$ns" ip -6 route show default | sed -n 's/.* via \(fe80::[^ ]*\) .*/\1/p' | head -1)
	nsx "$ns" emuagent tcp internet_http "[2001:db8:ffff::1]:80"
	nsx "$ns" emuagent tcp internet_dns "[2001:db8:ffff::53]:53"
	nsx "$ns" emuagent tcp home_lan "[2001:db8:f00d::1]:8080"
	nsx "$ns" emuagent tcp box_uplink_ssh "[2001:db8:f00d::2]:22"
	nsx "$ns" emuagent tcp box_car_ssh "[$pfx::1]:22"
	nsx "$ns" emuagent tcp box_car_web "[$pfx::1]:80"
	[ -n "$ll" ] && nsx "$ns" emuagent tcp box_ll_ssh "[$ll%$dev]:22"
	nsx "$ns" emuagent tcp other_car "[$other::1]:22"
}

# Every RS and RA on the box's car link, as tcpdump -v text, for the harness
# to check RFC 4861 timing and the withdrawal contents.
capture() {
	case $1 in
	start)
		(tcpdump -i phy0-sta0 -n -tt -v -l 'icmp6 and (ip6[40] == 133 or ip6[40] == 134)' >"$STATE/ra.txt" 2>"$STATE/ra.err" </dev/null &)
		sleep 1; ev capture_start ;;
	stop) killall tcpdump 2>/dev/null; sleep 1; ev capture_stop; echo "----- capture"; cat "$STATE/ra.txt" ;;
	*) die "capture start|stop" ;;
	esac
}

case ${1:-} in
	setup) setup ;;
	upstream) upstream "$2" ;;
	isolation) isolation "$2" ;;
	capture) capture "$2" ;;
	box-apply) box_apply "$2" ;;
	box-carplayd) shift; box_carplayd "$@" ;;
	carplayd-restart) carplayd_restart ;;
	wpa-kill) wpa_kill ;;
	car) car "$2" "$3" "${4:-}" ;;
	phone) phone "$2" "$3" ;;
	# Timing-critical polling (20 ms, ms timestamps) is done by the Go agent.
	wait) shift; exec emuagent wait "$@" ;;
	events) cat "$STATE/events" 2>/dev/null ;;
	*) echo "usage: emu.sh setup|box-apply A|B|car A|B on|off|power N|phone A|B join|leave|check|wait <cond> [A|B] [timeout]" >&2; exit 2 ;;
esac
