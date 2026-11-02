# bmw-f32-carplay-wan

Give the iPhone internet while it's on **wireless CarPlay** in the basement garage, where there's no LTE/GSM.

## The problem

Wireless CarPlay puts the iPhone's Wi-Fi on the car's own access point (`BMW<last 5 of VIN> CarPlay`-style SSID). That network hands out an address but **no router and no DNS**, so the phone keeps cellular as its internet path. Underground there's no cellular, so Google Maps can't search or route until you drive out.

## The key fact

If something on the CarPlay Wi-Fi offers a gateway, iOS uses that Wi-Fi for **both** CarPlay and internet ([Apple dev forum, BMW G30](https://developer.apple.com/forums/thread/76449)). Another owner set the router and DNS by hand on the phone: the "No Internet" warning went away and CarPlay kept working. So the car doesn't need to be modified. A box in the garage just has to join the car's Wi-Fi and route to the house.

```mermaid
flowchart LR
  P[iPhone] -- "CarPlay Wi-Fi<br/>(car is the AP)" --> HU[Head unit AP<br/>BMW...CarPlay]
  P -. "manual router/DNS<br/>for that SSID" .-> B
  B[Garage bridge box<br/>Wi-Fi client of car AP] -- "Ethernet + NAT" --> L[Home LAN<br/>172.16.0.0/16]
  L --> W((Internet))
```

## Approaches, ranked

| # | Approach | Cost | Touches car | Expected | Status |
|---|---|---|---|---|---|
| 0 | Offline maps (Google Maps offline area / Apple Maps offline) + start the route on home Wi-Fi before CarPlay connects | $0 | no | Works today for known places; search outside the area fails | to test |
| 1 | **Garage Wi-Fi bridge**: box joins the car's CarPlay SSID as a client, NATs to garage Ethernet; phone gets router/DNS for that SSID | ~$40–90 | no | Best bet | **main track** |
| 2 | Cellular booster (donor antenna outside, indoor antenna in garage) | $400–1,000 | no | Works for everyone and every app | fallback |
| 3 | OBD-II ENET: talk to the ZGW/head unit over the diagnostic Ethernet and look for an IP path to the head unit's Wi-Fi | ENET cable ~$15 | read-only | Learning; a usable route is unlikely without rooting the head unit | exploration |
| 4 | Root the head unit (QNX) and NAT Wi-Fi ↔ ENET | time + risk | yes | Possible in theory, high risk of bricking | not planned |

## Plan

| Phase | Do | Proves |
|---|---|---|
| **P0** | iPhone: Settings → Wi-Fi → (i) on the car network: note IP, mask, router (expected empty), DNS, and whether **Password** can be revealed. Test offline maps in the garage | Car subnet, PSK source, the free option |
| **P1** | Mac in the garage, car on: scan the car AP (band, channel, security, BSSID) — `docs/test-log.md` has the commands | Box radio requirements (5 GHz? DFS?) |
| **P2** | **MacBook proof of concept**: Mac joins the car SSID with the PSK, USB-C Ethernet to the garage LAN, IP forwarding + pf NAT; phone's router/DNS for the car SSID = Mac | Second client allowed, no client isolation, CarPlay survives, Maps works |
| **P3** | Permanent box: OpenWrt travel router (e.g. GL.iNet) in WISP/client mode on the car SSID, Ethernet uplink, static IP on the car subnet, masquerade | Hands-off: car on → internet |
| **P4** | Exit test: drive out with the manual config in place; time the fallback to cellular | No dead zone when leaving |
| **P5** | OBD ENET exploration (`docs/obd-enet.md`) | What's reachable from the OBD port |

## Unknowns to settle first

- **Head unit**: factory NBT EVO ID5/ID6 (2017+ LCI, wireless CarPlay) or an aftermarket CarPlay retrofit box? Retrofit boxes are Linux/Android boards and change the whole picture.
- **CarPlay PSK**: is it visible on the iPhone (iOS 16+ shows saved Wi-Fi passwords)? If not, see the [Bimmerfest thread](https://www.bimmerfest.com/threads/nbt-evo-carplay-wifi-password.1376809/) referenced from the Apple thread.
- **Car AP**: allows more than one client? Isolates clients?
- **Garage**: Ethernet drop and power near the parking spot.

## Rules

- Never commit the car's PSK, VIN or keys. Put them in `secrets/` (gitignored).
- Car work is read-only unless a step says otherwise: no coding, no flashing.
- Log every test in `docs/test-log.md`: date, firmware, what was run, result.
