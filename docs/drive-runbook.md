# Drive runbook: recording a departure from both sides

For R2/R3 of [recon-box.md](recon-box.md): recon-pi in the car, garage-sbc serving, drive out of the
garage, around the block, and back in. Everything here is checkable before the car moves, because a
departure is expensive to repeat and a capture that was never recording looks exactly like a capture
with nothing in it.

## What we are trying to catch

CarPlay drops on every departure and will not come back until airplane mode is toggled or the NBT is
power-cycled ([test-log.md](test-log.md)). The hypothesis: the head unit serves the far-away garage
box at the slowest rates with retries, and that airtime comes out of CarPlay's own stream.

Two findings make this concrete:

- **Wireless CarPlay rides the same Wi-Fi link the box joins.** Bluetooth only discovers and
  reconnects; Apple's own session says *"the Wi-Fi link is used for all audio and video transfer to
  the CarPlay protocol, as well as iAP2 data transfer"*
  ([WWDC17 #717](https://developer.apple.com/videos/play/wwdc2017/717/)). The head unit also runs a
  second BSS on the same channel, `DIRECT-xx-BMW#####`, a Wi-Fi Direct group owner, but it sits idle
  with no associated stations — the phone associates to the `BMW##### CarPlay` AP, the same BSS as
  the box. So the box and CarPlay share one BSS and one AP radio, not merely one channel.
- **A slow station drags the whole BSS down.** 802.11 gives each station roughly equal *transmission
  opportunities*, not equal airtime, so a station at 1 Mb/s holds the channel about 11× longer per
  frame than one at 11 Mb/s, and the fast stations collapse to the slow one's throughput
  ([Heusse et al., INFOCOM 2003](https://infocom2003.ieee-infocom.org/papers/21_01.PDF)). Measured in
  the garage with the car parked: 725 frames/min at **1 Mb/s** and a **9.5 % retry rate** in the car's
  BSS, with the box at −78…−82 dBm while the in-car recorder hears the head unit at −61.

The head unit does **not** advertise a BSS Load element (its beacon carries tags 0, 1, 3, 5, 42, 45,
48, 50, 61, 107, 221×3), so there is no channel-utilisation figure to read. Airtime has to be
computed from the capture: every frame's length and its `radiotap.datarate`, which the ring records.

## Pre-flight

Run these; do not drive until each one passes.

| Check | Command | Pass |
|---|---|---|
| Recorder alive | `systemctl is-active carrecon` | `active` |
| Monitor mode on the car's channel | `iw dev mon0 info` | `type monitor`, `channel 11` |
| Ring actually rotating | `grep ring_status <events> \| tail -2` | `files=` climbing, two different `newest=` |
| Capture not stalling | `grep -c stall <events>` | `0` |
| Frames arriving | `grep iface <events> \| tail -1` | `rx_packets` climbing, `rx_dropped` ~flat |
| Power and heat | `grep health <events> \| tail -1` | `throttled=0x0` |
| Clocks agree | `date` on both boxes | same minute, same zone |
| Box serving the right car | `curl -s localhost:9111 \| grep serving` | the car under test, the other disabled |
| Box debug trace on | `grep -c dbg_decide <events>` | climbing while serving |

**The one that is easy to get wrong:** the capture must contain the phone's full four-way handshake,
or none of its traffic can be decrypted afterwards — Wireshark needs all four messages
([HowToDecrypt802.11](https://wiki.wireshark.org/HowToDecrypt802.11)). A phone that was already
associated before the capture started never sends one. So:

1. Start the recorder **first**, and never wipe the ring afterwards.
2. Then start the car, so the phone associates fresh.
3. Confirm all four: `grep eapol <events>` must show `m=1`, `m=2`, `m=3`, `m=4` for the phone's MAC.

If a message is missing, toggle Wi-Fi on the phone (or CarPlay off/on) and check again.

## Power

A Pi 4 browns out on most car USB ports, and an undervoltage reboot would both truncate the capture
and leave the clock wrong — there is no RTC, so after a reboot without network `fake-hwclock` restores
a time that can be up to an hour stale. Keep it powered continuously; do not reboot it in the car.

`carrecon` logs `health throttled=…` every 30 s. The bits:

| Value | Meaning |
|---|---|
| `0x0` | fine |
| `0x1` / `0x4` | under-voltage **now** / throttled **now** |
| `0x10000` / `0x40000` | under-voltage / throttling **has occurred** since boot |

Anything other than `0x0` during the power-bank soak means the supply is not holding up; a sticky
`0x10000` with a clean `0x1` means it dipped earlier but is steady now.

## Known confounders to rule out first

- **iPhone Personal Hotspot must be off.** Multiple reports have wireless CarPlay dropping within
  minutes of enabling it, independently of anything we run, because the hotspot competes with the
  CarPlay Wi-Fi link. With it on, a drop proves nothing about the box.
- **The AR9271 can stop receiving silently.** It is documented to go quiet in monitor mode after
  anywhere from seconds to hours, sooner on busy channels, while the interface stays up and tcpdump
  keeps running with nothing to read
  ([open-ath9k-htc-firmware#126](https://github.com/qca/open-ath9k-htc-firmware/issues/126)).
  `carrecon` watches for 30 s without a frame and recovers — recreating `mon0`, then reloading
  `ath9k_htc` to re-download firmware. A `stall` event in the log means part of the drive is missing.
- **5 GHz is out of reach.** The AR9271 has no 5 GHz monitor mode. Both of this head unit's BSSes are
  on channel 11, so nothing is lost here, but it would be on a head unit that moved the P2P group to
  5 GHz.

## The drive

1. Pre-flight passes; CarPlay up with **audio playing and the CarPlay screen showing**, so the stream
   is continuous and its interruption is unambiguous.
2. Note the wall-clock time. Drive out, around the block, back in. Do not rush the exit — the
   interesting window is the 30 s either side of leaving the garage.
3. Say out loud / note when CarPlay drops, if it does. The `stream_stop` mark should catch it, but a
   human timestamp is the ground truth the detector gets checked against.
4. Back in the garage: leave everything running for 2 more minutes, then pull.

## After

Both logs carry wall clock, so they merge directly. The box's `dbg_decide` line gives the leave
decision every second with its thresholds and trend baseline; the recorder's `sec` and `beacon` lines
give per-second bytes, retries and the head unit's RSSI from inside the car.

Decrypt and build the timeline with the car's PSK (from `secrets/`, never here). Questions to answer,
in order:

1. Did CarPlay's stream stop, and when, relative to the box's `leave`/`goodbye`?
2. Was there a deauth or disassoc, from whom, with what reason code?
3. What were the head unit's rates and retries **to the box** in the 30 s before the drop?
4. How much airtime did the box's share consume (Σ length ÷ rate) versus the phone's?
5. Did the box rejoin after leaving, and at what signal? (This morning's F22 departure rejoined twice
   and then clung on at 1.0 Mb/s until the link died.)

**The A/B is what decides it.** Repeat with `systemctl stop carplayd` on the garage box before
leaving. If drops happen only with the box on, the airtime hypothesis stands; if they happen either
way, the box is not the cause and the NBT is.
