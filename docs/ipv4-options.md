# IPv4 options that don't fight the car

> **Status (2026-11-02):** not needed. The IPv6 route worked in P2 ([findings.md](findings.md)). The simplest IPv4 fallback is still ksweber's manual setup (phone Configure IP → `.5` / router `.254`), which iOS 27 allows via Known Networks.
>
> **Status (2026-11-13):** needed after all, for a reason P2 never exercised — **IPv4-only hosts on the home LAN**. Reaching the internet works; reaching `nas.example.net` does not. This is option **E**, and the Order below always said to add it once P2 worked. See [E, in practice](#e-in-practice-the-homelab-problem).

The car's DHCP gives the iPhone `172.16.224.x/24`, no router, no DNS. Racing that DHCP server is crude: it contradicts the car. These options work *with* what the car hands out.

| # | Idea | Why it's clean | Catch | Odds | Test cost |
|---|---|---|---|---|---|
| A | **Make the car hand out the router**: read the NBT EVO coding (CAFD) for WLAN/DHCP parameters; if a gateway/DNS field exists for the CarPlay interface, point it at `172.16.224.254` | The source of truth says it, so nothing disagrees. Works for every client | No public evidence such a parameter exists; the WLAN entries people code (`WLAN_STATUS`, `WLAN_CHANNEL`, `WLAN_DOMAIN`) reportedly belong to the hotspot, not CarPlay. Changing coding is a write (back up first) | Low–medium | Read-only E-Sys dump over ENET (ties into P5) |
| B | **On-link proxy**: the phone already reaches `172.16.224.0/24` without a router. A proxy at `172.16.224.254` needs no routing at all. Deliver it as a PAC URL in a configuration profile's Wi-Fi payload for the CarPlay SSID (`com.apple.wifi.managed`, `ProxyType`/`ProxyPACURL`), with DIRECT fallback | Zero routing, zero DHCP. PAC fallback makes it vanish when the box is gone | iOS applies proxy settings from the *primary* service only ([Apple DTS, 2017](https://developer.apple.com/forums/thread/92039)); with cellular dead the car Wi-Fi may become primary, but don't count on it. A profile Wi-Fi entry may replace the CarPlay-provisioned one. QUIC/UDP bypasses proxies (Maps falls back to TCP) | Low–medium | One `.mobileconfig` + a proxy on the Mac |
| C | **ICMP Router Discovery (RFC 1256)**: IPv4's own router advertisement. The box multicasts ICMP type 9 to 224.0.0.1 | The exact IPv4 twin of the IPv6 plan | No sign Darwin/iOS implements it (most client OSes dropped it) | Low | Small Go sender; watch for the phone's traffic |
| D | **In-path DHCP relay**: a garage AP with the same SSID/PSK, bridged to the car network (OpenWrt `relayd`), adds router/DNS options to the car's own DHCP replies as they pass through | Car still answers, the box only annotates | The phone must associate to the garage AP, but the car's AP is inside the car and always stronger. Roaming at the exit drops CarPlay | Low | Second radio, messy |
| E | **IPv4 service over IPv6**: DNS64 + NAT64 on the box; iOS synthesizes IPv6 for IPv4-only hosts, and for literals via `ipv4only.arpa` (iOS 9.2+, CFNetwork/Network.framework only) | Phone's IPv4 untouched, no conflict. Already in [ipv6-route.md](ipv6-route.md) | Depends on the IPv6 route working | High (if IPv6 works) | Part of P3 |

## Order

1. **P2 as planned** (IPv6 RA). If it works, add E and stop.
2. **A, read-only**: with the ENET cable (P5), read the NBT EVO CAFD and search it for `WLAN`, `DHCP`, `GATEWAY`, `ROUTER`, `DNS`, `HOTSPOT`, `CARPLAY`. Write nothing until a parameter is found and the coding is backed up.
3. **B and C** in the same session as P2 if IPv6 fails: they're cheap and either one would be a real IPv4 fix.

D stays on the shelf.

## E, in practice: the homelab problem

P2 proved the phone can reach **the internet**. It did not prove the phone can
reach **the home LAN**, and those turn out to be different problems. Measured
2026-11-12 with a phone live on the F32:

- `nas.example.net` resolves perfectly well — the zone is hosted by a public DNS provider
  and returns `10.0.0.21`. **DNS is not the fault.** The
  phone gets a correct answer and then has nowhere to send the packet.
- The host is **IPv4-only** (no AAAA), and the phone's only working path on a
  car link is IPv6. Its car-side IPv4 has no route off-link, and the box drops
  it outright: `iifname "cara" meta nfproto ipv4 drop`.
- The box *also* firewalls the car off from the home LAN on purpose:
  `ip6 daddr { <home /56>, fc00::/7, fe80::/10, ff00::/8 } reject`.

So a plain DNS relay — pointing the phone at pi-hole, or running a forwarder on
the box — changes nothing: it returns the same unreachable IPv4 address. Only a
relay that **rewrites the answer into an IPv6 address the phone can reach**
helps, which is DNS64, and it is only useful paired with a NAT64 translator.

### What is built

`carplayd` gained `nat64 = <prefix>/96` and `nat64_allow = <IPv4 hosts>`
(commit *feat(carplayd): reach IPv4-only LAN hosts via DNS64/NAT64*, tests in
`carplayd/nat64_test.go`). When set it:

- emits an nftables **accept for each allowed host's synthesised /128**, placed
  ahead of every reject — the NAT64 /96 lives inside `fc00::/7`, and a
  translated packet leaves by the translator rather than the uplink, so both
  rejects would otherwise swallow it;
- opens **port 53** on the car link, which the input chain otherwise closes
  deliberately, because a synthesised AAAA only exists if the phone asks us;
- advertises **only the box** as RDNSS. Offering a public resolver alongside
  ours would make resolution depend on which the phone picked — ours
  synthesises the AAAA, theirs returns the unroutable A. `dns =` stays the
  upstream list and the health target, so health still tests the real internet.

Use a prefix out of our own space (`fd5c:ba1e:f32:64::/96`), **not**
`64:ff9b::/96`: RFC 6052 §3.1 forbids the well-known prefix for non-global
targets, and every host worth reaching here is RFC 1918. carplayd rejects that
combination at config parse.

### The blocker, and how it was cleared

The box could not run a translator at all. Its Orange Pi 4A vendor kernel has
`# CONFIG_TUN is not set`, so tayga has no `/dev/net/tun`, and with no kernel
headers `jool-dkms` cannot build a module either. nftables has no v6→v4
translation of its own, so there was no third option.

Resolved 2026-11-13 by cross-building `tun.ko` out-of-tree against the Orange Pi
source — see [kernel-modules.md](kernel-modules.md). `/dev/net/tun` now exists
and survives a reboot, so tayga is viable.

### Deployed 2026-11-13

Translator **tayga** (`/etc/tayga.conf`, tun `nat64`, pool `192.168.255.0/24`
masqueraded out `eth0` by its own `ip carplay_nat64` nft table — its own, so
carplayd recreating `inet carplayd` cannot take the rule with it). Resolver
**unbound** (`module-config: "dns64 validator iterator"`), bound only to the box
address on each car prefix, `access-control` refusing everything else, forwarding
`example.net` to the homelab resolver and the rest to public DNS.

Allowlist, chosen by the owner — three hosts covering five names:

| synthesised | IPv4 | names |
|---|---|---|
| `fd5c:ba1e:f32:64::a00:15` | 10.0.0.21 | nas, backup |
| `fd5c:ba1e:f32:64::a00:6` | 10.0.0.6 | media, vmhost |
| `fd5c:ba1e:f32:64::a00:35` | 10.0.0.53 | dns |

Verified after a reboot: all five names synthesise and answer. Everything else
is unchanged — `google.com` still returns its **real** AAAA (`dns64-synthall:
no`, so dual-stack hosts are still reached natively), and the home-prefix reject
still stands for every address outside the allowlist.

The one that had to be checked before deploying: **`captive.apple.com` has real
AAAA records** (`2620:149:a21:f000::…`), so it is not synthesised and iOS's
validation probe is unaffected. Had it been IPv4-only, DNS64 would have pointed
the probe at an address the allowlist rejects and broken *Use For Internet*
outright.

Two things to know about the box:

- **dnsmasq owns `:53`.** Stock Debian `dnsmasq.service` wildcard-binds every
  address, so unbound could not open the car-side resolver at all
  (`Address already in use for fd5c:ba1e:f32:a::1 port 53`). Fixed with
  `bind-dynamic` + `except-interface=cara,carb` in `/etc/dnsmasq.d/`, which also
  copes with those interfaces appearing later than dnsmasq does.
- **It needed `tun.ko`**, which the vendor kernel does not ship
  ([kernel-modules.md](kernel-modules.md)).

Not yet proven: a real phone using it. Both cars were away.

### The alternative, if the allowlist grows awkward

Give the target host a stable **ULA** address and AAAA record and narrow the
home-prefix reject to that `/128`. No translation at all, native end to end —
but per-host, and it needs router and DNS changes rather than box-only ones.
