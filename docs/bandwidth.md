# How much bandwidth does CarPlay need, and what is left for us?

*2026-11-16. The research is done; the measurements are not.* This exists because the egress cap on a car link was picked by hand from a single measurement — 2 Mbit/s, because unshaped saturation dropped the F22's CarPlay session and 2 Mbit/s held for 120 seconds. That number has no principle behind it, and the right value is a property of *this car in this parking spot*, not of the protocol.

Claims are labelled as everywhere else: **measured**, **source**, **inferred**, **hypothesis**.

## What Apple actually requires: not public

The authoritative document is the CarPlay section of Apple's **Accessory Interface Specification**, distributed through the MFi program. We do not have it and will not guess at its contents.

| Source | Figure | Standing |
|---|---|---|
| A Scribd mirror of Apple's *CarPlay Wireless Performance Test Guide* | An AP test requiring **≥19 Mbps TCP uplink** measured with iperf, plus **+10 Mbps** if the accessory has an instrument cluster display | **source, single, unverified mirror.** A second search found no corroboration. Treat as order-of-magnitude |
| [WWDC16 session 722](https://developer.apple.com/videos/play/wwdc2016/722/) | 24-bit colour required, 60 Hz refresh strongly recommended, an H.264 decoder a hard requirement | source |
| Apple Developer Forums, quoting the R10 addendum | **2×2 MIMO recommended** for the accessory's Wi-Fi | source |
| Consumer blogs ([1](https://cartechstudio.com/blogs/apple-carplay/does-carplay-use-bluetooth-or-wifi), [2](https://cartechstudio.com/blogs/apple-carplay/can-you-use-carplay-without-wifi)) | 10–15 Mbps steady with peaks over 30; elsewhere 15–25 Mbps; elsewhere 10–11 Mbps | **unreliable.** Mutually inconsistent SEO content with no methodology. One even quotes 480 Mbps, which is the USB 2.0 line rate for *wired* CarPlay |

**What this is worth.** The only figure with any provenance is Apple's own test guide wanting ~19 Mbps of AP headroom for CarPlay. Nobody publishes the stream's actual bitrate, and it is adaptive anyway — a moving map is not a parked idle screen.

## Why it is tight here, which the numbers above explain

| Fact | Label |
|---|---|
| The CarPlay AP on both cars is **2.4 GHz channel 11, HT20, one spatial stream** — 52–65 Mbit/s PHY, so roughly 25–30 Mbit/s of MAC throughput | measured |
| Apple's own addendum **recommends 2×2 MIMO**; these head units are 1×1 | source |
| Apple's own test guide wants **≥19 Mbps** available for CarPlay | source, single |
| The head unit **relays** phone↔box traffic (no TDLS), so every byte we forward **crosses the air twice** | measured |
| So our usable share is about `(MAC throughput − CarPlay's demand) ÷ 2` | **inferred** |

Put together: if CarPlay really wants something like 19 Mbps of a ~25–30 Mbit/s link, the remainder is single-digit Mbit/s, and our traffic consumes it at **double** cost. A 2 Mbit/s cap spends ~4 Mbit/s of airtime. That is consistent with everything observed — 2 Mbit/s mostly working, residual jitter remaining, and unshaped saturation taking the session down — without proving it.

**This is the thing to measure, and we can.** `carrecon` already records per-second bytes per station in the BSS from monitor mode, phone → head unit included. That is CarPlay's own stream, directly, on these two cars. No blog required.

## The controller, instead of a number

`rate = auto` (see `carplayd.conf.example`). It steers the cap by **round-trip time to the phone, measured only while we are actually sending**, because that is the one signal on the box that moves with the shared medium: unshaped it reached 2051 ms, capped it sat at 30–130 ms.

| Property | Choice | Why |
|---|---|---|
| Control signal | p95 loaded RTT to the phone | CarPlay's own stream never passes through the box, so it cannot be measured directly. Its packets queue behind the same airtime and the same relay, so our latency is the proxy |
| Statistic | p95, not mean | 17×10 ms + 3×900 ms averages 143 ms — inside a 150 ms target — while p95 is 900 ms. A stream does not care about the average, it cares about the late packet |
| Gate | only decide when the shaper hit its limit | At idle the phone's power save puts round trips at 4 ms one second and 2051 ms the next. A controller that believed that would ratchet to the floor while nothing was happening |
| Direction | −30% down, +25% up, and **up only under demand** | Too high can drop CarPlay and cost a head-unit reset. Too low only costs download speed |
| Deadband | hold between ⅔·target and target | So it settles instead of oscillating around the boundary |
| Start | 2 Mbit/s, not the maximum | The one value measured safe on a real car. An unmeasured car should climb into its headroom under observation |
| Memory | learned rate persisted per car in `learned.json` | Same place and spirit as the learned signal thresholds |
| Safety | a link lost **while the signal was healthy** lowers a persistent ceiling to 70% of the rate in force | That is the signature of overdriving the head unit rather than of driving away. **Heuristic**, and gated on signal so an ordinary departure cannot ratchet the cap down over time |

Defaults: `rate_min = 512kbit`, `rate_max = 6mbit`, `rate_rtt = 150ms`, decisions every 15 s. All configurable per car.

Every change logs an `autorate` event with the p95, the target and the reason; a learned ceiling logs `autorate_ceiling`.

## The test, for both cars

Three parts. Part 1 is the one that turns the table above from inference into measurement.

### Part 1 — what CarPlay itself consumes (needs `carrecon` in the car)

This is the missing number. Run the recorder in the car, in monitor mode on channel 11, with CarPlay active and **the box's cap set to `off` but nothing loading it** — so the only traffic is CarPlay's own.

```sh
# in the car, on the recorder
sudo carrecon -i <mon-iface> -c 11 -out captures/carplay-only-<car>.jsonl
```

Record, per car, for at least 5 minutes each:

| Condition | What it answers |
|---|---|
| Parked, CarPlay idle on the Now Playing screen | the floor |
| Maps on screen, actively routing, moving map | the realistic steady demand |
| Siri invoked, voice responding | the peak |
| Scrolling a long list in CarPlay | the screen-update burst |

From the `sec` events take phone → head unit bytes/s. **That is CarPlay's bandwidth on your car**, and `MAC throughput − that` is the headroom our cap has to live inside, halved for the relay.

### Part 2 — the rate ladder, per car

```sh
sudo scripts/rate-trial.sh --secs 90 1mbit 2mbit 3mbit 4mbit
```

It applies each rate, waits for you to load the link hard, and records loaded RTT percentiles, throughput, shaper drops and whether the link survived — then asks you the one thing the box cannot see: **did the audio stutter.** Results land in `captures/` and it prints a table to paste below.

`--include-off` adds the unshaped step. It is not the default: unshaped saturation dropped the F22's session and needed a hard reset of the head unit.

Set a **fixed** `rate` and restart carplayd before running this. In `auto` the controller re-decides every 15 s and will overwrite each step; the script warns but cannot stop it.

### Part 3 — let the controller run

Set `rate = auto`, drive normally for several sessions per car, then read the events:

```sh
grep -E "carplayd_(autorate|autorate_ceiling|shaped)" /var/lib/carplayd/events.log
```

| Pass condition |
|---|
| The rate settles rather than oscillating — a handful of distinct values, not a sawtooth |
| It converges near the best rate Part 2 found by hand, from either direction |
| It never climbs during an idle interval (no `autorate` with `up` while nothing was downloading) |
| No CarPlay session is dropped across the soak |
| A learned ceiling, if one appears, is explainable by a real event rather than by an ordinary departure |

## Results

Empty until the above is run. Both cars, because only the F22 has ever been measured under load.

### CarPlay's own consumption (Part 1)

| Car | Condition | phone→head unit kB/s (median) | peak | Source |
|---|---|---|---|---|
| — | — | — | — | *not yet measured* |

### Rate ladder (Part 2)

| Car | rate | mean kB/s | p50 | p95 | max | drops | link | audio |
|---|---|---|---|---|---|---|---|---|
| — | — | — | — | — | — | — | — | *not yet measured* |

### Controller behaviour (Part 3)

| Car | sessions | rate range seen | settled at | ceilings learned | drops |
|---|---|---|---|---|---|
| — | — | — | — | — | *not yet measured* |

## Sources

- [WWDC16 session 722 — CarPlay](https://developer.apple.com/videos/play/wwdc2016/722/)
- [WWDC17 session 717 — Developing Wireless CarPlay Systems](https://developer.apple.com/videos/play/wwdc2017/717/) ([slides PDF](https://devstreaming-cdn.apple.com/videos/wwdc/2017/717tmz5rl6c2t2k/717/717_developing_wireless_carplay_systems.pdf?dl=1))
- [Apple Accessory Interface Specification R22 (Scribd mirror — unverified)](https://www.scribd.com/document/858963872/Apple-Accessory-Interface-Specification-R22)
- [CarPlay Wireless Performance Test Guide R6 (Scribd mirror — unverified, the 19 Mbps figure)](https://www.scribd.com/document/977207654/Wireless-Performance-Test-Guide-R6)
- [AppleInsider, inside the tech behind CarPlay](https://appleinsider.com/articles/14/03/03/inside-the-tech-behind-carplay-apples-new-in-vehicle-infotainment-system)
- Consumer-blog bandwidth figures, recorded only to mark them as unreliable: [Car Tech Studio, Bluetooth vs Wi-Fi](https://cartechstudio.com/blogs/apple-carplay/does-carplay-use-bluetooth-or-wifi), [Car Tech Studio, CarPlay without Wi-Fi](https://cartechstudio.com/blogs/apple-carplay/can-you-use-carplay-without-wifi)
