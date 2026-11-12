// imuprobe: why does the QMI8658 on this board not answer?
//
// Every earlier attempt violated the datasheet in the same way, and so did
// LilyGO's own example: the QMI8658C specifies a **System Turn On Time of
// 1.75 s**, which "starts once VDDIO and VDD are within 1% of Final Value"
// (datasheet, Electrical Characteristics). Both codebases probe within a few
// hundred milliseconds of enabling the sensor rail -- roughly 35x inside spec.
//
// The datasheet also says pin 12, CS, is the interface selector: "I2C/I3C/SPI-UI
// selection Pin. (If 1: I2C-UI Mode: I2C/I3C communication enabled, SPI idle
// mode)". So the level of CS while the part powers up may decide which
// interface it comes up in, and every attempt so far let CS idle high.
//
// This program tests those two things, and only those two, in a way that is
// hard to argue with:
//
//   A  read WHO_AM_I now, with the rail long since up (the control)
//   B  power-cycle the sensor rail with CS idling HIGH, wait past 1.75 s,
//      then read repeatedly for 10 s
//   C  power-cycle the sensor rail with CS held LOW through power-up,
//      wait past 1.75 s, then read repeatedly for 10 s
//
// It never writes to the sensor. No soft reset, no CTRL registers: a write to a
// part that is not answering only adds a variable, and the critique of the
// earlier work was right that reads come first.
//
// WHO_AM_I is register 0x00 and reads 0x05. REVISION_ID is 0x01. Both are read,
// because two registers disagreeing tells you more than one register failing.
#include <Arduino.h>
#include <SPI.h>
#include <Wire.h>
#include <XPowersLib.h>

#define PIN_SPI_SCK 36
#define PIN_SPI_MISO 37
#define PIN_SPI_MOSI 35
#define PIN_SD_CS 47
#define PIN_IMU_CS 34
#define PIN_I2C1_SDA 42
#define PIN_I2C1_SCL 41
#define ADDR_PMU 0x34

// The datasheet figure, plus margin. "System Turn On Time starts once VDDIO and
// VDD are within 1% of Final Value", so the clock starts at the rail, not at
// the enable command.
static const uint32_t TURN_ON_MS = 1750;
static const uint32_t SETTLE_MS = 2500;

static XPowersAXP2101 pmu;
static SPIClass periphSPI(HSPI);  // the bus LilyGO's own board support uses

static void deselectCard() {
    pinMode(PIN_SD_CS, OUTPUT);
    digitalWrite(PIN_SD_CS, HIGH);
}

// One register read: address with the read bit set, then one byte clocked out,
// CS low across both. 100 kHz: this bus is shared and goes through headers.
static uint8_t rd(uint8_t reg, uint8_t mode = SPI_MODE0, uint32_t hz = 100000) {
    deselectCard();
    periphSPI.beginTransaction(SPISettings(hz, MSBFIRST, mode));
    digitalWrite(PIN_IMU_CS, LOW);
    delayMicroseconds(5);
    periphSPI.transfer((uint8_t)(reg | 0x80));
    uint8_t v = periphSPI.transfer(0x00);
    digitalWrite(PIN_IMU_CS, HIGH);
    periphSPI.endTransaction();
    delayMicroseconds(10);
    return v;
}

static void readPair(const char *tag, uint32_t sinceMs) {
    uint8_t w0 = rd(0x00, SPI_MODE0), r0 = rd(0x01, SPI_MODE0);
    uint8_t w3 = rd(0x00, SPI_MODE3), r3 = rd(0x01, SPI_MODE3);
    Serial.printf("%-22s t+%-6lums  WHO_AM_I m0=0x%02X m3=0x%02X  REVISION m0=0x%02X m3=0x%02X  %s\n",
                  tag, (unsigned long)sinceMs, w0, w3, r0, r3,
                  (w0 == 0x05 || w3 == 0x05) ? "<<< WHO_AM_I CORRECT" : "");
}

// NOTE (from the V3.1 schematic, read 11 Nov 2026): the earlier version of this
// program power-cycled ALDO2 between phases. That was pointless. The schematic
// shows the QMI8658A (designator UX1) with **pin 5 VDDIO and pin 8 VDD both tied
// to VDD3V3**, decoupled by C21 (100nF), C22 (100nF) and C20 (1uF). ALDO2 does
// not reach it. VDD3V3 also feeds the RTC and the programming header, so it is a
// main rail: nothing in software can cycle it, and an ESP32 reset does not drop
// it either.
//
// Two consequences:
//   - no test ever run on this board has power-cycled the IMU;
//   - the 1.75 s turn-on time has been satisfied for hours, so that hypothesis
//     cannot explain a failure on a board that has been powered all along.
//
// Only a full cold power-down -- USB out and the 18650 removed -- cycles it, and
// that cannot be done from here. So this program now does the one thing software
// still can: read widely and patiently, and print something worth sending to
// LilyGO.

// dump reads a span of registers in both SPI modes at several clock rates. The
// pattern across addresses is the evidence: a floating bus reads the same byte
// everywhere, while a part that is answering badly varies with the address.
static void dump(const char *tag, uint32_t hz) {
    Serial.printf("\n-- %s @ %lu Hz --\n", tag, (unsigned long)hz);
    for (uint8_t base = 0x00; base < 0x20; base += 8) {
        Serial.printf("  0x%02X: ", base);
        for (uint8_t i = 0; i < 8; i++) Serial.printf("%02X ", rd(base + i, SPI_MODE0, hz));
        Serial.print(" |m3| ");
        for (uint8_t i = 0; i < 8; i++) Serial.printf("%02X ", rd(base + i, SPI_MODE3, hz));
        Serial.println();
    }
    // The registers the datasheet names, called out so they are not lost in the grid.
    Serial.printf("  WHO_AM_I(0x00)=0x%02X  REVISION_ID(0x01)=0x%02X   (expect 0x05, then a revision)\n",
                  rd(0x00, SPI_MODE0, hz), rd(0x01, SPI_MODE0, hz));
}

void setup() {
    Serial.begin(115200);
    for (uint32_t t = millis(); !Serial && millis() - t < 2500;) delay(10);
    Serial.println("\n=== imuprobe: QMI8658 read-only diagnostic ===");
    Serial.println("datasheet: WHO_AM_I 0x00 = 0x05; System Turn On Time 1.75s from rails stable");
    Serial.println("           CS pin selects interface (high = I2C-UI, SPI idle)");

    Wire1.begin(PIN_I2C1_SDA, PIN_I2C1_SCL);
    if (!pmu.begin(Wire1, ADDR_PMU, PIN_I2C1_SDA, PIN_I2C1_SCL)) {
        Serial.println("FATAL: AXP2101 not found; every rail is off and nothing below means anything");
        return;
    }
    pmu.setALDO2Voltage(3300);
    pmu.enableALDO2();
    pinMode(PIN_IMU_CS, OUTPUT);
    digitalWrite(PIN_IMU_CS, HIGH);
    deselectCard();
    periphSPI.begin(PIN_SPI_SCK, PIN_SPI_MISO, PIN_SPI_MOSI);

    Serial.printf("\n--- A control: rail has been up since boot ---\nALDO2 %s %dmV\n",
                  pmu.isEnableALDO2() ? "on" : "off", pmu.getALDO2Voltage());
    delay(SETTLE_MS);
    for (int i = 0; i < 3; i++) {
        readPair("A rail-up-since-boot", millis());
        delay(500);
    }

    for (uint32_t hz : {100000UL, 500000UL, 4000000UL}) dump("register dump", hz);

    Serial.println("\n=== done.");
    Serial.println("The IMU is on VDD3V3 (schematic UX1 pins 5 and 8), not ALDO2, so it has");
    Serial.println("been powered continuously and the 1.75 s turn-on time is long satisfied.");
    Serial.println("Next steps are physical, and the schematic names the parts to probe:");
    Serial.println("  - VDDIO at C21 (100nF), VDD at C22 (100nF) / C20 (1uF). Expect 1.71-3.6 V.");
    Serial.println("  - is UX1 populated at all? It is a 14-pin LGA, ~2.5 x 3 mm.");
    Serial.println("  - capture CS 34 / SCK 36 / MOSI 35 / MISO 37 during one register-0 read.");
}

void loop() { delay(1000); }
