# Response to the external review of status.md (2026-11-03)

An outside reviewer audited [status.md](status.md) against the RFCs, Apple's documentation and the Linux wireless docs. The reviewer didn't see the code. This file lists each finding, what we changed, and the evidence. **Changed** = code or docs changed and verified. **Agreed, open** = now an open item in [status.md](status.md). **Narrowed** = a claim reworded.

## P0

| Finding | Response | Evidence |
|---|---|---|
| **RA schedule not RFC 4861 compliant** ("every 1 s until a phone is there", "RS answered at once") | **Changed.** It was a real defect: 1 s multicast RAs while no phone was seen, and immediate RS replies. carplayd now sends every RA through one scheduler:<br>• periodic multicast RAs at a random 3–4 s (`ra_interval` = MaxRtrAdvInterval, at least 4 s);<br>• consecutive multicast RAs at least 3 s apart (MIN_DELAY_BETWEEN_RAS), whatever triggers them: periodic, RS from `::`, withdraw, resume or goodbye;<br>• RS from an address answered by unicast after a random 0–0.5 s (RFC 7772).<br>The 1 s multicast fixed a real problem (the phone's first RS is lost while its handshake finishes). It is replaced by treating a new device's MLD report as an implicit RS, answered the same way. `ra_eager` is now a config error | `carplayd/ra_sender.go`, `schedule.go`; unit test `TestRASchedulerRFC4861` (fake socket, real time). Emulator **ra-audit** captures every RS/RA on the box with tcpdump and checks them: min multicast gap 3.00 s, 0 spacing violations, every RS answered by unicast within 0.5 s ([results](emulator-results.md#round-3-after-the-review)) |
| **Router lifetime 0 doesn't withdraw DNS** | **Changed.** A withdrawal RA now carries router lifetime 0, RDNSS lifetime 0 and prefix preferred lifetime 0. The valid lifetime is kept: hosts ignore cuts below 2 h (RFC 4862 5.5.3e), and open connections can finish. The normal RDNSS lifetime is min(3 × MaxRtrAdvInterval, router lifetime) = 12 s, so DNS never outlives the route after an abrupt power loss | `raParams.withdrawn()`, `TestWithdrawnRA`, `TestDNSLifeBoundedByRouter`; ra-audit asserts every lifetime-0 RA on the wire also has RDNSS 0 and preferred 0 |
| **Upstream failure while the car link is healthy** | **Changed.** carplayd TCP-connects to the DNS servers it advertises (or `health_target`) every 3 s. After 2 failures it withdraws the route but **stays on the car**. The first success restores it. Known devices get the withdrawal by unicast at once, everyone else by the next multicast allowed by the rate limit | `health.go`; emulator **upstream-loss** (the fake ISP stops answering, wan0 stays up). The emulated host lost the route 6.3–7.1 s after the failure and got it back 1.9 s after recovery, with 0 leaves (3 runs) |
| — the box-originated check can pass while the phone's forwarding/NAT path is broken | **Agreed, partly open.** Firewall reloads are covered: carplayd re-applies its nftables table (NAT included) within 3 s if it vanishes. A forwarded-path probe (box → its own car-prefix address → NAT → internet) is an open item | status.md open item |
| — `accept_ra` vs forwarding trap on the uplink | **Checked on garage-sbc:** `all.forwarding=1`, kernel `eth0.accept_ra=0`, and NetworkManager processes RAs in userspace, so the default route (`proto ra`) stays. On OpenWrt the wan is static or handled by odhcp6c (userspace) | `ip -6 route show default` on garage-sbc, 2026-11-03 |
| **"Phone back on cellular" ≠ a simulated host losing its route** | **Narrowed.** Emulator rows now say "emulated host": default route installed / removed. Real-iPhone cellular recovery (time to the first new request over cellular) is open item 4 | [emulator-results.md](emulator-results.md) |

## P1

| Finding | Response |
|---|---|
| **IPv4/NAT64 on a dual-stack link** | **Agreed, open.** The design is "a dual-stack link: car-local IPv4, IPv6-only internet path", not an IPv6-only network. NAT64/DNS64 + PREF64 (RFC 8781) must be tested on this exact link: A-only names, IPv4 literals, explicit IPv4 sockets, apps with their own resolvers, Tailscale on/off, and the head unit's local IPv4 still working. Moved ahead of two-box coordination |
| **The PLA doesn't unlock RSSI** | **Narrowed (our error).** `NEHotspotNetwork.fetchCurrent` gives SSID, BSSID and security type with the Access Wi-Fi Information entitlement plus location permission. There's no supported API for current Wi-Fi RSSI. Open item reworded |
| **A heartbeat can't guarantee one router per car** | **Agreed.** The production plan is now **fixed ownership** (one box per car), with takeover only after a bounded silence, rather than dynamic preemption between boxes. Split-brain behaviour (both boxes advertise) is defined as acceptable-but-degraded: IPv6 allows two default routers, and each box uses its own prefix. Failure-injection tests are in the plan |
| **Car RSSI ≠ the phone's path** | **Agreed, open.** Phone ↔ box traffic is relayed by the head unit. Real-car runs will measure forwarded RTT/loss from the phone, CarPlay audio while downloading, phone locked/unlocked, in pocket/mounted, doors closed, and departure speed |
| **TKIP group ≠ negotiated pairwise; psk-mixed not proven necessary** | **Narrowed.** The scan shows what the car offers (WPA+WPA2, TKIP group, TKIP/CCMP pairwise). carplayd now logs the negotiated `key_mgmt`, pairwise and group ciphers and Wi-Fi generation on every join. The emulator negotiated CCMP pairwise with a TKIP group; the real car's values come from the next run |

## P2: claims narrowed

| Was | Now |
|---|---|
| CarPlay Wi-Fi is 2.4 GHz only | The tested F32 was observed on 2.4 GHz channel 11 |
| Any 2.4 GHz radio works | Needs verified STA support, the car's security, client-to-client IPv6 through the head unit, reliable reconnects |
| Head unit takes no address from ours; nothing conflicts | No head-unit SLAAC address was seen in the P2 capture; carplayd counts devices per prefix, so a change would show |
| The phone sends its 3 RSs only at link-up | The P2 capture showed RSs at link-up; carplayd doesn't depend on later ones (periodic RAs + MLD trigger) |
| Nobody published this before us | We found no matching public implementation in the searches listed in findings.md |
| *Use For Internet* lifts PrimaryRank | Hypothesis from Apple's open-source configd; not traced on the device |
| iOS 27 beta 3 introduced it | Per a betaprofiles forum post; not independently confirmed |
| SSID digits kept out of git | They weren't: car PSKs were in a test file and SSID digits in six files. Scrubbed (d654b97) and a pre-commit hook now blocks them. Earlier commits still contain them (see below) |

## Security and operations

| Criterion | Status |
|---|---|
| Car clients can't reach the box's management (SSH, web UI, metrics), the home LAN, or the other car | **Done** with carplayd's `inet carplayd` nftables table: the box accepts only ICMPv6 from the car link; forwarding to ULA, link-local, multicast, the uplink's on-link prefixes (/56) and any non-uplink interface is rejected. Emulator **isolation**: internet reachable, 0 of 6 forbidden targets reachable. **isolation-control** (firewall off): 5 reachable, so the probe can see leaks |
| Firewall reload/restart keeps the rules; repeated starts don't duplicate | **Done:** the table is replaced atomically (create, delete, recreate) and re-applied when missing or when the home prefix changes |
| One owner for the supplicant and RAs | carplayd owns both (its own wpa_supplicant; netifd and odhcpd are off the radio) |
| Crash tests | SIGKILL of carplayd and of its wpa_supplicant are emulator scenarios. Power loss, radio removal and firmware stall need real hardware: open |
| Phone count ≠ address count | **Done:** counted by unique MAC |
| Logs/captures: bounded, redacted | Open: the events file isn't rotated yet; captures stay in `secrets/` |
| Rollback | `deploy-carplayd-sbc.sh rollback` on garage-sbc (removes the nft table too). EWS377 rollback: open |
| Git history | The scrub fixed the tree, not the history; rewriting it (filter-repo + force push) is the user's call |

## What we didn't change

- **The 20 s router lifetime.** The reviewer agreed it's compatible with 3–4 s RAs; its adequacy under garage packet loss is an open real-car question.
- **Dynamic two-car arbitration on a single box** stays. The reviewer's split-brain point is about two boxes, now fixed ownership.

## Second review: the README (2026-11-04)

A second outside review of the README. It found no new defects; its main point was that the README equated "the box joined and sent the right packets" with "the iPhone had working internet and recovered". Changes:

| Finding | Response |
|---|---|
| No end-to-end evidence of phone recovery (graceful, power cut, sudden RF loss) | **Agreed, now first in "What's next"**, ahead of two-car work. The README separates six clocks (association, internet readiness, withdrawal delivery, routing recovery, app recovery, coverage gap); only association has real-car numbers |
| The coverage gap after leaving the box's range | Added: to map along the real exit route; routing can't fix it |
| Health checked from the box, not the forwarding path | Already open; now stated in the README |
| IPv4 is a product limitation, not an enhancement | README says Google Maps only so far; IPv4-only services don't work until NAT64. NAT64 test matrix listed (A-only names, IPv4 literals, own resolvers, VPNs, CarPlay's local IPv4 kept local). PREF64 lifetimes are in 8 s units, so the rounding will be documented |
| Head unit might use our route | Added as an open item; no head-unit address in the proof-of-concept capture |
| "Route expires after 20 s" isn't a 20 s handoff | Reworded: it bounds the stale default route only, not DNS, app retries, VPN reconnects or the first cellular request |
| Withdrawal ≠ all IPv6 state gone | README now has the intended post-withdrawal state per item (route, RDNSS, deprecated address, open connections, return) |
| "83 connections", "< 0.1 s" undefined | Defined in the README: NAT-rule first packets (connections, not successful requests); join time from first sighting at or above the threshold to association |
| Isolation needs a threat model | Scoped wording: only ICMPv6 accepted from the car link; home prefixes blocked even when public; NAT64, existing conntrack entries and OpenWrt reload paths listed as not covered; phone ↔ head-unit traffic out of scope |
| Claims to narrow ("probably", "everything else", "nothing conflicts", "standards-compliant", "new in iOS 27") | All narrowed as suggested |
| Radio behaviour (RSSI vs phone path, asymmetry, multicast while locked, scan disruption, contention) | Folded into the end-to-end sessions and the two-car tests; "both cars at once" = one radio, one car served at a time |
