# Recon box: a Pi in the car that records the CarPlay Wi-Fi

**Why:** CarPlay drops on every departure and won't reconnect until airplane mode is toggled or the NBT is rebooted ([test log](test-log.md), 2026-11-04). The garage box only sees its own side. The fix deployed that day (trend leave) rests on a hypothesis: the head unit relays the phone's traffic to a box at the edge of range, starving CarPlay, and the link then dies without a goodbye. A box **in the car**, listening passively, can prove or kill that hypothesis. It also measures the review's open clocks: withdrawal delivery, the coverage gap, and app recovery ([README](../README.md)).

## Hardware

| Part | Notes |
|---|---|
| **recon-pi**: Raspberry Pi 4 B, 4 GB, Debian 13 | Onboard BCM43455 can't do monitor mode; used for home Wi-Fi in the garage (time sync, log upload) |
| **AR9271 USB adapter** (ath9k_htc, 2.4 GHz) | Monitor mode on channel 11 (both cars). Firmware `htc_9271.fw` from `firmware-atheros` |
| Power: **12 V → USB-C 3 A** adapter or a power bank | A Pi 4 browns out on most car USB ports (check `vcgencmd get_throttled`). A power bank keeps it running before the car starts, so it catches the phone's join |
| Optional: **USB GPS** (u-blox 7/8, ~$15) | Correct time with no internet, plus position along the exit route (where garage internet ends and cellular starts) |

**Before it leaves the house:** recon-pi runs a **TFTP server** (`/srv/tftp`). Anything that network-boots from it will fail while it's in the car.

## What it records

All on channel 11, without joining the car's network:

| Source | What | Answers |
|---|---|---|
| `mon0` (AR9271, monitor, ch 11 HT20) | Every 802.11 frame with radiotap headers (RSSI, rate, retry flag) to a pcap ring | Deauth/disassoc frames and who sent them; whether the **phone leaves the car's Wi-Fi** when CarPlay drops; retry storms and low-rate frames to the garage box (the airtime hypothesis); RAs and goodbyes from the box; the phone's RS/MLD |
| The same pcap + the car's PSK, offline | WPA2 decryption of the phone ↔ head unit traffic (needs the phone's 4-way handshake in the capture) | Whether the CarPlay session (TCP) resets; DNS and traffic through the box during departure |
| `recon.events` | Boot, capture start/stop, adapter lost, GPS fix, clock offset, per-second counters per MAC (frames, bytes, retries) | A timeline without opening the pcap |
| GPS (optional) | Position + PPS-free time via gpsd/chrony | The exit route's coverage gap |

The garage box (garage-sbc) logs its side at the same time: signal, leave, goodbye, link stats every 2 s. Both clocks are NTP-synced at home, and the events tie them together.

**Privacy:** the pcaps hold your phone's traffic and, decrypted, its contents. They stay on recon-pi and build-host under `secrets/`-style paths and are **never committed**.

## Software

Same pattern as carplayd: Go, small, systemd.

1. **`carrecon`** (Go, on the Pi):
   - finds the AR9271 by driver and creates `mon0` on channel 11 HT20;
   - runs `tcpdump -i mon0 -w` as a ring (200 MB × 50 files ≈ 10 GB);
   - keeps the per-MAC counters from the capture stream, writes `recon.events` and restarts the capture if the adapter drops;
   - **marks** a ring segment as kept when something interesting happens (a deauth or disassoc involving the phone or the head unit, the phone's traffic to the head unit stopping for more than 3 s, or the box's goodbye), so the ring never overwrites a departure.
2. **Home sync** (on the Pi): when it sees the home Wi-Fi (garage), the onboard chip joins it, chrony syncs time, and kept segments plus events are rsynced to build-host. The onboard chip never joins the car.
3. **`reconlab`** (Go + tshark in a podman container, on build-host):
   - decrypts with the car's PSK and builds one timeline per drive: car on → phone joins → box joins → traffic → box leaves (goodbye seen?) → CarPlay stream stops? → phone disconnects? → cellular;
   - merges garage-sbc's events;
   - outputs Markdown plus a chart of signal, retries and stream rate over time.

## Plan

| Step | Do | Done when |
|---|---|---|
| **R0 Prep** | Passwordless sudo for `alfa` on recon-pi (or run a bootstrap script by hand). `rfkill unblock wifi`; install `iw tcpdump firmware-atheros chrony` (+ `gpsd` if GPS); plug in the AR9271; confirm `iw phy` lists *monitor* | `mon0` up on channel 11 |
| **R1 Bench** | At home: capture the home Wi-Fi on its channel for 10 min; run carrecon + home sync; reconlab on the result | Timeline renders; ring and marks work; files reach build-host |
| **R2 Garage, parked** | Pi in the car, car on, CarPlay up, garage box serving, 10 min, no drive. Capture includes the phone's handshake | Decrypted CarPlay stream visible; baseline retry rate and stream rate; RAs from the box every 3–4 s |
| **R3 A/B departures** | Normal drives, alternating: **garage box on** (with the trend-leave fix) vs **garage box off** (`systemctl stop carplayd` before leaving, or the rollback). Note the clock time when CarPlay drops, or just let the stream-stop mark catch it | Whether drops happen **only with the box on**. If yes: what precedes them (retries to the box, deauths, phone disconnect). If no: the box isn't the cause |
| **R4 Exit route** | With GPS: where garage internet ends and cellular starts; withdrawal delivery and app recovery times | The review's coverage-gap and recovery clocks measured |
| **R5 Fix and verify** | Change carplayd (thresholds, trend, earlier withdraw, rate limiting) or the iOS side (e.g. *Use For Internet* off) based on R3; repeat R3 | No drops in 10 departures with the box on |

**R3 decides everything.** Until the A/B shows drops only with the box on, the airtime hypothesis is still a hypothesis.

## Open items

- sudo on recon-pi (blocks R0).
- Is anything netbooting from recon-pi's TFTP server?
- Power adapter or power bank; GPS yes or no.
- Does the NBT EVO have a client limit? The recon box doesn't join, so it doesn't matter here, but it would for any future in-car box.
