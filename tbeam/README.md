# CarNet Beam

A drive-test companion for the CarPlay departure investigation, on a LilyGO
T-Beam S3 Supreme. It rides in the car beside the passive Wi-Fi recorder and
logs what the recorder cannot see: where the car was, how fast, which way it
pointed, and how it moved — all stamped with GPS UTC so this log, the
recorder's, and the garage box's merge on wall clock.

## The button is the point

```
tap BOOT          mark this instant
hold BOOT 1.5 s   next page
```

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

## Build and flash

Builds on the build host, never on the Mac. Flashing from the Mac is fine.

```sh
rsync -a tbeam/ <host>:~/builds/carnetbeam/
ssh <host> 'podman run --rm -v ~/builds/carnetbeam:/work:Z \
  -v ~/builds/.cache/pio:/root/.platformio:Z -w /work localhost/cairn-pio:1 pio run'
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

### The IMU, and what has been ruled out

`WHO_AM_I` reads a constant **`0x3E`**. Five explanations were eliminated with
evidence on this board:

1. **Power** — ALDO2, the sensor rail, reads `on 3300mV` from the PMU.
2. **Bus contention with the card** — the IMU is probed before the card is ever
   mounted, with `SD_CS` driven high.
3. **SPI clock mode** — mode 0 and mode 3 both return the *identical* `0x3E`. A
   QMI8658 clocked in the wrong mode returns different garbage per mode.
4. **The driver** — a hand-rolled transaction and SensorLib's `SensorQMI8658`
   (the driver LilyGO's own example for this board uses, handed the already-begun
   SD SPI instance and CS 34) fail identically.
5. **I2C strapping** — a scan of both buses finds `0x3C 0x77 0x7C` and
   `0x34 0x51`. Nothing at 0x6A/0x6B, so it is not an I2C part here.

A stable *non-rail* byte, identical on both clock phases, means something is
driving MISO that is not a QMI8658 responding. On this board CS GPIO 34 does not
reach one. That matches the handoff's own open question: the V3.0/V3.1 revision
was never confirmed from the silkscreen, and §9.1 suspects this revision carries
a different part on that select line. **The next step is the V3.1 schematic**,
not more driver work.

The firmware reports this rather than hiding it: the MOTION page and the status
line both print both probe bytes and the conclusion, so the diagnosis travels
with the device.
