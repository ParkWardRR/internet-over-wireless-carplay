# bmw-f32-carplay-wan

Give the iPhone internet while it's on **wireless CarPlay** in the basement garage, where there's no LTE/GSM.

## Result: it works (2026-11-02)

A small Linux box in the car (garage-sbc, a Banana Pi, wired to the garage LAN) joins the car's CarPlay Wi-Fi as a second client and advertises an **IPv6 route + DNS** with NAT66 to the home connection. On the iPhone (iOS 27): Settings → Wi-Fi → the CarPlay network → **Check For Internet Access** → **Use For Internet**. Google Maps search and routing then work on CarPlay underground. Nothing on the car changes, and no IP settings change on the phone.

**Full write-up: [docs/findings.md](docs/findings.md).** Raw results: [docs/test-log.md](docs/test-log.md). Run it: `scripts/poc-linux.sh` (see *P2 on garage-sbc* in the test log).

```mermaid
flowchart LR
  P[iPhone<br/>Use For Internet ON] -- "CarPlay Wi-Fi 2.4 GHz<br/>(head unit is the AP)" --> HU[NBT EVO head unit<br/>172.16.224.1]
  B[Box in the car<br/>Wi-Fi client of the car AP] -. "IPv6 RA:<br/>prefix + 60 s route + DNS" .-> P
  P -. "IPv6 traffic" .-> B
  B -- "garage Ethernet + NAT66" --> L[Home LAN<br/>native IPv6]
  L --> W((Internet))
```

## The problem

Wireless CarPlay puts the iPhone's Wi-Fi on the car's own access point (`BMW<5 digits> CarPlay`). That network hands out an address but no router and no DNS, and its beacon says "no internet". So the phone keeps cellular as its internet path, and underground there's no cellular: Google Maps can't search or route until you drive out.

## Key facts

| Fact | Source | Checked |
|---|---|---|
| **The car's CarPlay Wi-Fi**: 2.4 GHz only (channel 11, HT20, one stream), WPA/WPA2 mixed with a TKIP group cipher (iOS calls it "Weak Security"). The beacon's **802.11u Interworking element has the Internet bit clear** (Private, venue Vehicular). No Apple Device IE | `iw` scan from the box | ✅ 2026-11-02 |
| DHCP gives 172.16.224.0/24 with **no router and no DNS**. Head unit = AP at `172.16.224.1`, sends **no IPv6 RAs**. A second client is allowed (box got `.127`, iPhone `.110`). The car's LTE hotspot (if fitted) is a separate 172.16.222.x network | box; [gearshifters](https://gearshifters.org/bmw/how-to-find-bmw-carplay-password/) copy of the Bimmerfest post; [Apple forum 76449](https://developer.apple.com/forums/thread/76449) | ✅ |
| SSID digits = last 5 of the VIN. PSK = 10 random alphanumerics, sent to the phone over Bluetooth. **iOS 27 shows it, no jailbreak**: Settings → Wi-Fi → Edit → Known Networks → the CarPlay network → Password | the iPhone | ✅ 2026-11-02 |
| **iOS 27**: the CarPlay network page hides IP settings and shows a **Check For Internet Access** button. If the network passes, a **Use For Internet** switch appears ("your iPhone will also use your vehicle's hotspot to access the internet. If the hotspot loses connectivity, the iPhone will use its cellular connection"). The Known Networks page still has **Configure IP** | the iPhone; [betaprofiles iOS 27 beta notes](https://forum.betaprofiles.com/t/whats-new-in-ios-27-beta-3/23195?page=2); [DiPlay #393](https://github.com/shihabal3amri/DiPlay/issues/393) | ✅ |
| **Prior art on this head unit (Feb 2022)**: ksweber put a Teltonika RUT850 LTE router on the CarPlay Wi-Fi at `.254` and set the iPhone to manual `.5`, gateway + DNS `.254`: "Finally the iPhone providing Carplay with the NBT head unit was able to fetch internet". Nobody else found doing it; the IPv6 / iOS 27 route appears new | [Bimmerfest thread](https://www.bimmerfest.com/threads/nbt-evo-carplay-wifi-password.1376809/) (read via `r.jina.ai`); ~30 searches in 4 languages | ✅ |
| Two kinds of wireless CarPlay car. **Wi-Fi pairing**: CarPlay over the car's normal hotspot, so the phone uses hotspot data. **Bluetooth pairing**: a private CarPlay-only network with no internet. BMW's NBT EVO is the second kind | Audi TSBs [2021](https://static.nhtsa.gov/odi/tsbs/2021/MC-10189148-0001.pdf), [2024](https://static.nhtsa.gov/odi/tsbs/2024/MC-10250567-0001.pdf) | ✅ |
| Why a CarPlay network normally carries no internet: configd gives a service ranked `Never` only a scoped default route (wired CarPlay is `Never`); *Use For Internet* lifts it. Darwin asks for AAAA on a ULA-only network, so no public prefix is needed | Apple's open-source configd, Libinfo, xnu ([iphone-diagnostics.md](docs/iphone-diagnostics.md#reading-the-probes)) | ✅ |
| Home LAN is 172.16.0.0/16, which overlaps the car's 172.16.224.0/24. Harmless: nothing at home uses .224.x, the box NATs, and the scripts refuse to start unless they really got a car address | Kea config | |
| Home has native IPv6 (Spectrum); Google Maps answers over IPv6. A few hosts are IPv4-only (`gsp1.apple.com`, push, Mapbox): DNS64 + NAT64 would cover them | AAAA lookups ([ipv6-route.md](docs/ipv6-route.md)) | ✅ |

## Approaches

| # | Approach | Cost | Touches car | Status |
|---|---|---|---|---|
| **1** | **Box in the car on the CarPlay Wi-Fi, IPv6 RA + RDNSS, NAT66 to garage Ethernet.** No phone settings; the route expires 60 s after the car leaves | a Linux SBC or ~$70–90 travel router | no | ✅ **works** (P2) |
| 0b | Manual IPv4: box at `.254` with NAT44, phone Configure IP → Manual `.5` / router `.254`. `poc-linux.sh` already holds `.254` | same box | no | proven by ksweber (2022); not needed, and the static router setting outlives the garage |
| 1b | USB-C Ethernet on the phone, cabled to a travel router on the garage Wi-Fi | ~$50 | no | untested backup; needs no PSK |
| 0 | Offline maps: Apple Maps with *Only Use Offline Maps* ([105084](https://support.apple.com/en-us/105084)); Google Maps offline areas ([6291838](https://support.google.com/maps/answer/6291838?hl=en&co=GENIE.Platform%3DiOS)) | $0 | no | works for known places, no live traffic |
| 2 | Cellular booster (weBoost ~$570, Cel-Fi GO ~$1,300; femtocells mostly discontinued) | $570–1,400 + install | no | fallback |
| 3 | OBD-II ENET recon ([obd-enet.md](docs/obd-enet.md)) | ~$15 | read-only | exploration |
| 4 | Root the head unit | time + risk | yes | not planned |

## Plan

| Phase | Do | Status |
|---|---|---|
| **P0** | iPhone checks: CarPlay page, PSK, Configure IP | ✅ PSK and Configure IP via Edit → Known Networks |
| **P1** | Read the car AP: band, security, beacon flags (`poc-linux.sh ie`) | ✅ 2.4 GHz ch 11, WPA/WPA2 TKIP, Interworking Internet = 0 |
| **P2** | Proof of concept: garage-sbc in the car, `poc-linux.sh up`; Check For Internet Access → Use For Internet; Maps | ✅ works; traffic, latency and goodbye RA checked |
| **P3** | Permanent box. **Interim: garage-sbc in the garage, automatic** (`scripts/carplay-box.sh`: NM auto-joins the car Wi-Fi, a dispatcher hook starts radvd + NAT). **Final: an OpenWrt router** ([docs/openwrt-box.md](docs/openwrt-box.md), `scripts/openwrt-carplay.sh`) | **carplayd** running on garage-sbc since 2026-11-03 (replaced carplay-box.sh); OpenWrt/EWS377 to do |
| **L / P** | **Lab telemetry, then fast connect / leave / reconnect for one or both cars** on the EnGenius EWS377AP (OpenWrt, IPQ8072A): [docs/fast-connect-plan.md](docs/fast-connect-plan.md) | planned |
| **2C** | **Two cars** (F32 + F22): per-car ULA, no IPv4 on car links, carplayd, build-host hwsim simulator, F22 survey from garage-sbc ([docs/two-cars.md](docs/two-cars.md)) | carplayd handles both; F22 channel + survey pending |
| **P4** | Exit test: drive out, time the return to cellular (60 s router lifetime) | to do |
| **P4b** | Restart test: does iOS remember *Use For Internet* across car restarts? | to do |
| **P5** | OBD ENET exploration | optional |

Car: factory NBT EVO (wireless CarPlay only). Garage: Ethernet, home Wi-Fi and power at the parking spot.

## Getting the CarPlay Wi-Fi password (no jailbreak)

On iOS 27 it's under **Settings → Wi-Fi → Edit → Known Networks → `BMW… CarPlay` → Password** (Face ID, with a Copy button). It's stored in `secrets/carplay.env`. If a future iOS hides it, other routes are:

- the Passwords app → Wi-Fi;
- Apple's *Share Password* to a Mac that tries to join the network ([102635](https://support.apple.com/en-us/102635)). `poc-mac-nat.sh` works without a PSK if the Mac is already joined;
- an encrypted local backup, last resort.

Sniffing it is out (10 random characters) and so is Bluetooth (encrypted).

## Repo

| Path | What |
|---|---|
| [docs/findings.md](docs/findings.md) | What we learned and why it works |
| [docs/fast-connect-plan.md](docs/fast-connect-plan.md) | Lab telemetry + fast connect/leave/reconnect plan (EnGenius OpenWrt) |
| [docs/two-cars.md](docs/two-cars.md) | F32 + F22: scenarios, per-car prefixes, carplayd, radios, arbitration, build-host simulator |
| [docs/test-log.md](docs/test-log.md) | Every test, dated |
| [docs/ipv6-route.md](docs/ipv6-route.md) | IPv6 design, reachability, permanent-box recipe |
| [docs/iphone-diagnostics.md](docs/iphone-diagnostics.md) | rvictl capture, iOS logs, CarNet Probe |
| [docs/ipv4-options.md](docs/ipv4-options.md) | IPv4 alternatives |
| [docs/obd-enet.md](docs/obd-enet.md) | OBD ENET recon |
| `scripts/poc-linux.sh` | The working setup on a Linux box (`ie`, `up`, `capture`, `status`, `down`) |
| `scripts/carplay-box.sh` | garage-sbc hands-off mode (`install`, `status`, `uninstall`) |
| `scripts/openwrt-carplay.sh`, [docs/openwrt-box.md](docs/openwrt-box.md) | The OpenWrt replacement |
| `scripts/poc-mac-nat.sh` | Same on a Mac (rtadvd + pf) |
| `scripts/car-ie.sh`, `scripts/car-ie.swift` | Beacon decoder for the Mac |
| `ios/CarNetProbe`, `scripts/install-probe.sh` | iPhone diagnostics app |

## Rules

- Never commit the car's PSK, VIN, SSID digits (they're the end of the VIN), MACs or keys. Put them in `secrets/` (gitignored), captures included.
- Car work is read-only unless a step says otherwise: no coding, no flashing.
- Log every test in [docs/test-log.md](docs/test-log.md): date, firmware, what was run, result.
