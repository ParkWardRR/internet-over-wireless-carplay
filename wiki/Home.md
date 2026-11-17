# Internet over wireless CarPlay

Give your iPhone a working internet connection on your car's wireless CarPlay network, in a place with no cell signal.

Your car's wireless CarPlay creates its own private Wi-Fi network so the phone can talk to the dashboard. That network carries **no internet** — it is a cable replacement, not a hotspot. Normally you never notice, because the phone uses cellular for everything else. Park underground and that stops: CarPlay still shows Maps, but Maps cannot search, route, or load a tile.

This project puts a small Linux box on that same network and hands the phone a route out to a wired connection. **The car is not modified. The phone's IP settings are not changed.**

## Start here

| If you want to… | Read |
|---|---|
| Set it up | **[[Getting Started]]** |
| Understand why it works | **[[How It Works]]** |
| Know what the car's network actually is | **[[The CarPlay Network]]** |
| Know how much bandwidth you really have | **[[Bandwidth and Airtime]]** |
| Fix something that is broken | **[[Troubleshooting]]** |
| Pick hardware | **[[Hardware]]** |
| Measure it yourself | **[[Measuring]]** |
| Know why it was built this way | **[[Design Decisions]]** |
| Know what is still unproven | **[[Open Questions]]** |

## The three things that catch everyone out

**1. You have to tap a switch on the phone.** iOS gives a CarPlay network the lowest service rank: background daemons use it, and your apps ignore it. Until you tap **Check For Internet Access** → **Use For Internet**, Maps will fail while everything on the box looks perfect. On one instrumented drive, 1.36 MB moved through the box and no user-facing app touched a byte. See [[Troubleshooting]].

**2. Do not saturate it.** Every byte is relayed by the head unit, so it crosses the air twice and competes with CarPlay's own audio and screen. Pushed hard, the head unit dropped CarPlay entirely and needed a hard reset to pair again. The box caps its own traffic for this reason. See [[Bandwidth and Airtime]].

**3. There is spare airtime and you cannot use it.** CarPlay uses about a quarter of the channel, leaving ~20 Mbit/s idle — but the head unit's relay collapses at roughly 3.8 Mbit/s. **The bottleneck is the relay, not the air.** See [[Bandwidth and Airtime]].

## How confident is any of this?

Every claim in this wiki is labelled:

- **measured** — on the car, the phone, or the box, with the date
- **source** — read from code, a specification, or a vendor document
- **emulator** — reproduced on Linux stand-ins, which cannot tell you anything about iOS behaviour
- **inferred** / **hypothesis** — reasoning, explicitly not evidence

Measured on **two BMWs with factory NBT EVO head units** and an **iPhone on iOS 27**. One garage, one phone. Treat it as a solid data point, not a universal law. The dated evidence is in `docs/test-log.md` in the repository, including the conclusions that turned out to be wrong.
