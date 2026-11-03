# Emulator results (2026-11-03)

[emulator.md](emulator.md) explains the setup: the box's own OpenWrt snapshot firmware (r38011, kernel 6.18.60) under KVM on build-host, with mac80211_hwsim radios, two fake NBT EVO cars, Linux phone stand-ins and a fake internet. Every repetition boots a fresh VM. Up to three VMs ran in parallel (build-host load ~4–5), so timings carry some noise.

The two boxes compared:
- **stock**: netifd + odhcpd + fw4 configured by `scripts/openwrt-carplay.sh`, which is what the EWS377 would run without carplayd;
- **carplayd**: `carplayd/` owning the radio, with fw4 doing only NAT66.

**Read the numbers as comparisons, not absolutes.** hwsim scans are near-instant and there's no RF. The "phone" is a Linux stand-in host, not iOS: "route" means that host installed or removed the box's default route, not that an iPhone moved to or from cellular. Time zero is the harness command (hostapd started, signal changed, link cut).

## Headline

| What | stock | stock + `scan_list` | **carplayd** |
|---|---|---|---|
| Car starts → box associated (box already hunting, random phase), median / p90 | 1.47 / 1.63 s | 1.15 / 1.22 s | **0.39 / 0.59 s** |
| Same over 25 cycles (stock) / 60 cycles (carplayd soak), median / max | 1.61 / 1.97 s | n/a | **0.52 / 1.06 s** |
| Emulated host associated → default route (box already serving), median / p90 | 3.83 / 6.72 s | 6.86 / 6.86 s | **0.32 / 0.35 s** |
| Box joins late (host's RS already spent) → host route | 4.25 s | n/a | **0.01 s** |
| Phone route → internet check passes | 0.15 s | 0.15 s | 0.19 s |
| Car fades (3 dB/s) below the leave threshold → emulated host removes the route | never: the box keeps the link and keeps advertising | | **3.8–4.8 s** (3 s `leave_for` + goodbye) |
| Car cut off abruptly → host's route expires | 60.5 s (router lifetime 60) | | **20.0 s** (router lifetime 20; no goodbye can arrive) |
| Car back in range → box associated | 1.33 s | | **0.25 s** |
| Signal back above the rejoin threshold → phone route | n/a | | **0.65–1.01 s** |
| Car B starts after car A drove off → box associated / phone B route | stock serves one car | | **0.88 s / 0.82 s**, on car B's own prefix |
| Both cars start together → the priority car (A) is served | n/a | | **5/5**; when B's beacon came first, preempted to A in 0.84 s |
| Signal hovering around the leave threshold for 30 s | n/a | | **0 leaves, 0 re-joins** (needs 3 s continuously below) |
| carplayd's wpa_supplicant killed → serving again | n/a | | **3.0 s** (PING/PONG watchdog) |
| carplayd killed and restarted → serving again | n/a | | **0.35 s** |

Runs: 33 stock and 82 carplayd scenario repetitions. Every repetition completed; no crashes.

## Bugs the emulator found in carplayd (fixed)

| Found by | Bug | Fix |
|---|---|---|
| two-cars | After car A left, carplayd **re-selected car A twice** before car B: wpa_supplicant keeps departed APs in `SCAN_RESULTS` for minutes | `bss_expiration_scan_count=1`, `bss_expiration_age=10`: one missed scan of its channel and the AP is gone. Car B: 1.29–1.74 s → 0.88 s |
| two-cars, detect | A phone's **first Router Solicitation is lost** when it's sent before the WPA handshake completes; the phone waited for the 3 s periodic RA | RAs every `ra_eager` (1 s) until a phone is seen (2.7 s → 0.82 s). **Superseded in round 3:** that broke RFC 4861 spacing; a new device's MLD report now triggers a unicast RA within 0–0.5 s |
| both-on | **Whichever car's beacon came first won**, ignoring priority | A higher-priority car preempts within 15 s while no phone is on the served car (grace scans every 2 s) |
| garage-sbc deploy | Metrics port 9101 already taken (the label printer's dashboard); the listener **failed silently** | Bind up front, exit with an error; garage-sbc uses 9111 |
| first soak attempt | **Unknown-channel sweeps** (all of 2.4 GHz, ~3.9 s on brcmfmac) would slow detection of the known car | Sweep every 5th hunt tick only |

## Things the emulator showed about the stock stack

- **`scan_list`** (the car's channel only) cuts stock detection by ~0.3–1 s. Useful on the EWS377 even without carplayd.
- **odhcpd takes 3.8–6.9 s** to give a newly joined phone its route, and 4.2 s when the box joins after the phone. It waits for the link-local address to stop being tentative, uses a fixed 1 s first-RA timer, and evidently misses or ignores early RSs. carplayd answers immediately.
- **A 60 s router lifetime** means a phone keeps a dead route for a minute after an abrupt drive-out. carplayd uses 20 s, and sends a goodbye whenever the link is still there.

## Not covered here (needs real hardware or a real iPhone)

- **RF:** real scan dwell (ath11k ~50 ms per channel, brcmfmac ~40–150 ms), signal through car glass, the actual leave/rejoin thresholds at the parking spots. Survey these from garage-sbc and the EWS377.
- **iOS:** *Check For Internet Access*, *Use For Internet* and whether it's remembered across car restarts; iOS's RS timing and internet check. These need the real phone in the real car.
- **NBT EVO specifics** beyond what P1 measured: how fast the head unit's AP comes up after ignition, and whether the F22 matches the F32.

## Reproduce

On build-host:
```sh
emu/run.sh build
emu/run.sh test -box stock -n 5
emu/run.sh test -box carplayd -n 5
```
Raw JSON/MD/logs: `emu/out/results/` (round 1 archived in `~/builds/carplay-results-archive/`).

## Round 3: after the review

Same emulator, carplayd changed per [review-response.md](review-response.md): RFC 4861 RA scheduler, full withdrawal, upstream health gating, nftables isolation. 17 carplayd scenarios × 3 runs, three VMs in parallel on build-host (2026-11-03). Every run that booted passed its assertions; one soak VM didn't boot (infrastructure, rerun pending).

| Emulated event | round 2 | **round 3** | target |
|---|---|---|---|
| hostapd started → box associated, median (min–max) | 0.39 s | **0.71 s** (0.50–1.10) | 1.5 s ✅ |
| Host associated → default route, median (min–max) | 0.32 s | **0.47 s** (0.45–0.76) | 4 s ✅ |
| Box joins late → host route | 0.01 s | **0.01 s** | |
| Signal below leave threshold → host removes the route | 3.8–4.8 s | **3.7–4.1 s** (unicast withdrawal goes out before the final multicast) | 5 s ✅ |
| Car cut off → route expires | 20.0 s | **20.0–20.7 s** | 21 s ✅ |
| Back in range → box associated | 0.25 s | **0.21 s** | 1.5 s ✅ |
| Car B starts → box associated | 0.88 s | **0.91 s** (0.89–1.78) | 1.5 s ❌ one run of three |
| wpa_supplicant killed → associated again | 3.0 s | **2.7 s** | |
| carplayd restarted → serving | 0.35 s | **0.39 s** | |
| Hovering signal, 30 s | 0 leaves | **0 leaves** | |
| **New:** upstream dies (wan up, ISP silent) → host loses the route | – | **6.3–7.1 s**, 0 leaves | 10 s ✅ |
| **New:** upstream back → host route | – | **1.9 s** | 4 s ✅ |
| **New:** host reaches internet / box, home LAN, other car | – | **2 of 2 / 0 of 6** (firewall off: 5 of 6, so the probe sees leaks) | 0 leaks ✅ |
| **New, ra-audit** (tcpdump on the box): closest multicast RAs | – | **3.00 s**, 0 violations | ≥ 3 s ✅ |
| **New, ra-audit**: RS → unicast answer, worst | – | **0.13–0.35 s**, every RS answered | ≤ 0.5 s ✅ |
| **New, ra-audit**: withdrawal RAs with RDNSS or preferred lifetime > 0 | – | **0** | 0 ✅ |

The host's route now takes ~0.15 s longer: the 1 s multicast stream is gone, and the stand-in's first RS is lost in every run, so the MLD-triggered unicast answer is what delivers it. The car-B outlier is association time, which the RA changes don't touch; watch it on real hardware.
