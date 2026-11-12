# imuprobe

A read-only QMI8658 diagnostic for the T-Beam S3 Supreme. It tests the two
datasheet-conformance faults that both the CarNet Beam firmware and LILYGO's own
example share, and nothing else.

## Why

`WHO_AM_I` reads a constant `0x3E` on this board. Earlier attempts cycled
through SPI modes, clock rates, dummy bytes, drivers and an ALDO2 power cycle.
Reading the QMI8658C datasheet afterwards showed two things every one of those
attempts had in common:

1. **System Turn On Time is 1.75 s**, and it "starts once VDDIO and VDD are
   within 1% of Final Value". Everything so far probed within a few hundred
   milliseconds of enabling the rail — about 35× inside spec.
2. **CS selects the interface.** Pin 12 is the "I2C/ I3C /SPI-UI selection Pin.
   (If 1: I2C-UI Mode: I2C/I3C communication enabled, SPI idle mode)". Every
   attempt let CS idle high while the part powered up.

## What it does

| Phase | Test |
|---|---|
| **A** | Control: read `WHO_AM_I` and `REVISION_ID` with the rail long since up |
| **B** | Power-cycle the sensor rail with CS idling **high**, wait 2.5 s, then read once a second for 10 s |
| **C** | Power-cycle the sensor rail with CS held **low** through power-up, wait 2.5 s, release CS, then read for 10 s |

Each read is done in both SPI mode 0 and mode 3 — not because that is
diagnostic (the part supports both, so matching results prove nothing) but
because it costs nothing and removes the question.

**It never writes to the sensor.** No soft reset, no CTRL registers. A write to
a part that is not answering only adds a variable.

## Reading the result

- Any line showing `WHO_AM_I m0=0x05` or `m3=0x05` — **the part works** and the
  cause was a datasheet-conformance bug, in which case CarNet Beam needs the
  same wait and CS handling.
- Every line `0x3E` — both hypotheses are eliminated. The next steps are
  physical: measure VDD (pin 8) and VDDIO (pin 5) at the chip's decoupling
  capacitors, and capture CS 34 / SCK 36 / MOSI 35 / MISO 37 during one
  register-0 read.

Then send `LILYGO-REPORT.md`.

## Flash

Restores nothing by itself — reflash CarNet Beam from `../dist` afterwards.

```sh
cd tbeam/imuprobe/dist
esptool --chip esp32s3 -p "$PORT" -b 460800 --before default-reset --after hard-reset \
  write-flash --flash-mode keep --flash-freq keep --flash-size keep \
  0x0 bootloader.bin 0x8000 partitions.bin 0xe000 boot_app0.bin 0x10000 firmware.bin
```
