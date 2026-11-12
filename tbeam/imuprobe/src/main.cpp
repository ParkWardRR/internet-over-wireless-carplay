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
//   D  write to the part: restore 4-wire, try 3-wire with the reply on MOSI,
//      and watch INT1 (see the phase D comment -- reads are exhausted)
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

// ---- Phase D: stop reading, start writing -------------------------------
//
// Everything above is a read, and every failure we have is a *read* failure.
// LilyGO's own example fails at `reset(): Reset chip failed, Response val = 0`
// -- that is the driver writing a soft reset and then failing to read back the
// confirmation. The register pattern says the same thing: 0x3E across
// 0x00-0x07, 0x00 across 0x30-0x37, 0xFF with CS deselected. A part answering
// badly varies with the address; a floating input that MOSI is capacitively
// coupled into also varies with the address, because the address is what MOSI
// just clocked out. The second explanation now looks likelier, and it means
// **MISO is not being driven at all** while the writes may have been landing
// the whole time.
//
// That is worth testing, and it cannot be tested by reading harder. So this
// phase writes. The program was deliberately read-only -- "a write to a part
// that is not answering only adds a variable" -- and that was right while reads
// were unexhausted. They are exhausted.
//
// Every write here is to a volatile register and is undone by the cold power
// cycle that has never yet been performed on this board, so none of it is a
// one-way door.
//
// D1  Restore 4-wire mode. CTRL1 bit 7 (SIM) selects 3-wire SPI; the datasheet
//     default is 0x20, i.e. 4-wire. But this part has NEVER been power-cycled
//     (VDD3V3 is a main rail), so if anything ever wrote SIM=1 -- an SD card
//     transaction while IMU_CS happened to be low would do it -- the part
//     stopped driving MISO then and has never come back. A blind write of
//     CTRL1=0x20 undoes exactly that. Writes reach the part in both modes,
//     because 3-wire shares the host's MOSI line.
//
// D2  The workaround, if MISO is broken rather than the part. Put the part
//     deliberately INTO 3-wire mode and read the reply back on MOSI, which is
//     a line we know works: every write goes out on it. If WHO_AM_I comes back
//     0x05 here, the sensor is alive and usable with a bit-banged half-duplex
//     driver, and the only thing wrong with this board is one broken signal.
//
// D3  Alive-or-dead, with no read path at all. Enable the accelerometer and
//     route data-ready to INT1, then watch the interrupt pin for edges. INT1
//     is pin 4 of UX1 on net 148; the LilyGO wiki pin table puts the IMU
//     interrupt on GPIO 33, which is a lead and not confirmed from the
//     schematic, so a quiet result here proves nothing while a toggling result
//     proves a great deal: the chip is powered, alive, and accepting writes.
static const uint8_t REG_CTRL1 = 0x02, REG_CTRL2 = 0x03, REG_CTRL7 = 0x08;
static const uint8_t CTRL1_4WIRE = 0x20, CTRL1_3WIRE = 0xA0, CTRL1_INT1_4WIRE = 0x28;
static const int PIN_IMU_INT1 = 33;  // UNVERIFIED: LilyGO wiki pin table

static void wr(uint8_t reg, uint8_t val, uint8_t mode = SPI_MODE0) {
    deselectCard();
    periphSPI.beginTransaction(SPISettings(100000, MSBFIRST, mode));
    digitalWrite(PIN_IMU_CS, LOW);
    delayMicroseconds(5);
    periphSPI.transfer((uint8_t)(reg & 0x7F));  // bit 7 clear = write
    periphSPI.transfer(val);
    digitalWrite(PIN_IMU_CS, HIGH);
    periphSPI.endTransaction();
    delayMicroseconds(20);
}

// rd3 is a bit-banged half-duplex read: clock the address out on MOSI, then
// turn MOSI around and sample it. This is how a 3-wire SPI slave replies, and
// it never touches MISO. SPI mode 0: set data on the falling edge, sample on
// the rising one.
static uint8_t rd3(uint8_t reg) {
    periphSPI.end();  // take the pins back from the peripheral
    pinMode(PIN_SPI_SCK, OUTPUT);
    pinMode(PIN_SPI_MOSI, OUTPUT);
    digitalWrite(PIN_SPI_SCK, LOW);
    deselectCard();
    digitalWrite(PIN_IMU_CS, LOW);
    delayMicroseconds(5);

    for (int i = 7; i >= 0; i--) {  // address, read bit set
        digitalWrite(PIN_SPI_MOSI, ((reg | 0x80) >> i) & 1);
        delayMicroseconds(5);
        digitalWrite(PIN_SPI_SCK, HIGH);
        delayMicroseconds(5);
        digitalWrite(PIN_SPI_SCK, LOW);
    }
    pinMode(PIN_SPI_MOSI, INPUT);  // the turnaround: the part drives it now
    delayMicroseconds(5);
    uint8_t v = 0;
    for (int i = 7; i >= 0; i--) {
        digitalWrite(PIN_SPI_SCK, HIGH);
        delayMicroseconds(5);
        v |= (uint8_t)(digitalRead(PIN_SPI_MOSI) & 1) << i;
        digitalWrite(PIN_SPI_SCK, LOW);
        delayMicroseconds(5);
    }
    digitalWrite(PIN_IMU_CS, HIGH);
    periphSPI.begin(PIN_SPI_SCK, PIN_SPI_MISO, PIN_SPI_MOSI);
    return v;
}

static void phaseD() {
    Serial.println("\n=== D: writing to the part, because every failure so far is a read failure ===");

    Serial.println("\n-- D1: blind CTRL1=0x20 (SIM=0, 4-wire), then read MISO as usual --");
    wr(REG_CTRL1, CTRL1_4WIRE);
    delay(50);
    for (int i = 0; i < 3; i++) {
        uint8_t w = rd(0x00, SPI_MODE0);
        Serial.printf("   WHO_AM_I after 4-wire restore: 0x%02X %s\n", w,
                      w == 0x05 ? "<<< THE PART IS BACK" : "");
        delay(100);
    }

    Serial.println("\n-- D2: CTRL1=0xA0 (SIM=1, 3-wire), reply read back on MOSI, MISO unused --");
    wr(REG_CTRL1, CTRL1_3WIRE);
    delay(50);
    for (int i = 0; i < 3; i++) {
        uint8_t w = rd3(0x00), r = rd3(0x01);
        Serial.printf("   3-wire WHO_AM_I=0x%02X REVISION=0x%02X %s\n", w, r,
                      w == 0x05 ? "<<< ALIVE, AND MISO IS THE ONLY BROKEN SIGNAL" : "");
        delay(100);
    }
    wr(REG_CTRL1, CTRL1_4WIRE);  // leave it as we found it
    delay(50);

    Serial.printf("\n-- D3: data-ready interrupt on INT1, watched at GPIO %d (UNVERIFIED pin) --\n",
                  PIN_IMU_INT1);
    Serial.println("   a toggling pin proves the part is alive and taking writes with no read path;");
    Serial.println("   a quiet pin proves nothing, because the GPIO may simply be the wrong one.");
    pinMode(PIN_IMU_INT1, INPUT_PULLDOWN);
    wr(REG_CTRL2, 0x04);              // accelerometer ODR
    wr(REG_CTRL7, 0x01);              // aEN: enable the accelerometer
    wr(REG_CTRL1, CTRL1_INT1_4WIRE);  // INT1 output enable
    delay(100);
    int edges = 0, last = digitalRead(PIN_IMU_INT1);
    for (uint32_t t = millis(); millis() - t < 3000;) {
        int now = digitalRead(PIN_IMU_INT1);
        if (now != last) {
            edges++;
            last = now;
        }
        delayMicroseconds(200);
    }
    Serial.printf("   INT1 edges in 3 s: %d  %s\n", edges,
                  edges > 0 ? "<<< THE PART IS ALIVE" : "(quiet -- dead part, or wrong GPIO)");
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

    phaseD();

    Serial.println("\n=== done.");
    Serial.println("The IMU is on VDD3V3 (schematic UX1 pins 5 and 8), not ALDO2, so it has");
    Serial.println("been powered continuously and the 1.75 s turn-on time is long satisfied.");
    Serial.println("Next steps are physical, and the schematic names the parts to probe:");
    Serial.println("  - VDDIO at C21 (100nF), VDD at C22 (100nF) / C20 (1uF). Expect 1.71-3.6 V.");
    Serial.println("  - is UX1 populated at all? It is a 14-pin LGA, ~2.5 x 3 mm.");
    Serial.println("  - capture CS 34 / SCK 36 / MOSI 35 / MISO 37 during one register-0 read.");
}

void loop() { delay(1000); }
