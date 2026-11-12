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

static void railOff() {
    pmu.disableALDO2();
    delay(400);  // let the rail actually fall, not just the register change
}

// csLow: hold CS low through power-up, so the part sees SPI rather than I2C on
// its selection pin as its supplies come up.
static void railOn(bool csLow) {
    pinMode(PIN_IMU_CS, OUTPUT);
    digitalWrite(PIN_IMU_CS, csLow ? LOW : HIGH);
    pmu.setALDO2Voltage(3300);
    pmu.enableALDO2();
}

static void phase(const char *name, bool csLow) {
    Serial.printf("\n--- %s ---\n", name);
    Serial.printf("rail off, then on with CS %s; ALDO2 reads %s %dmV\n", csLow ? "LOW" : "HIGH",
                  pmu.isEnableALDO2() ? "on" : "off", pmu.getALDO2Voltage());
    railOff();
    uint32_t t0 = millis();
    railOn(csLow);
    // Nothing is read before the datasheet's turn-on time has elapsed: reading
    // early is what every previous attempt did.
    delay(SETTLE_MS);
    if (csLow) {
        digitalWrite(PIN_IMU_CS, HIGH);  // release it before the first transaction
        delay(10);
    }
    Serial.printf("waited %lums (spec: %lums)\n", (unsigned long)(millis() - t0), (unsigned long)TURN_ON_MS);
    // Then keep reading: a part that becomes ready late shows up here and
    // nowhere else.
    for (int i = 0; i < 10; i++) {
        readPair(csLow ? "C cs-low-at-power" : "B cs-high-at-power", millis() - t0);
        delay(1000);
    }
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

    phase("B power-cycle, CS idling HIGH through power-up", false);
    phase("C power-cycle, CS held LOW through power-up", true);

    Serial.println("\n=== done. If every line reads 0x3E, the two datasheet-conformance");
    Serial.println("=== hypotheses (turn-on time, CS-at-power-up) are both eliminated.");
}

void loop() { delay(1000); }
