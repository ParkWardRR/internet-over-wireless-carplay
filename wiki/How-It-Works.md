# How It Works

```
iPhone ──── CarPlay Wi-Fi 2.4 GHz ────► NBT EVO head unit (the access point)
  ▲                                            │
  │  IPv6 route + DNS, advertised              │ relays between its clients
  │                                            ▼
  └──────────────────────────────────── Linux box (second client)
                                               │
                                          Ethernet + NAT
                                               ▼
                                        your internet
```

## The idea in one paragraph

The car's network hands out IPv4 with **no router and no DNS**, and the head unit sends **no IPv6 router advertisements of its own**. So the box does not fight it: it leaves IPv4 alone and becomes the *only IPv6 router on the link*, advertising a private prefix, itself as the route, and a DNS server. The phone picks that up with plain SLAAC — standard autoconfiguration, nothing special. The box then translates out to your real connection.

Nothing is configured on the phone beyond the one switch.

## Why IPv6 and not IPv4

The car owns IPv4. The head unit is the DHCP server, it sends no router option, and iOS hides manual IP settings on a CarPlay network. The only IPv4 way in would be to answer the phone's DHCP request faster than the car does — a race you would have to win every single time.

IPv6 has no such conflict: the link has no IPv6 router at all, so there is nothing to race. The box advertises, the phone configures itself, and the car is untouched.

The link ends up **dual-stack**: the car's own IPv4 for CarPlay, and our IPv6-only path for internet. That matters — it is why a translator is needed for IPv4-only sites.

## What the box actually does

- **Serves whichever configured car appears**, one daemon per radio, with a firewall table per car link that permits the internet and nothing else: not the box's own services, not your home network, not the other car.
- **Leaves before the link dies, and says goodbye.** Departures here do not fade politely — the signal stays flat and then the link drops. So it also leaves on the driver's transmit-failure rate climbing, which buys about 8 seconds of warning where signal strength gives none. **On the USB adapters in use now that counter is never implemented**, so only the signal rules are actually live — see [[Design Decisions]] and [[Open Questions]].
- **Learns its own thresholds.** Fixed signal numbers cannot work: the box sits behind a wall where "parked" and "dying" overlap, and every number moves when a car parks differently. Each car's healthy band is learned at runtime and persisted. A learned threshold may make the box *more patient* than configured, never more eager — see [[Design Decisions]].
- **Caps its own traffic** so it cannot take CarPlay down. See [[Bandwidth and Airtime]].
- **Reaches IPv4-only hosts** through DNS64 + NAT64, since our path is IPv6-only.
- **Recovers a deaf radio.** The adapter sometimes stops receiving while reporting itself healthy; a run of empty scans on a channel that always has neighbours means the radio, not the world.

## Why your apps ignore it until you tap

iOS ranks a CarPlay network's service as low as it goes. A service ranked that way gets only a *scoped* default route: background daemons will use it, and applications will not.

That is why the box can be working perfectly and Maps still fails.

The switch presumably lifts that rank. **This is a hypothesis from Apple's open-source networking code, not something traced on the device** — what is *measured* is the behaviour: before the tap, apps move nothing; after it, they work.

A useful second finding: after that first tap, **iOS mostly re-checks by itself.** Across absences of 15–29 minutes the phone came back on its own 4–24 seconds after the box resumed serving. But not always — two sessions one morning never validated, and a real car restart needed the tap again.

## Why everything costs double

The phone and the box are both *clients* of the head unit's access point, and there is no direct link between them. So every byte goes **phone → head unit → box** or the reverse: it crosses the air twice, and shares that air with CarPlay's own video and audio.

This single fact explains nearly every performance characteristic of the system. Read [[Bandwidth and Airtime]].
