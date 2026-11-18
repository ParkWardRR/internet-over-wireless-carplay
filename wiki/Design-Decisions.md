# Design Decisions

Why it is built this way, and what was tried and rejected.

## IPv6 route, not manual IPv4

The car owns IPv4: the head unit is the DHCP server, sends no router option, and iOS hides manual IP settings on a CarPlay network. Getting in over IPv4 would mean answering the phone's DHCP request faster than the car does, every time.

The link has **no IPv6 router at all**, so adding one conflicts with nothing and needs no phone configuration. Manual IPv4 does work — it is how the one piece of prior art did it in 2022 — but a static router setting on the phone outlives the garage and follows you everywhere.

## Learned signal thresholds, not configured ones

Fixed numbers cannot work in this environment: the box sits behind a wall where **a parked car's signal overlaps the signal at which the link dies**, and every value moves when a car parks somewhere new or an adapter is swapped. So each car's healthy band is learned at runtime and persisted.

**With one hard rule: a learned threshold may make the box more patient than configured, never more eager.**

That rule came from a failure. A car parked much closer than it ever had, its learned healthy band moved up accordingly, and the derived "give up" threshold became tight enough that perfectly ordinary variation tripped it — so the box abandoned a car it had been serving happily, twice in thirty seconds. For a car parked close, *no* relative margin is safe; you would need a margin so wide that learning stops meaning anything. The configured value is the outer bound, and the clamp was missing in that direction.

## Leaving on transmit failures, not signal strength

Departures here do not fade. Measured on a real drive: the signal sat **flat for 35 seconds and then the link simply died** — no warning, no goodbye, and a phone left holding a dead route. No signal rule could have fired.

The driver's transmit-failure rate was the only advance warning, and it gave about 8 seconds of it. Across hundreds of samples that rate is **bimodal** — low when healthy, high when failing, with nothing in between — so the threshold sits in an empty gap and needs no tuning.

**The decision outlived the hardware it was made on.** That drive used the box's onboard radio. Both cars are now served by USB AR9271 adapters, whose driver reports the transmit-failure counter as permanently zero — so the rule is still in the code, still correct, and cannot fire. Every leave since has come from a signal rule, which is what this section says is insufficient. Replacing the signal is open work; pretending the rule is running is not an option.

## Capping our own traffic

Because the box can otherwise take CarPlay down — measured, including a head unit that needed a hard reset. See [[Bandwidth and Airtime]].

The cap is **off by default**: a different head unit may not need it, and a silent throttle is worse than none. Where it is on, the cost is stated rather than hidden.

### Why the cap is not derived from free airtime

There is roughly 20 Mbit/s of idle channel. The relay collapses at about 3.8 Mbit/s. **Airtime is not the binding constraint**, so a ceiling computed from it would walk straight back into the failure. The automatic mode's upper bound was *lowered* on the strength of the relay measurement, against the airtime measurement.

### Why latency, and not throughput, steers the automatic mode

CarPlay's own stream never passes through the box, so it cannot be measured directly. But its packets queue behind the same airtime and the same relay, so **round-trip time to the phone is the proxy** — it reached 2051 ms unshaped and sits at 30–130 ms capped.

Three details that matter:

- **Only measured while actually sending.** At idle the same number is dominated by the phone's power saving, and a controller that believed it would ratchet to the floor while nothing was happening.
- **A high percentile, not the mean.** The mean hides exactly the behaviour that breaks audio.
- **Down fast, up slow, and up only under real demand.** Too high can drop CarPlay and cost a reset; too low only costs download speed.

## Control traffic gets priority

The short queue that protects latency also starved the router advertisements that keep the phone's route alive — they could not get past a queue full of a download. Tightening the cap makes that worse, so control traffic has its own lane.

## Isolation by range, not by list

The car link is confined to the internet: not the box's own services, not your home network, not the other car.

When the IPv6-to-IPv4 translator was added, the first version permitted a short **list** of named hosts. That quietly meant every IPv4-only *public* site was refused, because the translated addresses all sit in a range the isolation rules reject. Now the non-global ranges are rejected **by range** and the rest is permitted — a guard derived from the address space rather than from remembering to list a host.

## What was rejected

| Idea | Why not |
|---|---|
| Manual IPv4 on the phone | works, but the static setting follows the phone everywhere |
| USB-C Ethernet on the phone | plausible backup, needs no password, untested |
| Offline maps | fine for known places, no live traffic or search |
| Cellular booster | expensive, and fixes a different problem |
| Rooting the head unit | time, risk, and it breaks the read-only rule |
| Advertising "internet available" in a beacon to remove the tap | the car already advertises *no* internet and iOS probes anyway, so that bit is not what gates it |

## Rules the project holds itself to

- **Never commit** a car's password, VIN digits, MAC addresses or keys. A pre-commit hook blocks the known values; it has caught a real leak twice.
- **Scanning your own refs is not enough.** This repository published unscrubbed history twice — once in the files, once because a rewrite pushed the main branch but left release tags pointing into the old history. Check what the *remote* serves.
- **Car work is read-only** unless a step says otherwise. No coding, no flashing.
- **Log every test** with a date, what ran, and what changed as a result. A claim with no entry is not a result.
- **Label every claim** measured / source / emulator / inferred. The emulator is a Linux stand-in and can tell you nothing about iOS behaviour.
- **Keep the wrong conclusions.** Several confident findings here turned out to be wrong and are recorded as wrong rather than quietly edited out.
