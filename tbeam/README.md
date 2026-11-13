# CarNet Beam

A drive-test companion for the CarPlay departure investigation, on a LilyGO
T-Beam S3 Supreme. It rides in the car beside the passive Wi-Fi recorder and
logs what the recorder cannot see: where the car was, how fast, which way it
pointed, and how it moved — all stamped with GPS UTC so this log, the
recorder's, and the garage box's merge on wall clock.

## Controls

Every button does something, and the SYS page prints the legend so the device
documents itself in the car.

| Control | Action |
|---|---|
| **tap BOOT** | **Mark this instant.** Straight to the card with position, speed, heading; full-screen confirmation |
| **hold BOOT** 1.5 s | Next page |
| **short press power key** | Blank or unblank the screen, and flush the card immediately. The OLED is bright at night, and a flush means the last seconds are safe before you pull power |
| **long press power key** | The PMU's own hardware power-off. The firmware flushes the card when it sees the event |
| **RESET** | Reboot. A new log file pair per boot, so nothing is overwritten |

Tap it the moment CarPlay drops. The instant goes to the card immediately with
position, speed, heading and acceleration, and the screen confirms it full-size
so you can check at a glance without reading. Until now that moment was a human
saying "about then", and the recorder's stream-stop detector had nothing to be
checked against.

## Screens

| Page | Answers |
|---|---|
| **DRIVE** | Compass rose with a needle, speed in big digits, fix state, mark count. The page you glance at while moving. |
| **MARKS** | The last five marks, newest first, with times and speeds. |
| **MOTION** | Acceleration, peak since the last mark, pitch/roll, gyro. Or why the IMU is absent. |
| **GPS** | Position, altitude, HDOP, satellites, UTC, NMEA sentence counters. |
| **SYS** | Battery, chip temperature, card bytes written and dropped, rows, marks. |

## Logs

Two CSV files per boot under `/carnet` on the microSD (the mesh firmware owns
`/tbeam`, which is left alone):

- `drive-NNN.csv` — one row a second: time, fix, position, speed, course,
  heading, magnetometer, accelerometer, gyro, |a|, battery, uptime.
- `marks-NNN.csv` — one row per tap, written straight through rather than
  buffered, because a mark is the one record that must survive a power cut in
  the next two seconds.

Writes never block the loop: rows go into a ring, the loop flushes it every two
seconds, and a missing or failing card fills the ring and increments a counter.

## Driving with no laptop

The Beam and the recorder Pi log a whole trip together, unattended:

```
power bank ──> recorder Pi ──USB──> CarNet Beam
                   │                     │
            carrecon (Wi-Fi)      GPS, compass, marks
            carnetbridge  <────── $CNB telemetry + $CNBMARK
```

One cable does power and data. Both services start at boot (`carrecon`,
`carnetbridge`), so there is nothing to type: apply power and drive.

**Why this solves the clock problem.** In the car the Pi has no network and so
no NTP, and it has no RTC — a reboot leaves it up to a minute stale. The Beam has
GPS. Rather than step the Pi's clock mid-capture, which would corrupt the very
timestamps the analysis merges on, `carnetbridge` writes the Beam's **GPS time
next to the Pi's own clock every second, with the difference**:

```
beam gps_time=2026-11-12T02:43:06.000Z fix=1 sats=9 mph=23.4 ... pi_skew_ms=-412
```

The two logs are then aligned exactly, afterwards, with no risk during the drive.
GPS time arrives *before* a position fix, so alignment works from the first
second even underground.

Marks are forwarded the moment they happen, so a tap lands in the recorder's own
timeline as `beam_mark mark=4 mph=31.2 … pi_skew_ms=-412` rather than being
reconstructed later. That is what the garage box's `stream_stop` detector finally
gets to be checked against.

Logs: `/var/lib/carrecon/beam.events` on the Pi, and `/carnet/*.csv` on the
Beam's own card — two independent copies.

## Build and flash

Builds on the build host, never on the Mac. Flashing from the Mac is fine.

```sh
rsync -a tbeam/ <host>:~/builds/carnetbeam/
ssh <host> 'podman run --rm -v ~/builds/carnetbeam:/work:Z \
  -v ~/builds/.cache/pio:/root/.platformio:Z -w /work localhost/pio-build:1 pio run'
# then, from the Mac, in one invocation with every size "keep":
esptool --chip esp32s3 -p "$PORT" -b 460800 --before default-reset --after hard-reset \
  write-flash --flash-mode keep --flash-freq keep --flash-size keep \
  0x0 bootloader.bin 0x8000 partitions.bin 0xe000 boot_app0.bin 0x10000 firmware.bin
```

**This board normally runs the owner's Meshtastic node.** Take a full image
first and restore it afterwards — see `tbeam-supreme-firmware-handoff.md`.

## State on this board

| Subsystem | Status |
|---|---|
| PMU (AXP2101) | ✅ rails confirmed on, battery/charge read |
| Magnetometer (QMC6309 @ 0x7C) | ✅ heading works |
| GNSS (MAX-M10, UART2) | ✅ sentences flow, 0 bad checksums. No fix indoors, as expected |
| microSD logging | ✅ mounts, writes, rotates per boot |
| OLED (SH1106) | ✅ renders (bezel insets respected) |
| IMU (QMI8658) | ❌ does not answer on CS 34 — see below |

### The IMU: what is actually established, and what is not

`WHO_AM_I` reads a constant **`0x3E`**. The authoritative record is §9.1 of
`tbeam-supreme-firmware-handoff.md`. Three claims this file previously made were
wrong, and are corrected here rather than softened:

| Previous claim | Why it was wrong |
|---|---|
| "Mode 0 and mode 3 return the identical byte, and a wrong mode would give different garbage" | **The QMI8658 supports both mode 0 and mode 3** (datasheet: "SPI (3-wire or 4-wire modes)"). Identical results across them is *expected* and diagnostic of nothing. |
| "An I2C scan rules out I2C strapping" | The scan covered the board's normal I2C buses (17/18 and 42/41). The SPI-connected IMU's data and clock are **GPIO 35 and 36**. Wrong pins; it tested nothing about the IMU. |
| "Power ruled out: ALDO2 reads 3300 mV" | That is the PMU's *programmed setting*. The QMI8658 has **separate VDD (pin 8) and VDDIO (pin 5)** supplies, 1.71–3.6 V each, and neither was measured at the chip. |

"The vendor example fails, therefore it is not firmware" is also too strong. It
removes an application-specific bug as the explanation; it does not verify build
configuration, library versions, initialisation timing, or that signals reach the
part.

**The defensible conclusion is narrower: the fault reproduces outside this
application, and the boundary between board configuration and hardware has not
been established.**

### Two datasheet-conformance faults, now testable

Reading the QMI8658C datasheet turned up two things *both* this firmware and
LilyGO's example get wrong:

1. **System Turn On Time is 1.75 s**, and note 7 says it "starts once VDDIO and
   VDD are within 1% of Final Value". Both codebases probe within a few hundred
   milliseconds of enabling the sensor rail — roughly 35× inside spec.
2. **CS is the interface selector.** Pin 12: *"I2C/I3C/SPI-UI selection Pin.
   (If 1: I2C-UI Mode: I2C/I3C communication enabled, SPI idle mode)"*. Every
   attempt so far let CS idle high through power-up.

`tbeam/imuprobe/` tests exactly those, **read-only** — no soft reset, no CTRL
writes, because a write to a part that is not answering only adds a variable:

- **A** control: read with the rail long since up
- **B** power-cycle the rail with CS idling high, wait past 1.75 s, read for 10 s
- **C** power-cycle the rail with CS held **low** through power-up, then read

If every line still reads `0x3E`, both hypotheses are eliminated and the next
steps are physical: measure VDD and VDDIO at the chip's decoupling capacitors,
and capture CS 34 / SCK 36 / MOSI 35 / MISO 37 during one register-0 read.

### Identifying the board revision in five seconds

LilyGO's own docs give a physical cue needing no tools: **the PPS LED is blue on
V3.0 and red on V3.1.** V3.1 also adds silkscreen to the back of the PCB. The
documented V3.1 changes are the QMC6309 magnetometer, an onboard BMS, BME280 and
OLED address-selection resistors, and that silkscreen — **no IMU change is
documented**, which weakens "a different part on that select line". This board's
`0x7C` magnetometer is the QMC6309, so V3.1 is likely.

Schematics: `schematic/T-Beam-S3-Supreme/T-Beam-S3-Supreme-V3.0.pdf` and
`-V3.1.pdf` in the LilyGo-LoRa-Series repo.

The firmware reports this rather than hiding it: the MOTION page and the status
line both print both probe bytes and the conclusion, so the diagnosis travels
with the device.
