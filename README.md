# bmw-f32-carplay-wan

Give the iPhone internet while it's on **wireless CarPlay** in the basement garage, where there's no LTE/GSM.

## The problem

Wireless CarPlay puts the iPhone's Wi-Fi on the car's own access point (`BMW<5 digits> CarPlay`). That network hands out an address but no router and no DNS, so the phone keeps cellular as its internet path. Underground there's no cellular, so Google Maps can't search or route until you drive out.

## The key facts

| Fact | Source |
|---|---|
| Car DHCP gives the phone an address in **172.16.224.0/24** (iPhone usually `.100`) but **no router, no DNS**. The car's own LTE hotspot (if fitted) is 172.16.222.x and firewalled from it | [Bimmerfest](https://www.bimmerfest.com/threads/nbt-evo-carplay-wifi-password.1376809/), [Apple dev forum](https://developer.apple.com/forums/thread/76449) |
| **A second client works**: a laptop joined the CarPlay Wi-Fi and pinged the iPhone (no client isolation) | Bimmerfest #1, 2020 |
| **Proven end to end**: an LTE router joined the CarPlay Wi-Fi as `172.16.224.254`; the iPhone set manual IP `172.16.224.5/24`, router + DNS `.254`, and got internet while CarPlay kept running | Bimmerfest #2, 2022 |
| PSK = 10 random alphanumerics, delivered over Bluetooth. **Since ~2025 iOS shows it**, no jailbreak needed | Bimmerfest #1, #3 (Jan 2025) |
| Home LAN is 172.16.0.0/16, which overlaps the car's 172.16.224.0/24. Nothing at home uses .224.x and the box NATs, so it's harmless | Kea config |
| iOS shows **no IP/router/DNS settings** for CarPlay networks, only a **Check For Internet Access** toggle ("If your car offers hotspot internet access… you may be able to use that hotspot") | the iPhone, 2026-11-01 |
| iOS labels the car network **Weak Security**: probably WPA/TKIP or WPA/WPA2 mixed. The bridge box must allow legacy WPA | the iPhone, 2026-11-01 |
| Home has native IPv6 (Spectrum); Google Maps APIs answer over IPv6 | tested from the Mac |

If iOS still exposes IP settings for the CarPlay network somewhere (Settings → Wi-Fi → Edit → Known Networks), the proven manual-IPv4 setup is enough. If not, the route has to come from the network. **Plan: IPv6 router advertisements.** A box on the car Wi-Fi advertises an IPv6 prefix, a default route and DNS (RDNSS), and NAT66es to the home connection. The car's IPv4 is left alone, so there's no DHCP fight. With Check For Internet Access on, iOS should use it. Fallback if iOS ignores an IPv6-only route: answer DHCPv4 faster than the car, with a router option (racy). **Design, reachability table and the permanent-box recipe: [docs/ipv6-route.md](docs/ipv6-route.md).**

```mermaid
flowchart LR
  P[iPhone<br/>Check For Internet Access ON] -- "CarPlay Wi-Fi<br/>(car is the AP)" --> HU[Head unit AP<br/>BMW 5-digit CarPlay]
  B[Garage box<br/>Wi-Fi client of car AP] -. "IPv6 RA:<br/>prefix + route + DNS" .-> P
  P -. "IPv6 traffic" .-> B
  B -- "Ethernet + NAT66" --> L[Home LAN<br/>native IPv6]
  L --> W((Internet))
```

## Approaches, ranked

| # | Approach | Cost | Touches car | Expected | Status |
|---|---|---|---|---|---|
| 0 | Offline maps (Google Maps offline area / Apple Maps offline) + start the route on home Wi-Fi before CarPlay connects | $0 | no | Works today for known places; search outside the area fails | to test |
| 1 | **Garage Wi-Fi bridge**: box joins the car's CarPlay SSID as a client, advertises an IPv6 route + DNS, NAT66 to garage Ethernet | ~$40–90 | no | Best bet | **main track** |
| 1b | iPhone (USB-C models) + USB Ethernet adapter, cabled to a small travel router in the car that's a client of the garage Wi-Fi. Wi-Fi stays on CarPlay; internet goes over Ethernet. No PSK needed | ~$50 | no | Should work; adds a cable to the phone | backup if the PSK can't be found |
| 2 | Cellular booster (donor antenna outside, indoor antenna in garage) | $400–1,000 | no | Works for everyone and every app | fallback |
| 3 | OBD-II ENET: talk to the ZGW/head unit over the diagnostic Ethernet and look for an IP path to the head unit's Wi-Fi | ENET cable ~$15 | read-only | Learning; a usable route is unlikely without rooting the head unit | exploration |
| 4 | Root the head unit (QNX) and NAT Wi-Fi ↔ ENET | time + risk | yes | Possible in theory, high risk of bricking | not planned |

## Plan

| Phase | Do | Proves |
|---|---|---|
| **P0** | ✅ iPhone car-network page checked (no IP settings, Weak Security, toggle exists). Turn **Check For Internet Access** on; test offline maps in the garage | The free option |
| **P1** | Mac in the garage, car on: scan the car AP (band, channel, security, BSSID). Commands in `docs/test-log.md` | Box radio requirements (5 GHz? DFS? WPA/TKIP?) |
| **P2** | **MacBook proof of concept**: `sudo scripts/poc-mac-nat.sh up`. The Mac joins the car SSID, runs `rtadvd` (ULA /64, 60 s router lifetime, RDNSS) and NAT66 out of USB-C Ethernet | Second client allowed, no client isolation, iOS takes the IPv6 route, CarPlay survives, Maps works |
| **P3** | Permanent box: OpenWrt travel router (e.g. GL.iNet) as a Wi-Fi client of the car SSID (legacy WPA allowed), odhcpd RA + RDNSS, NAT66 (or a routed /64 from OPNsense) | Hands-off: car on → internet |
| **P4** | Exit test: drive out; the 60 s router lifetime should hand the phone back to cellular within a minute | No dead zone when leaving |
| **P5** | OBD ENET exploration (`docs/obd-enet.md`) | What's reachable from the OBD port |

## Known

- **Head unit:** factory NBT EVO (wireless CarPlay).
- **Garage:** Ethernet, home Wi-Fi and power at the parking spot.
- **iPhone:** CarPlay network page has no IP settings and no password field.

## Blocker: the CarPlay Wi-Fi password (PSK)

The box has to join the car's Wi-Fi, so it needs the PSK. The CarPlay page in Settings → Wi-Fi doesn't show it, but per Bimmerfest (Jan 2025) iOS does expose it somewhere now. Where to look, in order:

| # | Route | Notes |
|---|---|---|
| 1 | **Passwords app → Wi-Fi** → `BMW… CarPlay` | iOS 18+ lists every known network here |
| 2 | Settings → Wi-Fi → **Edit** → Known Networks → (i) | May also show the IP settings the CarPlay page hides |
| 3 | Encrypted Finder backup of the iPhone → decrypt its keychain | Needs a backup decryptor (small Go tool) |
| 4 | Approach 1b | Avoids the PSK entirely |

Brute force is out: 62^10 keyspace.

Still unknown: whether iOS accepts an IPv6-only internet route on a CarPlay network (P2 answers it).

## Rules

- Never commit the car's PSK, VIN, SSID digits or keys. Put them in `secrets/` (gitignored).
- Car work is read-only unless a step says otherwise: no coding, no flashing.
- Log every test in `docs/test-log.md`: date, firmware, what was run, result.
