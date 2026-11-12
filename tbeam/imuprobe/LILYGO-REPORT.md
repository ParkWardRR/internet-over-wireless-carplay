# Draft report for LILYGO — QMI8658 unreadable on one T-Beam S3 Supreme

Framed as the evidence supports: the official example cannot read `WHO_AM_I` on
this specimen. **Not** "hardware fault confirmed" — that has not been shown.

Fill in the bracketed items before sending.

---

**Title:** Official QMI8658_GetDataExample cannot read WHO_AM_I on one T-Beam S3 Supreme (SX1262)

**Board:** T-Beam S3 Supreme, 915 MHz / SX1262, u-blox MAX-M10, SH1106 OLED.
Revision: **[V3.0 or V3.1 — PPS LED blue = V3.0, red = V3.1; V3.1 also has
silkscreen on the back of the PCB]**. The magnetometer answers at `0x7C`
(QMC6309), which is the V3.1 part.

**Summary:** Every other peripheral works. The QMI8658 never returns
`WHO_AM_I = 0x05`, including under LILYGO's own unmodified example.

**With the unmodified official example** (`examples/Sensor/QMI8658_GetDataExample`,
built `-DT_BEAM_S3_SUPREME_SX1262`, LilyGo-LoRa-Series commit **[fill in]**,
platform espressif32@6.13.0, SensorLib **[fill in]**, XPowersLib 0.2.6):

```
UBlox GNSS init succeeded, using UBlox GNSS Module
Switching RTC Source to 32.768Khz succeeded, using 32K XTAL
[E][SensorQMI8658.hpp:385] reset(): Reset chip failed, Response val = 0 - 0x0
Failed to find QMI8658 - check your wiring!
```

The GNSS and RTC come up in that same boot, so `setupBoards()` ran.

**Raw register reads** (own read-only probe, CS 34, SD CS 47 held high, card not
mounted, 100 kHz, SPI mode 0 and mode 3 identical):

| Registers | Returns |
|---|---|
| `0x00`–`0x07` | `0x3E` |
| `0x0F`, `0x30`–`0x37`, `0x75` | `0x00` |
| CS deselected | `0xFF` |

Address-dependent, and it changes when CS is asserted.

**Tested and unchanged:** SPI mode 0 and 3; 100 kHz / 500 kHz / 4 MHz; dummy
byte between address and data; `SPIClass(FSPI)` and `SPIClass(HSPI)`; SD card
unmounted with CS 47 driven high; a hand-rolled driver and SensorLib's
`SensorQMI8658::begin(SPIClass&, 34)`.

**Two hypotheses the V3.1 schematic rules out, in case they come up:**

- *An earlier power-cycle test was meaningless.* We cycled **ALDO2**, on the
  strength of the rail table listing it as "Sensor". The schematic shows UX1 pin
  5 (VDDIO) and pin 8 (VDD) **both tied to VDD3V3**, so the IMU never lost power.
  Since VDD3V3 is a main rail, **no test performed on this board has ever
  power-cycled the IMU**, and nothing in software can.
- *The 1.75 s System Turn On Time is therefore long satisfied* ("starts once
  VDDIO and VDD are within 1% of Final Value"), so it cannot explain a failure on
  a board that has been powered continuously.
- *CS as interface selection is level-based, not latched* — pin 12 is the
  "I2C/ I3C /SPI-UI selection Pin (If 1: I2C-UI Mode … SPI idle mode)" — and CS
  is driven low for every transaction, so the part should be in SPI mode when it
  is read.

**Not yet measured** (needs a meter on the board, and is the next thing we will
do): VDD at C22/C20 and VDDIO at C21, datasheet range 1.71–3.6 V each; whether
**UX1** is populated at all; and an SPI capture of one register-0 read.

**Questions**
1. Is `0x3E` on `0x00`–`0x07` a pattern you have seen, and does it indicate
   anything specific? It is address-dependent and changes when CS is asserted, so
   it does not look like a simply floating bus.
2. Can you confirm from the V3.1 schematic that `IMU_CS` is GPIO 34 on this
   revision, and that UX1 shares `SPI_MOSI/SCK/MISO` with the SD card through the
   0R links R32/R33/R34?
3. Is this consistent with a defective part or solder joint, and is an exchange
   appropriate?

**Related:** issue #255 is the same symptom; it ends with the author reporting
success in MicroPython but **no code or root cause was posted** before it was
auto-closed, so it is not usable as a fix.
