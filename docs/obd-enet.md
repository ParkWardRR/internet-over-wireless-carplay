# OBD-II ENET exploration (P5)

Goal: map what's reachable on the F32's diagnostic Ethernet, and whether any IP path leads from the OBD port to the head unit's Wi-Fi side. Expectation: the ZGW (central gateway) forwards diagnostics (HSFZ) to ECUs. It's not a general router, so a usable internet route through it is unlikely without changing the head unit. Treat this as recon.

## Hardware

- ENET cable (OBD-II → RJ45). Buy one with all 4 data pins wired; reviews report cheap cables missing RJ45 pin 6.
- USB-C Ethernet adapter on the Mac.
- Car battery charger/maintainer for sessions over ~15 min.

## Pinout (from common DIY guides — **verify with a multimeter before plugging in**)

| OBD-II pin | Function |
|---|---|
| 3 / 11 | Ethernet pair (RX+/RX−, car side) |
| 12 / 13 | Ethernet pair (TX+/TX−, car side) |
| 8 | Ethernet activation: needs +12 V via ~510 Ω from pin 16 |
| 16 | +12 V battery |
| 4 / 5 | Ground |

Source: [Team-BHP DIY ENET cable](https://www.team-bhp.com/forum/diy-do-yourself/230803-diy-making-enet-obd2-cable-hook-your-bmw-laptop.html), [pinouts.ru BMW](https://pinouts.ru/CarElectronics/car_diag.shtml).

## Steps (read-only)

| Step | Command / action | Record |
|---|---|---|
| 1 | Ignition on (engine off), charger connected, plug in ENET | time |
| 2 | Mac Ethernet on DHCP; expect a link-local `169.254.x.x` address | Mac IP |
| 3 | `sudo tcpdump -ni en<N> -c 200` | broadcasts: vehicle announce, ZGW IP, VIN |
| 4 | `arp -an` and `ping -c3 169.254.255.255` | hosts on the link |
| 5 | `nmap -sT -Pn --top-ports 200 <zgw-ip>` (slow, polite) | open ports. Expect HSFZ diagnostics on TCP 6801 and vehicle discovery on UDP 6811 ([Quantex](https://quantexlab.com/en/develop/elm327_hsfz.html)); the car takes no DHCP and falls back to a 169.254.x.x Auto-IP ([ediabaslib](https://github.com/uholeschak/ediabaslib/blob/master/docs/ENET_WiFi_Adapter.md)) |
| 6 | Look for a second address behind the ZGW (head unit) via the vehicle-ident reply / E-Sys connection list | head unit IP, reachable? |
| 7 | Unplug. Confirm the car sleeps (pin 8 activation keeps the bus awake and drains the battery) | sleep OK |

## Don't

- No coding, flashing or FDL writes from E-Sys/ISTA in this project.
- Don't leave the cable plugged in with the car locked.
- No port scans faster than the default nmap timing; gateways can drop into a fault state.
