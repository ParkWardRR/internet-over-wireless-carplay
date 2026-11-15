# AP signalling: can a beacon tell iOS the car network has internet?

*Design note, 2026-11-13. Nothing in this file has been measured yet.* It answers a third outside review (the first two are in [review-response.md](review-response.md)) which proposed building a replacement access point that advertises internet capability, in the hope of removing the **Use For Internet** tap that iOS currently demands after every car restart.

The review's own conclusion is the one adopted here: **this is a staged research project with stop/go gates, not a replacement for carplayd.** The gates below are ordered so the cheapest thing that could kill the idea runs first, and so no stage starts before the stage that justifies it has passed.

Claims are labelled the same way as [status.md](status.md): **measured**, **source**, **inferred**, **hypothesis**.

## The problem this would solve

iOS gives a CarPlay network's service a rank that denies it to applications. Until the driver opens Settings → Wi-Fi → the car network → **Check For Internet Access** → **Use For Internet**, Maps gets nothing while every box-side metric reads healthy, and the setting does not survive the next ignition cycle (measured, 2026-11-11, confirmed live 2026-11-12). That tap is the single worst thing about the current system, which is why a beacon-level fix is worth investigating at all.

## What is known, and what is not

| Claim | Label | Where from |
|---|---|---|
| The F32's NBT EVO transmits an 802.11u Interworking element with the **Internet bit clear**, access network type private, venue group vehicular | measured | `iw` scan from the box, 2026-11-02 ([test-log.md](test-log.md)) |
| The F32 transmits **no Apple Device IE at all** | measured | same scan |
| iOS offered *Check For Internet Access* on that network anyway, the probe passed over IPv6, and Maps then worked | measured | P2, 2026-11-02 |
| Apple's wireless CarPlay guidance says the head unit communicates internet state dynamically through the Apple Device IE and an "Interworking"/"Networking" IE | source | [WWDC17 session 717](https://developer.apple.com/videos/play/wwdc2017/717/), 8:11 and 33:25 |
| An AP reusing a CarPlay SSID and passphrase is expected to offer the same services | source | same, 33:04 |
| A service ranked `Never` in configd gets only a scoped default route; *Use For Internet* presumably lifts that | hypothesis | Apple's open-source configd/xnu; never traced on a device |
| Internet-capability advertisements change iOS's probing or default-path eligibility | **hypothesis, untested** | — |
| An iPhone will join a different BSSID using the BMW-supplied credentials | **hypothesis, untested** | — |
| CarPlay discovery and sessions survive an external AP and whatever links it to the head unit | **hypothesis, untested** | — |
| Correct advertisements remove the consent requirement | **hypothesis, untested**, and not implied by any of the above | — |

The first two rows already constrain the idea more than the review assumed. The car that works today advertises **no** internet capability by either mechanism, and iOS probed it regardless. Whatever the Internet bit does on this phone, it is not the gate on *Check For Internet Access* appearing or passing. The remaining possibility — that it changes the network's rank, and so whether the tap is needed at all — is the one actually worth testing, and it is the one nothing here yet touches.

### Two corrections to the earlier advice

- **No ready-made Apple IE hex string.** An earlier version of this idea named an `InternetAccess` bit and implied it could be dropped into hostapd's `vendor_elements`. It cannot. The bit names in [`recon/ieaudit/apple.go`](../recon/ieaudit/apple.go) and [`scripts/car-ie.swift`](../scripts/car-ie.swift) come from third-party copies of an Apple header and from open-source head-unit projects. That provenance is enough to decode a capture and argue about it; it is not enough to transmit. Trials C and D are blocked on this, and [`test-ap/trials/C-apple-ie.conf.blocked`](../test-ap/trials/C-apple-ie.conf.blocked) does not load.
- **"Networking IE" was a transcript artefact — resolved.** Apple's own slides for session 717 say **Interworking IE**; the "Networking IE" wording came from the auto-transcript at 33:25. There is no separate unidentified Apple signalling format to hunt for, and the standards-based experiment uses the 802.11u **Interworking** element (ID 107, 802.11-2020 9.4.2.91) as planned.

## Prior art (2026-11-14)

A deeper search found implementation-level material. **None of it demonstrates the outcome we want** — nobody has published a phone that automatically uses an external internet route on genuine NBT EVO CarPlay after every restart because the AP's elements were changed. What it does supply is one hard-won pitfall, one new capability, and one confusion to avoid.

| Source | What it establishes | What it does not |
|---|---|---|
| Apple's [session 717 slides](https://devstreaming-cdn.apple.com/videos/wwdc/2017/717tmz5rl6c2t2k/717/717_developing_wireless_carplay_systems.pdf?dl=1) (PDF, not the transcript) | Names the Apple Device IE **and the Interworking IE** as the mechanisms for communicating internet connectivity and changes to it | Anything about consent, or its persistence across ignition cycles |
| [Ciellee's CarPlay AP notes](https://blog.csdn.net/Ciellee/article/details/118112471) (CSDN, Chinese) | A real CarPlay AP configuring Interworking and Apple vendor elements in hostapd — and **the element never reached the air until the Wi-Fi driver was patched** to pass it down (`WL11U`, `wl_cfg80211_start_ap()`) | A validated internet-capable Apple IE. Its example is `internet=0` with `access_network_type=3`, and its Apple IE payload is identity fields — name, manufacturer, model, Bluetooth MAC, device ID — not a capability template |
| [Runtime vendor-IE notes](https://blog.csdn.net/weixin_43209963/article/details/128860397) (CSDN, Chinese) | hostapd's control interface can change advertised elements **without restarting the AP**: `SET vendor_elements <hex>`, `SET assocresp_elements <hex>`, `UPDATE_BEACON` | That any given hostapd build supports it, or that iOS re-evaluates CarPlay internet eligibility when those fields change |
| [Reverse Engineering SE: the Apple beacon tag](https://reverseengineering.stackexchange.com/questions/31710/decoding-the-apple-vendor-tag-in-the-802-11-beacon) | A captured **Personal Hotspot** element, OUI `00:17:f2` type 6, which macOS surfaces as `IOS_IE` and uses to recognise a hotspot | Any link to CarPlay. The author leaves that explicitly open |

**The two Apple OUIs are different mechanisms and must not be swapped.**

| Mechanism | OUI | Question it bears on |
|---|---|---|
| CarPlay Apple Device IE | `00:a0:40` type 0 | Accessory identity and capability advertisement |
| Apple Personal Hotspot element | `00:17:f2` type 6 | Whether something is recognised *as* a hotspot |

`ieaudit` now decodes both and labels them apart, so a capture cannot file one under the other. The two published hotspot examples disagree in one octet (`…03 03 00 00` captured versus `…03 01 00 00` in an OpenWrt example) and the field meanings are unresolved, so it prints the payload rather than interpreting it. Impersonating a hotspot is at best a secondary experiment: macOS recognising a hotspot says nothing about an iPhone promoting a CarPlay-managed network to its default path.

**Trials C and D stay blocked.** The Ciellee Apple IE example is the best public lead on the element's *structure*, and it is still not an internet-capable template: it demonstrates no A/B test of a capability bit, and it embeds another vehicle's device identities, which the exclusions below rule out copying.

### The pitfall that changes gate 1's acceptance criteria

A CarPlay developer configured `interworking=1`, hostapd accepted it, the generated configuration contained it — and the element was not transmitted, because the driver had no code to install it into the beacon. Four observations, only one of which is evidence:

| Observation | What it licenses you to conclude |
|---|---|
| hostapd accepts `internet=1` | The config parser accepted it |
| The generated config contains the option | The config layer passed it through |
| **An independent capture shows the changed element** | **The advertisement actually changed** |
| Phone behaviour changes across repeated controlled trials | The advertisement affects this phone's policy |

This is why `ap-signal-test.sh` takes its verdict from a second radio and never from the config file — a decision made before this source was found, and now ratified by someone who paid for it.

**On our hardware the trap is specific and avoidable.** It bites FullMAC radios, where firmware builds the beacon and the driver must hand each extra element down. On SoftMAC (mac80211) drivers hostapd builds the whole beacon and the kernel transmits it verbatim. On the SBC: `car-a` and `car-b` are **ath9k_htc — SoftMAC, fine**; `wlan0` is the onboard Broadcom **`bcmsdh_sdmmc` — FullMAC, exactly the trap**. `ap-signal-test.sh` now refuses a non-mac80211 AP radio unless `ALLOW_FULLMAC=1`.

## Gate 0 — trace the rank before buying anything

**Cost: one sysdiagnose. Could retire the entire programme.**

Every stage after this one assumes the car network's rank is something a beacon can influence. If instead iOS assigns the rank because the interface belongs to a CarPlay session — as the configd reading suggests, wired CarPlay being ranked `Never` with no beacon involved at all — then no access point, however it advertises, can change it, and gates 1 to 3 are weeks spent on an untestable hypothesis.

| Step | Evidence wanted |
|---|---|
| Capture a sysdiagnose on the iPhone while parked, CarPlay up, *Use For Internet* off | How the car network's service is ranked, and what names the rank |
| Repeat immediately after tapping *Use For Internet* | What changed, and whether the change is attached to the Wi-Fi service or to the CarPlay session |
| Compare against the home Wi-Fi service in the same dump | A baseline for a network nobody has to consent to |

**Stop if:** the rank is tied to the CarPlay session or interface rather than to anything the AP transmits. Record that and close this file; the tap is then an iOS product decision, and the only remaining routes are the ones already in [status.md](status.md).

## Gate 1 — control the advertisements, and prove it from the air

**Cost: about zero with the hardware on hand; roughly $30–80 if another radio is needed. Planning estimate, not a quote.**

This proves tooling, not policy. A hostapd configuration file is not evidence of what a radio transmitted, so the gate is a capture from a second radio.

| Component | Requirement | Why |
|---|---|---|
| AP host | Linux with hostapd and an AP-capable radio | Controls what the phone sees |
| Capture radio | A **separate** monitor-mode adapter | A radio cannot capture its own beacon |
| Uplink | Garage Ethernet | Keeps upstream Wi-Fi behaviour out of the experiment |
| Management | Wired | So a bad config does not cut off the session that would fix it |
| Phone instrumentation | CarNet Probe, plus logs | Separates Wi-Fi-bound connectivity from default-path connectivity |

**Not yet installed on the SBC** (checked 2026-11-14): neither `iw` nor `hostapd` is present. `apt-get install iw hostapd tcpdump` first, then let `run` verify the hostapd build has `CONFIG_INTERWORKING=y` — it fails loudly on `unknown configuration item`, which is the cheap half of the Ciellee pitfall. Both AR9271 dongles are also assigned to cars, so gate 1 needs a window with carplayd stopped, or a third dongle.

Run it:

```sh
sudo scripts/ap-signal-test.sh run A    # Interworking present, Internet bit clear
sudo scripts/ap-signal-test.sh run B    # the same AP, Internet bit set
```

`run` composes [`test-ap/hostapd-base.conf.example`](../test-ap/hostapd-base.conf.example) with one trial fragment, puts the monitor radio on the AP's channel, captures beacons and probe responses, and hands the capture to `ieaudit check`, which fails unless **at least one beacon and one probe response** from the BSSID under test carry an Interworking element with the expected bit, and every such frame agrees.

| Pass condition | How it is checked |
|---|---|
| hostapd accepts the options | It starts, and the log has no `unknown configuration item`. A build without `CONFIG_INTERWORKING=y` fails here, which is the point |
| The beacon carries the intended bit | `ieaudit check` |
| The probe response carries it too | `ieaudit check` — hostapd builds the two from different templates, and a config file cannot show a disagreement |
| Nothing else moved between A and B | Same SSID, BSSID, passphrase, DHCP, DNS, IPv6, firewall and uplink; only the fragment differs |
| The phone's behaviour is recorded separately from the box's | Association, probing, default-path selection and prompts each logged on their own |

A **test SSID, not the car's**: see the exclusions below.

**Stop if:** the bytes on the air do not match the configuration. That is a tooling or firmware problem and everything downstream would be uninterpretable.

**Expect, even on success:** no answer about CarPlay. An ordinary Wi-Fi network is not ranked `Never` to begin with, so a changed prompt here would be interesting and a *lack* of change would prove nothing. Gate 1 buys a validated capture pipeline and a working AP.

## Gate 2 — real CarPlay through an AP we control

**The expensive gate, and the one most likely to fail.**

A Linux box associated to the BMW as an ordinary station is not a transparent bridge for a downstream iPhone. OpenWrt documents station-mode layer-2 bridging as unsupported on many drivers; its `relayd` workaround gives IPv4 bridge-like behaviour, with IPv6 RA/NDP and link-local discovery left to solve separately ([OpenWrt relay configuration](https://openwrt.org/docs/guide-user/network/wifi/wifiextenders/relay_configuration)). Apple's session setup depends on Bluetooth/iAP2 pairing, Wi-Fi joining, Bonjour discovery and a head-unit-initiated session, so web browsing through the relay is not a success criterion.

| Candidate transport | Position |
|---|---|
| True layer-2 bridge (WDS / four-address) | Preferable; BMW support not established |
| `relayd` plus a separate IPv6 design | Experimental, not a transparent bridge |
| Routed link with discovery reflection | May fail on addresses inside discovery records, link-local scope, or session assumptions |
| NAT/repeater mode | Not sufficient for head-unit-initiated discovery |

| Pass condition |
|---|
| A capture proves the iPhone associated to **our** AP, not to the BMW |
| The Bonjour exchanges CarPlay needs cross the link in both directions |
| The head unit starts a genuine CarPlay session while the phone stays on our AP |
| Maps, audio, Siri and the controls work while parked |
| Several clean reconnections, without re-plumbing the topology by hand |

**Stop if:** any of these fail. Without a CarPlay-managed connection there is nothing to test the policy hypothesis on, and the signalling work goes back on the shelf rather than being carried as half-finished code.

## Gate 3 — the A/B matrix

Only after gates 0–2. Keep the data plane frozen: do not add NAT64/DNS64, change DHCP, alter the IPv6 design and change advertisements in the same week.

| Trial | Interworking internet | Apple Device IE | Purpose | State |
|---|---:|---|---|---|
| A | 0 | absent | Baseline through our AP | ready |
| B | 1 | absent | The standard flag on its own | ready |
| C | 0 | validated internet-capable form | The Apple signal on its own | **blocked** |
| D | 1 | validated internet-capable form | The combined OEM-style advertisement | **blocked** |

C is deliberately self-contradictory. It is a diagnostic, never a deployment configuration.

**Trial E — a live internet-state change, no reassociation.** Apple's guidance is that a head unit communicates connectivity changes *dynamically*, e.g. when its internet module fails. Every trial above changes the advertisement and then reassociates, which mixes two variables. hostapd's control interface can change advertised elements in place:

```text
SET vendor_elements <hex>       # Apple bytes — blocked, see above
SET assocresp_elements <hex>
UPDATE_BEACON
```

E is **not blocked**, because the standard Interworking bit needs no Apple bytes. The method does need verifying: whether this hostapd build accepts the field at runtime at all, and whether the beacon on air actually changes while the association persists — both answerable with the same second radio. It separates three questions the A/B matrix cannot:

| Sub-trial | What it isolates |
|---|---|
| Internet-capable before association | Whether iOS treats the initial connection differently |
| Flipped after association | Whether iOS reacts to a live connectivity change at all |
| Flipped back when the upstream dies | Whether signalling helps the phone *stop* choosing the Wi-Fi path — which would also be useful to the current design |

The last row is the one worth noting: it could pay off even if the no-tap goal fails, because departure and upstream-loss handling are live concerns in [status.md](status.md).

C and D unblock on **a capture from a real internet-capable wireless CarPlay head unit in both its internet-on and internet-off states**, decoded with `ieaudit pcap`, showing which octets move. The F32 cannot be that reference: it transmits no Apple Device IE. Until then the bytes would be a guess, and a guessed IE yields a trial nobody can interpret — the phone ignoring it proves nothing if the bytes were wrong, and the phone reacting proves nothing about which field did the work.

Use a **fresh association** between trials and record the prior consent state explicitly, or an earlier manual tap will make a later trial look like a success.

### What to measure, per trial

| Measurement | What it establishes |
|---|---|
| Association BSSID, from the capture | The AP under test is the one the phone used |
| Beacon and probe-response IEs, from the capture | The treatment reached the air |
| Wi-Fi-bound probe (CarNet Probe) | The transport works at all |
| Default-path probe | Ordinary application networking can use it |
| A fresh, uncached Maps search and route | User-facing success, not background traffic |
| Settings interaction log | Whether any acceptance tap was needed |
| Restart sequence | Whether the result survives the next association and ignition cycle |
| Cellular recovery | Whether driving away leaves a working default path |

An HTTP packet is not proof that Apple validated the network — identify its destination. Background push traffic is not proof that Maps can use the connection.

Threshold: **five consecutive clean starts** as an initial go/no-go, then a longer soak. A proposed engineering threshold, not a statistical reliability claim.

## Deployment would be harder than a passing bench test

The current design avoids a problem a replacement AP creates: the phone's CarPlay transport would depend on a garage radio that disappears when the car drives away. Apple's guidance is that a head unit should attempt a fast reconnect and fall back to Bluetooth after an unexpected loss — a recovery procedure, not a seamless-roaming guarantee.

| Deployment | Main consequence |
|---|---|
| Garage-hosted replacement AP | Must demonstrate an acceptable transition back to the BMW AP on departure |
| In-car AP with a garage Ethernet uplink | Survives the uplink going away, but needs permanent in-car hardware and a working link to the head unit |
| Modified real BMW AP advertisements | No roaming problem, but needs head-unit access and breaks the read-only rule in the README |

All configuration changes are made while parked. Driving tests stay separate from development.

## Exclusions

Things this experiment does not do, each for a reason:

| Not done | Why |
|---|---|
| Clone the BMW SSID or BSSID | Section 33:04 expects a same-credential AP to offer the same services; and a cloned BSSID makes the capture ambiguous about which AP the phone chose. Start distinct, observe which one it picks |
| Send forged management frames | Out of scope, and it would contaminate every measurement after it |
| Enable Passpoint/Hotspot 2.0, ANQP servers or captive-portal redirection | Each changes client behaviour on its own; the question is what one bit does |
| Transmit a constructed Apple Device IE | See gate 3 |
| Copy another vehicle's identity-bearing fields | Extract structure, never wholesale identity |
| Associate to someone else's CarPlay network for the reference capture | Capture passively; collect no other party's traffic or credentials |
| Touch a radio that is currently associated | `ap-signal-test.sh` refuses, so a car link cannot be pulled out from under carplayd |

## Trial manifest

One row per run, filled in from the capture rather than from intent. Keep the originals in `captures/` (not tracked) and paste only the sanitized `ieaudit` output here.

| Field | Why it is recorded |
|---|---|
| Date, trial | — |
| iOS build | iOS policy is the thing under test; a point release invalidates earlier rows |
| AP host, radio, driver, firmware, hostapd version | "hostapd accepted it" depends on all five |
| Treatment | The trial fragment, by filename |
| Prior consent state | Whether *Use For Internet* had ever been accepted for this SSID on this phone |
| Association BSSID | From the capture, not from intent |
| `ieaudit check` verdict | PASS/FAIL and the Interworking line it printed |
| Result | Against the measurement table above |

| Date | Trial | iOS | AP host / radio / driver | hostapd | Prior consent | Assoc. BSSID | ieaudit | Result |
|---|---|---|---|---|---|---|---|---|
| — | — | — | — | — | — | — | — | *no runs yet* |

## Results

Sanitized. Empty until gate 0 produces something.

| Date | Trial | Default-path probe | Manual taps needed | Survived restart | Departure behaviour |
|---|---|---|---|---|---|
| — | — | — | — | — | — |

## What is in the repository for this

| Path | What |
|---|---|
| [`recon/ieaudit/`](../recon/ieaudit) | Go decoder and validator for Interworking and **both** Apple vendor elements — the CarPlay Device IE (`00:a0:40`) and the Personal Hotspot element (`00:17:f2`), labelled apart; `hex`, `pcap`, `check`. Rejects malformed lengths and truncated captures, masks identity-bearing fields, and shares its canned test vector with `car-ie.swift` — the two decoders were run against it and agree (2026-11-13). Filter captures with `--bssid`: `--ssid` cannot match a beacon whose elements are too damaged to read, which is the case most worth seeing |
| [`test-ap/hostapd-base.conf.example`](../test-ap/hostapd-base.conf.example) | The constant baseline. Copy to `secrets/ap-test.conf` |
| [`test-ap/trials/`](../test-ap/trials) | One fragment per trial — the advertisement delta and nothing else |
| [`scripts/ap-signal-test.sh`](../scripts/ap-signal-test.sh) | Compose, run, capture, verify, tear down |
| `captures/` | Original pcaps and logs. Not tracked |

The deliverables in order: *can we control these advertisements?*, then *can real CarPlay run through that AP?*, and only then *does signalling remove the acceptance step?* Gate 0 comes before all three because it can make the question moot for a few minutes' work.
