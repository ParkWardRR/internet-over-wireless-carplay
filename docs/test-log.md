# Test log

One row per test. Put the PSK, VIN, SSID digits and MACs in `secrets/`, never here.

| Date | Phase | Setup | Result | Notes |
|---|---|---|---|---|
| 2026-11-01 | P0 | iPhone → Settings → Wi-Fi → car network | CarPlay page: no IP/router/DNS fields, no password field. **Weak Security** label. Has a **Check For Internet Access** button + Manage CarPlay | Next: Passwords app → Wi-Fi, and Settings → Wi-Fi → Edit → Known Networks |
| 2026-11-01 | — | Read the Bimmerfest thread | Car net 172.16.224.0/24, no gateway; 2nd client OK; LTE router at .254 + manual iPhone IP worked (2022); iOS shows the PSK since ~2025 | Script now also offers 172.16.224.254 + NAT44 |
| 2026-11-01 | — | Re-checked sources (Bimmerfest now bot-walled; used the gearshifters copy, Apple forum 76449, WWDC17 #717, Audi TSB, odhcpd/OpenWrt source) | Subnet, no gateway, 2nd client, 10-char PSK over Bluetooth confirmed. "iOS shows the PSK since 2025" **not** confirmed. Car announces "no internet" in its beacon (Apple Device IE). odhcpd lifetime facts corrected. Mac keychain has no CarPlay network | Docs + script updated: car-subnet guard, `PREFIX6` routed-prefix mode, routermode, forwarding restore |
| 2026-11-01 | — | CarNet Probe on the iPhone (iOS 27.0.1), home Wi-Fi baseline | All probes OK both ways; `wifi-bound` 38 ms Success; AAAA returned; gateways reported. Log in `secrets/probe-home-baseline.log` | App works on the device; Wi-Fi name blank until the Wi-Fi-info entitlement |
| 2026-11-01 | — | Read Apple's configd / Libinfo / mDNSResponder / xnu source | Rank `Never`/`Scoped` service gets only a scoped default route (wired CarPlay = `Never`); kernel installs the unscoped default only on configd's default interface; AAAA requested for any IPv6 service with a default route, ULA or not | The likely failure mode is iOS ranking, not the box. Added the `wifi-bound` probe to tell them apart |
| 2026-11-02 | — | Google search via SerpAPI (~20 queries, EN/DE/ZH), DiPlay issue tracker, iOS 27 beta notes | iOS 27: Check For Internet Access is a button; a passing test reveals **Use For Internet**. DiPlay (non-factory receiver) gets it; iOS re-checks on its own. Nobody has published a separate box on a factory CarPlay network working | Biggest risk narrowed to: does the check pass over IPv6 only? |
| 2026-11-02 | — | Read the Bimmerfest thread in full via the r.jina.ai reader proxy; ~10 more searches (BMW forums, Reddit, JA), GitHub code search | **Confirmed success on NBT EVO (Feb 2022)**: Teltonika RUT850 on the CarPlay Wi-Fi at .254, iPhone manual .5 / gw+DNS .254 → internet with CarPlay running. Jan 2025: PSK visible in the iPhone's Wi-Fi properties. No other reports | The `.254` alias + NAT44 in `poc-mac-nat.sh` reproduces that setup if iOS 27 shows Configure IP |
| 2026-11-02 | P0 | iPhone (iOS 27) → Settings → Wi-Fi → Edit → Known Networks → CarPlay network | **Password shown, with Copy** (no jailbreak). Full settings page incl. Private Wi-Fi Address and **Configure IP**. The phone also knows a second BMW CarPlay network (another car) | PSK in `secrets/carplay.env`. Manual IPv4 (0b) is possible on iOS 27 |
| 2026-11-02 | P1 | garage-sbc `iw` scan of the car AP | **2.4 GHz only**, ch 11, HT20 1-stream; WPA+WPA2 mixed, TKIP group (hence "Weak Security"). **802.11u Interworking: Internet bit 0**, Private, venue group Vehicular. **No Apple Device IE** in the beacon. Head unit = AP at `172.16.224.1` | The car signals "no internet" with Interworking, not the Apple IE |
| 2026-11-02 | P2 | garage-sbc in the car, `poc-linux.sh up` (ULA /64, radvd, NAT66 out of eth0) | **Works.** Car DHCP gave the box `.127` (2nd client OK). Head unit sends no RAs. iPhone (iOS 27.0.1) took a SLAAC address in the ULA prefix; tapping **Check For Internet Access** offered **Use For Internet**; Google Maps search + route on CarPlay worked. 81 NAT66 connections, 11.5 MB to the phone, 0 NAT44. No phone IP settings changed | IPv6 RA route confirmed. Next: P4 exit test, rejoin after a car restart, permanent setup |
| 2026-11-02 | P2 | Same session, from the SBC: 45 s capture of the iPhone's frames, pings, `down` | iPhone traffic **134 of 135 frames IPv6** (the one IPv4 frame was mDNS); TCP 443, QUIC, **Tailscale (UDP 41641) and STUN connected through the box**. iPhone's car-net IPv4 is `.110`. Ping SBC→iPhone 0 % loss, 38 ms avg (power save spikes to 157), SBC→head unit 10 ms, SBC→internet 12 ms. Link −73 dBm, 39/65 Mbit/s. **Goodbye RA works**: radvd's last RA on `down` had router lifetime 0 s, sent before leaving the Wi-Fi. Teardown clean; label-printer services untouched | `down` now pauses 1 s after stopping radvd so the goodbye RA goes out |
| 2026-11-03 | P3 | garage-sbc moved to the garage; `carplay-box.sh install` | NM connection `carplay-car` (autoconnect, unlimited retries), dispatcher hook + `carplay-radvd.service` installed; config and hook syntax checked. Car off, so not yet exercised | Next car start should bring it up hands-free: `journalctl -t carplay-box` |
| 2026-11-03 | review | External review of status.md ([review-response.md](review-response.md)); secrets audit | Car PSKs (a test file) and SSID digits (6 files) were tracked: scrubbed, pre-commit guard added | Git history still holds them (open item) |
| 2026-11-03 | review | carplayd unit tests incl. `TestRASchedulerRFC4861` (fake socket, real time) | Pass: unicast RS answer 0.47 s; withdraw/resume unicast at once; multicasts ≥ 3.0 s apart; final multicast router/RDNSS lifetime 0 | |
| 2026-11-03 | emulator r3 | 17 carplayd scenarios × 3 on build-host, incl. new upstream-loss, isolation, isolation-control, ra-audit | Every booted run passed. ra-audit: min multicast gap 3.00 s, RS answered by unicast ≤ 0.35 s, 0 incomplete withdrawals. Upstream loss: route gone after 6.3–7.1 s, back after 1.9 s, box stays on the car. Isolation: 0/6 leaks (5/6 with the firewall off) | Miss: car B join 1.78 s in 1 of 3 runs ([results](emulator-results.md#round-3-after-the-review)) |
| 2026-11-03 | garage-sbc | Uplink with forwarding on | `all.forwarding=1`, `eth0.accept_ra=0`; NetworkManager keeps `default via fe80::… proto ra`, so the forwarding/accept_ra trap doesn't apply. nft wasn't installed | Deploy installs nftables |
| 2026-11-03 | garage-sbc | carplayd redeployed (round-3 code) in survey mode (`survey = 10s`); event log moved to the SD card (Armbian's `/var/log` is zram); logrotate daily, 14 days | Scans confirmed: channel-11 entries ≤ 1 s old, the sweep reaches channel 1; firewall table applied; NAT66 moved from ip6tables to nft; upstream ok | Collecting until the next car session |

## P0 — iPhone, car network

| Field | Value |
|---|---|
| SSID pattern | `BMW<5 digits> CarPlay` |
| Security | "Weak Security" (WPA/TKIP or WPA/WPA2 mixed, to confirm in P1) |
| IP settings | not shown for CarPlay networks |
| Password revealable? | not on the CarPlay page; **yes** via Edit → Known Networks (with Copy) |
| Settings → Wi-Fi → Edit → ⓘ: listed? Password? Configure IP? | listed; password yes; Configure IP yes |
| Passwords app → Wi-Fi: listed? | |
| Check For Internet Access | button (iOS 27); tap it in P2 with the box running |
| Tapping it with no box: what does iOS show? | |
| Head unit | factory NBT EVO |
| iOS version | |

Offline maps: download the area in Google Maps (and Apple Maps), go to the garage, Airplane mode + Wi-Fi to the car only, try search + route.

## P1 — car AP from the Mac

Car on, Mac near the car:

```sh
sudo wdutil info                     # current link
system_profiler SPAirPortDataType | sed -n '/Other Local Wi-Fi Networks/,$p' | grep -A8 -i carplay
```

Record band, channel (DFS?), PHY mode, security (WPA/WPA2, TKIP/CCMP), BSSID OUI. `system_profiler` lists channel, band, PHY mode and security per network (checked on macOS 27); the old `airport -s` tool is gone.

The band decides the box: a 2.4 GHz-only travel router can't join a 5 GHz car AP.

Then read the car's beacon flags: `scripts/car-ie.sh` (allow Location for CarIE on first run; macOS hides raw IEs otherwise; `--selftest` checks the decoder). It prints the Apple Device IE flags and the Interworking element.

| Field | Value |
|---|---|
| Band / channel / security | |
| Apple Device IE flags (hex + names) | |
| `InternetAccess` (bit 11) | |
| Interworking element present? Internet bit? | |

## P2 on garage-sbc (Banana Pi in the car)

`garage-sbc.local` (Banana Pi, Allwinner sun55iw3, Debian 12, NetworkManager) is the test box: `eth0` wired to the garage LAN, onboard Broadcom Wi-Fi (`wlan0`, dual-band) joins the car. It also runs the Brother label printer services; the test touches none of them. `scripts/poc-linux.sh` is deployed to `~/carplay-poc/` with `carplay.env` (0600). radvd and tcpdump are installed, radvd disabled at boot. Drive it over SSH from the Mac:

```sh
ssh user@garage-sbc.local
cd ~/carplay-poc
sudo ./poc-linux.sh ie            # P1: band, cipher, Apple Device IE flags (InternetAccess bit)
sudo ./poc-linux.sh up            # join BMW… CarPlay, radvd + NAT66, .254 + NAT44
sudo ./poc-linux.sh capture 120   # passive: head unit RAs? DHCP server address?
sudo ./poc-linux.sh status        # iPhone's SLAAC address, NAT packet counters
sudo ./poc-linux.sh down          # undo everything
```

Then the iPhone steps from `up`'s output: IPv6 first (tap Check For Internet Access → Use For Internet), then the manual IPv4 setup, then back to Automatic. Run CarNet Probe for each and log the results in the P2 table below.

## P2 — MacBook proof of concept (IPv6 RA)

1. `cp secrets.example.env secrets/carplay.env` and fill in the SSID + PSK.
2. Plug the USB-C Ethernet adapter into the garage LAN (needs a 172.16.x address and global IPv6).
3. Look before talking. Join the car Wi-Fi by hand, start CarPlay, and capture for two minutes: `sudo tcpdump -ni en0 -w secrets/p2-passive.pcap 'icmp6 or udp port 5353 or udp port 67 or udp port 68'`. Answers: does the head unit send RAs of its own (ICMPv6 type 134)? Which IPv6 addresses does CarPlay use (link-local expected)? What is the head unit's IPv4 address (the docs assume `172.16.224.1`)?
   Watch the phone's side at the same time: `rvictl` capture and iOS logs, see [iphone-diagnostics.md](iphone-diagnostics.md). Run **CarNet Probe** before step 4 for a baseline.
4. `sudo scripts/poc-mac-nat.sh up`. It joins the car Wi-Fi, adds `fd5c:ba1e:f32:1::1/64`, starts `rtadvd` (60 s router lifetime, RDNSS 2001:4860:4860::8888 + 2606:4700:4700::1111) and NAT66 out of the Ethernet. It stops before touching anything if the Mac's Wi-Fi address isn't in 172.16.224.0/24, because home is 172.16.x too and a failed join would otherwise advertise the route on home Wi-Fi.
5. Start CarPlay. iPhone: Settings → Wi-Fi → car network → tap **Check For Internet Access**. If **Use For Internet** appears, turn it on.
6. `sudo scripts/poc-mac-nat.sh status`. Success looks like the iPhone listed with an `fd5c:ba1e:f32:1:` address and NAT66 states > 0.
7. Google Maps on CarPlay: search + route. Record: does it work, latency, CarPlay stable? Then CarNet Probe → **Run probes**, and share the log into `secrets/`.
8. `sudo scripts/poc-mac-nat.sh down`.

| Check | Result |
|---|---|
| Mac joined car SSID (2nd client allowed) | |
| Mac got car DHCP IPv4 | |
| iPhone took the SLAAC address (no client isolation, RA accepted) | |
| NAT66 states from the iPhone | |
| Check For Internet Access passed? Use For Internet appeared? | |
| Maps search + route over CarPlay, Use For Internet **on** | |
| Same with it **off** | |
| Password field visible on the network page once Use For Internet shows? | |
| If ULA failed: same with `PREFIX6=` a routed /64 | |
| Head unit sends its own RAs? (passive capture) | |
| CarNet Probe: Wi-Fi path status / gateways / "any" path interface | |
| CarNet Probe: probes Wi-Fi-only vs default (captive, google maps) | |
| CarNet Probe: AAAA answers on the ULA network? | |
| iOS logs: reason for internet / no internet on the car Wi-Fi | |
| Head unit took an `fd5c:` address? (`status` flags it) | |
| Apple Maps search (some hosts IPv4-only) | |
| CarPlay stayed connected | |

If the iPhone takes the address but still says no internet: try options B and C in [ipv4-options.md](ipv4-options.md).

### DHCP timing (decides whether the IPv4 race is winnable)

The car owns IPv4: its head unit is the DHCP server and sends no router option, and iOS hides manual IP settings for CarPlay networks. The only IPv4 way in is answering the phone's DISCOVER faster than the car. Measure the car's reply time while the Mac is on the car Wi-Fi:

```sh
sudo tcpdump -ni en0 -tttt 'udp port 67 or udp port 68'
# then on the iPhone: Wi-Fi off/on (or disconnect/reconnect CarPlay)
```

| Measure | Value |
|---|---|
| DISCOVER → car OFFER (ms) | |
| REQUEST → car ACK (ms) | |
| Lease time, server identifier (`172.16.224.1`?) | |
| Does the phone use INIT-REBOOT (REQUEST without DISCOVER) on rejoin? | |

Under a few ms from the car means a box rarely wins; tens of ms or more makes a race plausible.

## P4 — exit test

Drive out with the box running. Time from leaving the garage to Maps updating over cellular (router lifetime is 60 s). Leave CarNet Probe open with **Watch** on: its log timestamps when the default path leaves the car Wi-Fi.
