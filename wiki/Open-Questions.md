# Open Questions

What is genuinely unknown, so nobody has to rediscover the boundary.

## Not yet proven

| Question | Where it stands |
|---|---|
| **Does any of this behave the same on another head unit?** | Measured on two of the same model, in one garage. Nothing here should be assumed to generalise |
| **Another phone, another iOS version?** | One phone, iOS 27. The *Use For Internet* switch does not exist before iOS 27 at all |
| **Can two cars be served at once?** | **Yes** — 1 minute 53 seconds with two routes up, a prefix each and a phone attached to each, on independent radios. Ended by the head units when the cars were switched off. Not a soak test |
| **Does each car work on the current build?** | **Yes, both.** Each has been served and each has had iOS validate the link, so apps could use it. The evidence is lopsided: one car has validated 11 times over three days, the other twice, both times for under a minute |
| **Can two cars be *usable* at once?** | **Not shown.** In the window where both were served, neither phone validated, so only background traffic moved. Each car working and both working at once are different claims |
| **Has either car completed a long session on this build?** | No. Minutes, not hours — and on one car, seconds |
| **Does the box still leave cleanly before a link dies?** | **Unknown on the current radios.** The rule that made it reliable watches a driver counter the USB adapters never implement, so it cannot fire; the signal rules that remain are the ones a flat-signal departure was shown to defeat |
| **What is the relay's actual limit?** | Between 2 and 4 Mbit/s. It survives 2; it died somewhere under 4. Never bisected |
| **Does automatic cap tuning converge on a real car?** | It converges in tests and has never run on a car |

## Known limits that are unlikely to move

**The tap.** iOS ranks a CarPlay network's service as low as it goes. The explanation is a hypothesis from Apple's open-source networking code, never traced on a device. What is measured: before the tap, applications move nothing.

One promising-looking theory is **dead**: the car already advertises *no internet* in its beacon and has no Apple capability element, and iOS probes and passes anyway — so advertising "internet available" is not what would remove the tap.

**The relay.** The head unit is an infotainment computer being asked to route. Nothing on our side fixes that; we can only avoid overloading it.

**One channel.** CarPlay's audio and screen, our traffic twice over, and the neighbourhood all share one 20 MHz channel with a single spatial stream. Apple's own guidance recommends 2×2 MIMO for head units; these are 1×1.

**Power saving.** The phone's Wi-Fi sleeps. Round trips swing from 4 ms to over 2 seconds at idle. Not ours to change, and it has to be designed around rather than measured through.

## Things worth building that are not built

- **Proper queue management.** The box has only a rate limiter available; the better algorithms are not compiled into its vendor kernel. With them, the cap could probably sit higher for the same latency.
- **A forwarded-path health check.** The box currently checks its own internet, which can pass while the path *through* it to the phone is broken.
- **Two boxes with bounded failover**, so one can fail without taking the service with it.
- **Bisecting the relay limit** properly, which would turn a guessed cap into a measured one.
- **A departure signal the USB radios can actually produce**, to replace the counter they do not implement.

## How to add to this

If you reproduce any of it — or contradict it — the useful contribution is a dated entry saying what you ran, on what hardware, and what happened. Several confident conclusions in this project's own history turned out to be wrong, and they are recorded as wrong in `docs/test-log.md` rather than quietly edited out. That is the standard.
