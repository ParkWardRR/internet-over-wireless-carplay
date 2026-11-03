# OpenWrt box (P3, replaces garage-sbc)

Until it's built, garage-sbc does the job on its own: `scripts/carplay-box.sh` (installed 2026-11-03) joins the car's CarPlay Wi-Fi whenever it appears, and a NetworkManager hook starts radvd + NAT. The OpenWrt box does the same in a small, dedicated router.

## What the car needs (from P1/P2)

| Requirement | Why |
|---|---|
| **2.4 GHz** Wi-Fi client (STA) | The NBT EVO's CarPlay AP is 2.4 GHz only, channel 11, HT20 |
| WPA/WPA2 mixed with TKIP group cipher | `encryption 'psk-mixed'`; every OpenWrt `wpad` build has TKIP |
| Ethernet WAN | Garage LAN, native IPv6 (gets a global address + default route) |
| odhcpd RA server on the STA interface | Same RA that worked from garage-sbc: ULA /64, router lifetime 60 s, RDNSS |
| fw4 `masq6` on wan | NAT66 for the ULA (worked in P2; a routed /64 from OPNsense is optional) |
| Placement | In the garage close to the car. garage-sbc *inside* the car saw −71 to −79 dBm; outside the car, through glass and metal, it will be weaker, so test the spot before mounting |

## Hardware

| Pick | Notes |
|---|---|
| **GL.iNet GL-MT3000 (Beryl AX)**, ~$70–85 | Dual-band, vanilla OpenWrt 23.05+, plenty of RAM for DNS64/NAT64 later. Recommended |
| GL.iNet GL-MT300N-V2 (Mango), ~$30 | 2.4 GHz only, which is all the car needs; 128 MB RAM, fine for RA + NAT66, tight for unbound + jool |
| Avoid GL-SFT1200 (Opal) | No vanilla OpenWrt build |

Flash **vanilla OpenWrt** (24.10 or 25.12), not GL.iNet's stock firmware, so odhcpd and fw4 behave as documented.

## Setup

1. Flash vanilla OpenWrt; WAN port to the garage LAN. Check `ifstatus wan6` shows a global address.
2. Give the router a LAN subnet that isn't in 172.16.0.0/16 (stock `192.168.1.0/24` is fine).
3. Copy and run the config script with the F32's network (from `secrets/carplay.env`):
   ```sh
   scp scripts/openwrt-carplay.sh root@<router>:/tmp/
   ssh root@<router> "sh /tmp/openwrt-carplay.sh 'BMW##### CarPlay' '<psk>'"
   ```
   It adds only `wireless.carplay`, `network.carplay`/`carplay_ula`, `dhcp.carplay_ula`, a `carplay` firewall zone and `masq6` on wan.
4. Car on, CarPlay connected:
   ```sh
   iwinfo                                   # STA associated to the car, signal
   ifstatus carplay | grep -A3 ipv4-address # car DHCP lease 172.16.224.x
   ip -6 addr show; ip -6 neigh             # fd5c:ba1e:f32:1::1 on the STA; iPhone's SLAAC address
   logread | grep odhcpd                    # RAs on carplay_ula
   ```
5. iPhone: Settings → Wi-Fi → the CarPlay network → **Check For Internet Access** → **Use For Internet**. Maps.
6. When it works: `sudo ./carplay-box.sh uninstall` on garage-sbc, so the two boxes don't both advertise.

## Unknowns to check on the first run

- **odhcpd on an alias (`@carplay`)**: the ULA lives on an alias interface so the STA can stay `proto dhcp`. If odhcpd doesn't send RAs there, alternatives are `ip6assign 64` with `network.globals.ula_prefix` on `carplay` itself, or `odhcpd` → `radvd` package.
- **`ra_default '1'`** is required with a ULA-only prefix (otherwise odhcpd advertises router lifetime 0); check `tcpdump -vni <sta> icmp6` shows `router lifetime 60s`.
- **STA + AP on one radio**: if the router also runs an AP on the 2.4 GHz radio, it must follow the car's channel and drops when the car leaves. Simplest: use the 5 GHz radio for the router's own AP.
- **Goodbye RA**: odhcpd sends nothing on SIGTERM. Whether its final RA on interface disable carries router lifetime 0 with `ra_default 1` is disputed between two source readings; check with `tcpdump -vni <sta> icmp6` on `ifdown`. carplayd ([two-cars.md](two-cars.md)) sends its own goodbye, which sidesteps this. Without a goodbye, the 60 s lifetime still bounds the drive-out case.

## Later

- **DNS64 + NAT64** (`unbound` with `dns64`, `kmod-jool-netfilter`) + `ra_pref64 '64:ff9b::/96'` for the few IPv4-only hosts. Needs the MT3000-class box.
- **Validate the config before buying**: run OpenWrt x86 in a container on build-host with a veth pair standing in for the car Wi-Fi (dnsmasq on the far end as the "car"), and check netifd, odhcpd and fw4 produce the RA and NAT66 as designed.
