#!/bin/sh
# Turn a stock OpenWrt router (24.10/25.12, fw4, odhcpd) into the garage box:
# a Wi-Fi client of the car's CarPlay network that advertises an IPv6 route +
# DNS to the iPhone and NAT66es out of the WAN port (garage Ethernet). Same
# design as scripts/carplay-box.sh on garage-sbc; see docs/openwrt-box.md.
#
# Run on the router:  sh openwrt-carplay.sh 'BMW12345 CarPlay' 'the-psk'
#
# What it adds (nothing else is touched):
#   wireless.carplay        STA on the 2.4 GHz radio (the car is 2.4 GHz only),
#                           WPA/WPA2 mixed (the car uses a TKIP group cipher)
#   network.carplay         DHCP client on the car network, no default route,
#                           no DNS from the car
#   network.carplay_ula     alias on @carplay: fd5c:ba1e:f32:1::1/64 for the
#                           RA prefix, plus 172.16.224.254 for the manual-IPv4
#                           fallback
#   dhcp.carplay_ula        odhcpd RA only: 60 s router lifetime, RAs every
#                           6-20 s, SLAAC, RDNSS, prefix valid 1 d
#   firewall zone carplay   -> wan forwarding; masq6 on wan for the ULA
#
# UNTESTED on hardware. The SBC version (carplay-box.sh) is the proven one.
set -eu

SSID=${1:?usage: sh $0 'BMW12345 CarPlay' 'psk'}
PSK=${2:?usage: sh $0 'BMW12345 CarPlay' 'psk'}
ULA=fd5c:ba1e:f32:1::1/64

# The 2.4 GHz radio (band '2g'; older configs use hwmode 11g).
RADIO=
for r in $(uci show wireless | sed -n "s/^wireless\.\([^.]*\)=wifi-device$/\1/p"); do
	b=$(uci -q get "wireless.$r.band" || true); h=$(uci -q get "wireless.$r.hwmode" || true)
	case "$b$h" in *2g*|*11g*|*11b*) RADIO=$r; break ;; esac
done
[ -n "$RADIO" ] || { echo "no 2.4 GHz radio found" >&2; exit 1; }
echo "using $RADIO for the car's 2.4 GHz CarPlay Wi-Fi"

uci -q batch <<EOF
set wireless.$RADIO.disabled='0'
delete wireless.carplay
set wireless.carplay=wifi-iface
set wireless.carplay.device='$RADIO'
set wireless.carplay.mode='sta'
set wireless.carplay.network='carplay'
set wireless.carplay.ssid='$SSID'
set wireless.carplay.encryption='psk-mixed'
set wireless.carplay.key='$PSK'

delete network.carplay
set network.carplay=interface
set network.carplay.proto='dhcp'
set network.carplay.defaultroute='0'
set network.carplay.peerdns='0'
delete network.carplay_ula
set network.carplay_ula=interface
set network.carplay_ula.proto='static'
set network.carplay_ula.device='@carplay'
set network.carplay_ula.ipaddr='172.16.224.254'
set network.carplay_ula.netmask='255.255.255.0'
add_list network.carplay_ula.ip6addr='$ULA'

delete dhcp.carplay_ula
set dhcp.carplay_ula=dhcp
set dhcp.carplay_ula.interface='carplay_ula'
set dhcp.carplay_ula.ignore='1'
set dhcp.carplay_ula.dhcpv4='disabled'
set dhcp.carplay_ula.dhcpv6='disabled'
set dhcp.carplay_ula.ra='server'
set dhcp.carplay_ula.ra_default='1'
set dhcp.carplay_ula.ra_lifetime='60'
set dhcp.carplay_ula.ra_maxinterval='20'
set dhcp.carplay_ula.ra_mininterval='6'
set dhcp.carplay_ula.ra_flags='none'
set dhcp.carplay_ula.ra_slaac='1'
set dhcp.carplay_ula.ra_dns='1'
add_list dhcp.carplay_ula.dns='2001:4860:4860::8888'
add_list dhcp.carplay_ula.dns='2606:4700:4700::1111'
set dhcp.carplay_ula.max_preferred_lifetime='30m'
set dhcp.carplay_ula.max_valid_lifetime='1d'

delete firewall.carplay
set firewall.carplay=zone
set firewall.carplay.name='carplay'
add_list firewall.carplay.network='carplay'
add_list firewall.carplay.network='carplay_ula'
set firewall.carplay.input='ACCEPT'
set firewall.carplay.output='ACCEPT'
set firewall.carplay.forward='REJECT'
delete firewall.carplay_wan
set firewall.carplay_wan=forwarding
set firewall.carplay_wan.src='carplay'
set firewall.carplay_wan.dest='wan'
EOF

# NAT66 for the ULA on the wan zone (IPv4 masq is already on by default).
WANZ=$(uci show firewall | sed -n "s/^firewall\.\([^.]*\)\.name='wan'$/\1/p" | head -1)
[ -n "$WANZ" ] && uci set "firewall.$WANZ.masq6=1"

uci commit
/etc/init.d/network reload
wifi reload
/etc/init.d/odhcpd restart
/etc/init.d/firewall restart
echo "Done. With the car on: logread -f | grep -E 'odhcpd|wpa|carplay'; ip -6 neigh show"
