# Bandwidth and Airtime

The most useful page here, and the most counter-intuitive.

## Why airtime, not bytes

On a 1×1 HT20 link, a frame sent at the slowest rate occupies roughly **ten times** the airtime of the same frame at the fastest. So "bytes per second" tells you almost nothing about how much of the channel is left.

A concrete example from a real capture: a neighbour's access point was moving **5 kB/s** and consuming **16.5% of the channel**. A byte counter calls that noise.

So the measurement converts **every frame** into the microseconds it held the medium, using 802.11 timing — HT20 one-stream rate tables, OFDM and CCK preambles, plus the inter-frame gap and acknowledgement for unicast.

## What CarPlay costs

**Measured from a recorder inside the car**, 695 seconds of live CarPlay:

| | Median | p95 | Max |
|---|---|---|---|
| **CarPlay total** (phone + head unit) | **26.5%** of the channel | 38.0% | 45.3% |
| Phone → head unit | **~1.2 MB/s (9.4 Mbit/s)** | 1.8 MB/s | 2.2 MB/s |
| Channel busy, CarPlay running | 37.1% | 46.5% | 62.7% |
| Channel busy, car switched off | 17.2% | 30.8% | 60.9% |

So the neighbourhood accounts for about 17%, and **CarPlay adds roughly 20 points on top**.

**This has to be measured from inside the car.** From the garage, behind a wall, the same phone registered −76 to −84 dBm and its traffic was under-counted by roughly tenfold. In the car it was −36 dBm.

## What is left, and why you cannot have it

| | Median | p5 |
|---|---|---|
| Channel idle | 62.9% | 53.5% |
| Headroom at the best observed rate | 40.9 Mbit/s | 34.5 |
| **Halved for the relay** | **20.4 Mbit/s** | 17.2 |

**That number is a trap.** The head unit's relay falls over long before the air does:

| Load pushed through the relay | Result |
|---|---|
| ~220 kbit/s of full-size packets | **60–87% packet loss**, round trips 450–940 ms |
| Sustained, harder | Round trips to **2051 ms**; throughput collapsed to 1–4 kB/s |
| Harder still | **CarPlay dropped.** The head unit needed a hard reset to pair again |

All of that with airtime still largely free, and with the radio link reporting excellent signal and rate throughout.

**The bottleneck is the head unit, not the Wi-Fi.** It is an infotainment computer being asked to act as a router, and it is not one. Free airtime is *not* evidence for a higher limit — a ceiling derived from idle channel walks straight back into the failure that cost a reset.

A detail that confirms it is the relay rather than fragmentation: the round-trip spikes were **independent of packet size**. 56-byte pings produced the worst delays.

## So the box limits itself

| | Uncapped | Capped at 2 Mbit/s |
|---|---|---|
| Worst round trip to the phone | **2051 ms** | **378 ms** |
| Typical | 50–500 ms | 30–130 ms |
| Throughput floor | 1–4 kB/s | 9 kB/s |
| Queue depth | unbounded | 0–16 packets |
| CarPlay dropped? | **yes, needed a reset** | **no** |

The cost is real and you will notice it: bulk downloads cap near 250 kB/s. That is the trade — a slower download that does not take the dashboard with it.

It is a **mitigation, not a cure.** Residual jitter remains, and the parts that cause it — the phone's Wi-Fi power saving, the head unit's relay, CarPlay sharing one 2.4 GHz channel — are not ours to change.

## A trap worth knowing if you build this

A short queue protects latency, but it also **starved the periodic router advertisements that keep the phone's route alive** — they could not get out past a queue full of somebody's download. The thing protecting the link was quietly breaking it. Control traffic needs its own priority lane.

## Caveats on these numbers

- The airtime model charges **occupancy only** — no contention, no backoff. So channel-busy is a **floor** and headroom is **optimistic**. Back-to-back frames at the top rate imply about 46 Mbit/s of payload, against the 25–30 a real transfer gets.
- Our traffic is relayed, so **halve the headroom again**.
- One car, one phone, one garage, one evening.

How to reproduce all of it: [[Measuring]].
