# Measuring

How to reproduce the numbers rather than trust them.

## The tools

| Tool | Answers |
|---|---|
| `airtime` | How much of the channel is used, by whom, in microseconds — the only honest way to ask "how much is left" |
| `carrecon` | Full frame capture in the car, with a pcap ring, for reconstructing a specific event |
| `ieaudit` | Decodes and validates the beacon elements that advertise capabilities |
| `scripts/rate-trial.sh` | Steps the traffic cap through values and records what each does |
| `nft list counters` | Did the phone validate? how much was forwarded? did NAT64 carry anything? |

Binaries for Linux amd64 and arm64 are attached to each release, built reproducibly.

## Measuring what CarPlay costs

**Run this from inside the car.** From outside, the phone's traffic is under-counted roughly tenfold.

```sh
sudo airtime -iface mon0 -channel 11 -dir /var/lib/airtime \
     -cars <head-unit-BSSIDs> -box <your-radio-MACs>
```

It needs no network, writes one append-only file flushed every second, and is capped so it cannot fill the card. Then:

```sh
airtime -report /var/lib/airtime/<run>.jsonl
```

The report separates **CarPlay-active** seconds from **car-away** seconds by itself, which matters a great deal — an aggregate over a window where the car was absent for a third of the time dilutes everything.

### Pass both of your radio MACs

`-box` takes a list. If your box serves two cars from two adapters and you name only one, the other is indistinguishable from a phone — and it will be *large*, because it is close to the recorder. That contaminated an early "peak CarPlay bandwidth" figure here.

### Vary what CarPlay is doing

The demand is not constant. Worth capturing separately:

| Condition | What it tells you |
|---|---|
| Parked, Now Playing on screen | the floor |
| Maps routing, map moving | the realistic steady demand |
| Siri invoked | the peak |
| Scrolling a long list | the screen-update burst |

## Finding the right traffic cap

```sh
sudo scripts/rate-trial.sh --secs 90 1mbit 2mbit 3mbit 4mbit
```

Each step applies the rate, waits for you to load the link hard, records round-trip percentiles, throughput, drops and whether the link survived — then asks you the one thing the box cannot see: **did the audio stutter.**

`--include-off` adds an unshaped step and is deliberately not the default: unshaped saturation dropped CarPlay and needed a head-unit reset.

Set a **fixed** cap and restart the daemon before running this. In automatic mode the controller re-decides every 15 seconds and will overwrite each step.

## Reading beacons

```sh
ieaudit pcap capture.pcap --bssid <head-unit>
ieaudit check capture.pcap --internet 1 --bssid <head-unit>
```

`check` is a gate: it fails unless at least one beacon **and** one probe response carry the expected value, and they agree. A configuration file is not evidence of what a radio transmitted — those are built from different templates and can disagree.

It masks identity-bearing fields by default, so output is safe to paste into an issue.

## Lessons about measuring, learned the hard way

**Separate idle from loaded.** At rest, round trips to the phone swung between 4 ms and 2051 ms with no load at all — that is Wi-Fi power saving, not congestion. Any number that mixes the two is measuring the phone's sleep schedule.

**Use a high percentile, not the mean.** 17 samples at 10 ms plus 3 at 900 ms averages 143 ms — inside a 150 ms target — while the 95th percentile is 900 ms. A stream does not care about the average; it cares about the late packet.

**Do not trust a counter you have not sanity-checked.** The adapter reports retries as permanently zero because the feature is unimplemented. That is not a clean link.

**Classify by behaviour, not by the first thing you see.** An early version of the airtime tool labelled stations on first sight and filed the loudest device on the channel — the phone, sending essentially all of its traffic to a head unit — as a stranger. The report then claimed CarPlay used 11.9% of the airtime when the real figure was about 26%. A classifier that can be wrong about the loudest station is worse than none, because it looks authoritative.

**A counter proving a rule matched is not a page loading.** A rising "packets accepted" counter was taken as proof a fix worked. It was not — it meant packets matched a firewall rule, which they can do on their way to failing.
