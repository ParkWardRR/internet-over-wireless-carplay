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
unmounted with CS 47 driven high; ALDO2 power cycle; a hand-rolled driver and
SensorLib's `SensorQMI8658::begin(SPIClass&, 34)`.

**Tested to datasheet spec:** waiting past the **1.75 s System Turn On Time**
(datasheet note: "starts once VDDIO and VDD are within 1% of Final Value") after
a rail power-cycle, and holding **CS low through power-up** given that pin 12 is
the "I2C/ I3C /SPI-UI selection Pin (If 1: I2C-UI Mode … SPI idle mode)".
Result: **[fill in from imuprobe output]**.

**Measured supplies at the chip:** VDD **[fill in]** V, VDDIO **[fill in]** V
(datasheet: 1.71–3.6 V each). **[Attach SPI capture of one register-0 read if
available.]**

**Questions**
1. Which AXP2101 rail powers the QMI8658 on this revision? The docs list ALDO1 as
   "BME280 Sensor & Display & MAG Sensor" and ALDO2 as "Sensor", without naming
   the IMU.
2. Is `0x3E` on `0x00`–`0x07` a pattern you have seen, and does it indicate
   anything specific?
3. Can you confirm from the V3.1 schematic what CS GPIO 34 connects to on this
   revision?
4. Is this consistent with a defective part or solder joint, and is an exchange
   appropriate?

**Related:** issue #255 is the same symptom; it ends with the author reporting
success in MicroPython but **no code or root cause was posted** before it was
auto-closed, so it is not usable as a fix.
