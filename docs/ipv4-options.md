# IPv4 options that don't fight the car

The car's DHCP gives the iPhone `172.16.224.x/24`, no router, no DNS. Racing that DHCP server is crude: it contradicts the car. These options work *with* what the car hands out.

| # | Idea | Why it's clean | Catch | Odds | Test cost |
|---|---|---|---|---|---|
| A | **Make the car hand out the router**: read the NBT EVO coding (CAFD) for WLAN/DHCP parameters; if a gateway/DNS field exists for the CarPlay interface, point it at `172.16.224.254` | The source of truth says it, so nothing disagrees. Works for every client | No public evidence such a parameter exists. Changing coding is a write (back up first) | Low–medium | Read-only E-Sys dump over ENET (ties into P5) |
| B | **On-link proxy**: the phone already reaches `172.16.224.0/24` without a router. A proxy at `172.16.224.254` needs no routing at all. Deliver it as a PAC URL in a configuration profile's Wi-Fi payload for the CarPlay SSID (`com.apple.wifi.managed`, `ProxyType`/`ProxyPACURL`), with DIRECT fallback | Zero routing, zero DHCP. PAC fallback makes it vanish when the box is gone | iOS applies proxy settings from the *primary* service, and a Wi-Fi with no router may never be primary. A profile Wi-Fi entry may replace the CarPlay-provisioned one. QUIC/UDP bypasses proxies (Maps falls back to TCP) | Low–medium | One `.mobileconfig` + a proxy on the Mac |
| C | **ICMP Router Discovery (RFC 1256)**: IPv4's own router advertisement. The box multicasts ICMP type 9 to 224.0.0.1 | The exact IPv4 twin of the IPv6 plan | No sign Darwin/iOS implements it (most client OSes dropped it) | Low | Small Go sender; watch for the phone's traffic |
| D | **In-path DHCP relay**: a garage AP with the same SSID/PSK, bridged to the car network (OpenWrt `relayd`), adds router/DNS options to the car's own DHCP replies as they pass through | Car still answers, the box only annotates | The phone must associate to the garage AP, but the car's AP is inside the car and always stronger. Roaming at the exit drops CarPlay | Low | Second radio, messy |
| E | **IPv4 service over IPv6**: DNS64 + NAT64 on the box; iOS synthesizes IPv6 for IPv4-only hosts and literals (iOS 9.2+) | Phone's IPv4 untouched, no conflict. Already in [ipv6-route.md](ipv6-route.md) | Depends on the IPv6 route working | High (if IPv6 works) | Part of P3 |

## Order

1. **P2 as planned** (IPv6 RA). If it works, add E and stop.
2. **A, read-only**: with the ENET cable (P5), read the NBT EVO CAFD and search it for `WLAN`, `DHCP`, `GATEWAY`, `ROUTER`, `DNS`, `HOTSPOT`, `CARPLAY`. Write nothing until a parameter is found and the coding is backed up.
3. **B and C** in the same session as P2 if IPv6 fails: they're cheap and either one would be a real IPv4 fix.

D stays on the shelf.
