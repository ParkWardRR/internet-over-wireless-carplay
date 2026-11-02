# Seeing what the iPhone sees

The open question in P2 is iOS policy: the car announces "no internet" in its beacon, and we need to know whether iOS probes anyway, takes our route, and lets apps use it. Three tools answer it from the phone's side, cheapest first. Everything they produce goes in `secrets/`: logs and captures contain device names, MACs and the SSID.

| Tool | Needs | Answers |
|---|---|---|
| Packet capture (`rvictl`) | iPhone on USB to the Mac | Does the phone send a Router Solicitation, receive our RA, run its internet check (`captive.apple.com`) over the car Wi-Fi, query DNS? |
| iOS logs (`log collect`, Console.app) | iPhone on USB; the Wi-Fi logging profile for detail | *Why* iOS decides the network has or hasn't internet |
| **CarNet Probe** app (`ios/CarNetProbe`) | Installed once; no Mac in the garage | iOS's own verdict on the Wi-Fi path, Wi-Fi-only vs default fetches, AAAA behaviour, head unit address |

A CarPlay app wouldn't add anything: the CarPlay framework is UI templates only, and Apple grants the entitlement per app. No app can read the car's PSK (no API returns saved Wi-Fi passwords) or sniff packets.

## 1. Packet capture of the iPhone (rvictl)

The iPhone stays on the CarPlay Wi-Fi; the USB cable only carries the capture. Turn off Personal Hotspot and don't share the Mac's internet over USB, or the phone gets a second path.

```sh
xcrun devicectl list devices                      # UDID
rvictl -s <UDID>                                  # creates rvi0
sudo tcpdump -ni rvi0 -w secrets/iphone-p2.pcap   # all of the phone's interfaces
# ... start CarPlay, run the test ...
rvictl -x <UDID>
```

Then look for: ICMPv6 type 133 (RS) / 134 (RA), traffic to `captive.apple.com`, DNS to the RDNSS addresses, and which source address (SLAAC or 172.16.224.x) the phone uses. rvictl ships with Xcode on this Mac; whether it still captures on iOS 27 is part of the test.

## 2. iOS's own reasoning (logs)

For detail, install Apple's **Wi-Fi for iOS** logging profile from <https://developer.apple.com/bug-reporting/profiles-and-logs/> (Apple ID sign-in; the profile expires after a few days). Then, right after a test:

```sh
sudo log collect --device-udid <UDID> --last 15m --output secrets/iphone-p2.logarchive
log show --archive secrets/iphone-p2.logarchive \
  --predicate 'process IN {"symptomsd","wifid","configd","nesessionmanager"} OR subsystem == "com.apple.network"' \
  | grep -i -E 'captive|internet|router|rtadv|default route|probe' > secrets/iphone-p2-net.txt
```

Console.app does the same live: select the iPhone, Start streaming, filter on `process:symptomsd` or `subsystem:com.apple.network`.

## 3. CarNet Probe app

Install (Mac, Xcode, iPhone unlocked on the same Wi-Fi or on USB): `scripts/install-probe.sh`. Open it once and allow Local Network and Location.

| Section | What it shows | Read it as |
|---|---|---|
| Paths (live) | `NWPathMonitor` for any / Wi-Fi / cellular: status, IPv4/IPv6/DNS support, gateways | Wi-Fi path `satisfied` with `v6=true` and a gateway = iOS took our RA. "any" using `en0` = apps will use the car Wi-Fi |
| Addresses | Phone's own addresses, tagged car net / ULA / global v6 / link-local | Did it take the box's SLAAC prefix? |
| Probes (**Run probes**) | iOS's internet check bound to Wi-Fi (`wifi-bound`), then six URLs each fetched Wi-Fi only and the default way, with local/remote address | See "Reading the probes" below. `v4-only host` needs DNS64/NAT64 |
| DNS | `getaddrinfo` as apps do it (`AI_ADDRCONFIG`) | No AAAA answers on a ULA-only network = the ULA risk is real; rerun P2 with `PREFIX6=` |
| Bonjour | CarPlay/AirPlay services and their addresses on the car Wi-Fi | Head unit IPv4/IPv6 addresses |
| Watch | Internet check every 10 s, both ways, screen kept on | P4 exit test: how long until default flips back to cellular |
| Share (top right) | The whole log (`carnetprobe.log`) | Save to `secrets/`; also in Files/Finder under the app |

### Reading the probes

iOS decides which network carries ordinary traffic by ranking network services (configd). A service ranked **Never** or **Scoped** keeps its default route, but only as a *scoped* route: traffic bound to that interface gets through, nothing else does ([configd `SCNetworkConfigurationPrivate.h`](https://github.com/apple-oss-distributions/configd/blob/main/SystemConfiguration.fproj/SCNetworkConfigurationPrivate.h), [`ip_plugin.c`](https://github.com/apple-oss-distributions/configd/blob/main/Plugins/IPMonitor/ip_plugin.c)). The kernel agrees: it installs an RA's default router unscoped only on the default interface configd picked, and scoped everywhere else ([xnu `nd6_rtr.c`, `defrouter_select`](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/netinet6/nd6_rtr.c)), so an RA alone can't make the car Wi-Fi the default path. Wired CarPlay is configured `PrimaryRank = Never` ([`prefsmon.c`](https://github.com/apple-oss-distributions/configd/blob/main/Plugins/PreferencesMonitor/prefsmon.c)); wireless CarPlay very likely gets the same treatment, and "Check For Internet Access" is the obvious candidate for what lifts it. That's inference, not documented.

| `wifi-bound` | `wifi-only` | `default` | Means |
|---|---|---|---|
| ✅ | ✅ | ✅ | Done: apps use the car Wi-Fi |
| ✅ | ✅/❌ | ❌ | Network works; iOS keeps the Wi-Fi service out of the default path (rank Never/Scoped). Try the toggle; nothing on the box side will fix this |
| ❌ | ❌ | ❌ | Route not taken or broken: check Addresses (SLAAC?), the Wi-Fi path's `gw=`, and the Mac's `status` |

AAAA: configd only asks for AAAA records when a service has an IPv6 **default route**; it never checks whether the prefix is ULA, and Libinfo's `AI_ADDRCONFIG` counts any IPv6 address. So on Darwin a ULA prefix with a default route should get AAAA answers; the DNS row confirms it on the phone.

The Wi-Fi row (name, signal) stays "none" until the app gets the Access Wi-Fi Information capability: accept the latest Program License Agreement on developer.apple.com, then re-enable the entitlement block in `ios/CarNetProbe/project.yml`.
