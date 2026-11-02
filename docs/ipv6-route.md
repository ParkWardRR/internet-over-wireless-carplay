# The IPv6 route

Give the iPhone internet over the car's CarPlay Wi-Fi without touching the phone's settings or the car. A box on that Wi-Fi sends IPv6 router advertisements (RAs); the car keeps doing IPv4 DHCP (172.16.224.0/24, no gateway) and never knows.

## Why IPv6

| Need | IPv4 way | IPv6 way |
|---|---|---|
| Give the phone a default route | Phone settings (not offered for CarPlay networks) or racing the car's DHCP server | RA from any device on the link. The car doesn't send RAs, so nothing conflicts |
| Give the phone DNS | Same problem | RDNSS option in the RA (RFC 8106) |
| Take the route away when the car leaves | Lease must expire (car controls it) | Router lifetime we pick (60 s), or a "goodbye" RA with lifetime 0 |

## What the garage path can reach (AAAA checked 2026-11-01)

| Service | Hosts | IPv6 |
|---|---|---|
| iOS internet check | `captive.apple.com` | ✅ |
| Google Maps | `maps.googleapis.com`, `maps.gstatic.com`, `khms0.googleapis.com`, `mt0.google.com`, `clients4.google.com` | ✅ all |
| Apple Maps tiles | `gsp-ssl` / `gspe1` / `gspe19` / `gspe35-ssl.ls.apple.com` | ✅ |
| Apple Maps other | `gsp1.apple.com`, `gs-loc.apple.com` (Wi-Fi location), `cdn`/`api.apple-mapkit.com` | ❌ |
| Push notifications | `courier.push.apple.com` | ❌ |
| Waze, Spotify, iCloud | `waze.com`, `api.spotify.com`, `icloud.com` | ✅ |
| Mapbox (3rd-party apps) | `api.mapbox.com` | ❌ |

Google Maps works over IPv6 alone. The ❌ rows need **DNS64 + NAT64** on the box (Apple requires App Store apps to work on NAT64/DNS64 networks, so apps cope).

## Design decisions

| Question | Finding | Decision |
|---|---|---|
| ULA or a public prefix? | RFC 6724's default policy ranks ULA (fc00::/7, precedence 3) **below IPv4** (35). In the garage there's no IPv4 route (cellular dead), so ULA still gets used; near the exit, cellular IPv4 wins, which is fine | **Test:** ULA + NAT66 (no home changes). **Permanent:** a public /64 |
| Where does a public /64 come from? | Spectrum delegates **`2603:8003:4a00:d700::/56`** to OPNsense; only `d700::/64` (LAN) is used. 255 spare /64s | One OPNsense static route, e.g. `2603:8003:4a00:d7ca::/64` → the box's LAN address. No NAT. (The /56 could change if Spectrum re-delegates; NAT66 is the fallback) |
| CLAT for IPv4-only sites? | Apple enables CLAT on Wi-Fi only with DHCPv4 option 108 **and** PREF64 in the RA ([APNIC/RIPE](https://blog.apnic.net/2022/11/21/deploying-ipv6-mostly-access-networks/)); iOS 18 CLAT had Wi-Fi bugs until ~18.5 ([sorah](https://blog.sorah.jp/2025/11/04/mystery-of-clat-on-apple-devices)). The car runs DHCPv4, not us | No CLAT. **DNS64 + NAT64** instead (covers hostnames; IPv4 literals still fail) |
| Router lifetime | odhcpd defaults 1800 s and caps at 2700 s (RFC 9096). Long lifetimes leave a dead route after the car drives off | **60 s**, RAs every 6–20 s |
| Prefix lifetimes | If CarPlay itself ever uses these addresses (head unit or phone picks them up via SLAAC), a short *valid* lifetime would drop the session after leaving. Deprecated addresses keep existing connections | `pltime` 1800, `vltime` 86400 |
| DNS | iOS takes RDNSS from RAs (iOS works on IPv6-only Wi-Fi, so it must) | Test: public resolvers. Permanent: box's own resolver with DNS64 |
| "Check For Internet Access" | Undocumented. Apple's text says it lets iOS use the car's hotspot internet | Turn **on**; P2 tests on and off |

## Leaving the garage

The car's AP moves with the car, so the **box** loses the association and the **iPhone** stays on the car Wi-Fi with a route to a box that's gone.

| Mechanism | Time to cellular |
|---|---|
| Router lifetime expires | ≤ 60 s |
| Neighbour unreachability on the box's link-local | ~ 5–35 s, once traffic flows |
| **Goodbye RA** (router lifetime 0, RDNSS lifetime 0) sent while still associated, triggered by car-AP RSSI dropping or the garage door opening (Home Assistant) | ~ immediate |

## Permanent box (P3)

OpenWrt travel router (GL.iNet), `wwan` = client of `BMW<5 digits> CarPlay` (legacy WPA allowed), `wan` = garage Ethernet.

| Piece | OpenWrt |
|---|---|
| RA + RDNSS + PREF64 | `odhcpd`: `ra server`, `ra_lifetime 60`, `ra_maxinterval 20`, `ra_dns`, `ra_pref64 64:ff9b::/96`, `ra_flags none` (SLAAC only) |
| Prefix | routed /64 from OPNsense (static route), or ULA + NAT66 (`nftables` masquerade / NPTv6) |
| DNS64 | `unbound` with `module-config: "dns64 validator iterator"` |
| NAT64 | `jool` (kmod) or `tayga` |
| Goodbye RA | small Go daemon: watch `iw dev wwan link` signal; under threshold, send RA lifetime 0 (or HA webhook on garage door) |

The car's IPv4 stays the car's. The box takes a DHCP lease like any client and adds nothing on IPv4.

## Open questions (answered by P2)

1. Does iOS make the CarPlay Wi-Fi its internet path when it carries an IPv6 default route but IPv4 has none?
2. Does "Check For Internet Access" change that?
3. Does the head unit autoconfigure from our RA? (`status` flags it.) If so, does CarPlay survive leaving?
4. Does the car AP forward multicast RAs between clients? (RS→RA on join should work even if not.)
