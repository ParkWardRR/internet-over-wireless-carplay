// CarNet Beam: a drive-test companion for the CarPlay departure investigation.
//
// It rides in the car next to the passive Wi-Fi recorder and logs what the
// recorder cannot see: where the car was, how fast, which way it was pointing,
// and how it was moving. Everything is stamped with GPS UTC so the three logs
// -- this one, the in-car recorder's, and the garage box's -- merge on wall
// clock.
//
// The button is the point. Tap it the moment CarPlay drops and the instant goes
// to the card immediately, with position and speed. Up to now that moment was a
// human saying "about then", and the stream-stop detector had nothing to be
// checked against.
//
//	tap BOOT           mark this instant
//	hold BOOT 1.5 s    next page
//
// Bring-up order matters. Almost every rail is off at reset (the PMU owns
// them), and the IMU shares the SD card's SPI bus, so the IMU is identified
// before the card is ever mounted -- with the card's select held high -- which
// is how LilyGO's own example reads it.
#include <Arduino.h>
#include <SPI.h>
#include <U8g2lib.h>
#include <Wire.h>
#include <XPowersLib.h>

#include "config.h"
#include "drivelog.h"
#include "nmea.h"
#include "screens.h"
#include "sensors.h"

static U8G2_SH1106_128X64_NONAME_F_HW_I2C oled(U8G2_R0, U8X8_PIN_NONE);
static XPowersAXP2101 pmu;
// HSPI, not FSPI. On the ESP32-S3 the global `SPI` object is FSPI, so a second
// SPIClass on FSPI shares a peripheral with whatever else touches `SPI`.
// LilyGO's own board support declares this bus as `SPIClass SDCardSPI(HSPI)`,
// and that difference is the only thing separating this firmware's IMU bring-up
// from the vendor example's.
static SPIClass periphSPI(HSPI);
static nmea::Parser gps;
static sensors::Mag mag;
static sensors::Imu imu;
static drivelog::Log logger;

static int page = screens::PAGE_DRIVE;
static uint32_t markFlashUntil = 0;
static uint32_t lastMarkNumber = 0;
static char lastMarkTime[16] = {0};
static bool sdOk = false;
static bool screenOn = true;
// Motion derived from GPS, because the IMU on this board does not answer (see
// README). Speed and course deltas give longitudinal acceleration and turn
// rate, which is most of what a departure analysis wants from the IMU anyway.
static double lastSpeedMps = 0, lastCourse = 0;
static uint32_t lastMotionMs = 0;
static float gpsAccelMps2 = 0, gpsTurnDps = 0;

// ---- power ---------------------------------------------------------------
//
// At reset the GNSS, the IMU and the card have no power at all. If a part
// "isn't there", the rail is off, not the part: that cost a previous build the
// entire IMU.
static bool powerBegin() {
    Wire1.begin(PIN_I2C1_SDA, PIN_I2C1_SCL);
    if (!pmu.begin(Wire1, ADDR_PMU, PIN_I2C1_SDA, PIN_I2C1_SCL)) return false;
    pmu.setALDO2Voltage(3300);
    pmu.enableALDO2();  // "Sensor" rail: the QMI8658 IMU
    pmu.setALDO4Voltage(3300);
    pmu.enableALDO4();  // GNSS
    pmu.setBLDO1Voltage(3300);
    pmu.enableBLDO1();  // SD card
    pmu.setALDO3Voltage(3300);
    pmu.enableALDO3();  // OLED / misc
    // The power key is the board's other button. Short press is ours to use;
    // a long press is handled by the PMU in hardware and powers the board off.
    pmu.disableIRQ(XPOWERS_AXP2101_ALL_IRQ);
    pmu.clearIrqStatus();
    pmu.enableIRQ(XPOWERS_AXP2101_PKEY_SHORT_IRQ | XPOWERS_AXP2101_PKEY_LONG_IRQ);
    pmu.disableTSPinMeasure();
    pmu.enableBattVoltageMeasure();
    pmu.enableVbusVoltageMeasure();
    delay(50);  // let the rails settle before anything is probed
    return true;
}

// ---- time ----------------------------------------------------------------
//
// GPS UTC when there is a fix, otherwise milliseconds since boot, clearly
// marked as such so a row can never be mistaken for an absolute time.
static void isoNow(char *out, size_t n) {
    const nmea::Fix &f = gps.fix;
    if (f.year > 2000) {
        snprintf(out, n, "%04d-%02d-%02dT%02d:%02d:%06.3fZ", f.year, f.mon, f.day, f.hour, f.min, f.sec);
    } else {
        snprintf(out, n, "boot+%lu.%03lus", (unsigned long)(millis() / 1000),
                 (unsigned long)(millis() % 1000));
    }
}

static void doMark() {
    char t[32];
    isoNow(t, sizeof(t));
    logger.mark(t, (int)(gps.fix.speedMph() + 0.5), gps.fix.lat, gps.fix.lon,
                mag.present ? mag.headingDeg : -1.0f, imu.present ? imu.magnitudeG() : -1.0f);
    lastMarkNumber = logger.marks;
    // HH:MM:SS if we have real time, else the boot offset as-is.
    snprintf(lastMarkTime, sizeof(lastMarkTime), "%s", strlen(t) > 11 ? t + 11 : t);
    markFlashUntil = millis() + MARK_FLASH_MS;
    imu.peakG = 0;  // peak is "since the last mark", which is what a drive wants
    Serial.printf("[mark] %lu %s %dmph\n", (unsigned long)logger.marks, t,
                  (int)(gps.fix.speedMph() + 0.5));
    // ...and as a machine-readable line, so the recorder's timeline gets the
    // mark at the instant it happened rather than after the drive.
    Serial.printf("$CNBMARK,%s,%lu,%.1f,%.6f,%.6f,%.1f\n", t, (unsigned long)logger.marks,
                  gps.fix.speedMph(), gps.fix.lat, gps.fix.lon,
                  mag.present ? mag.headingDeg : -1.0f);
}

// ---- the power key -------------------------------------------------------
//
// Short press blanks or unblanks the screen and flushes the card at once. Both
// matter in a car: the OLED is bright at night, and a flush means the last few
// seconds are on the card before you pull the power. A long press is the PMU's
// own hardware power-off and needs no code.
static void powerKeyPoll() {
    static uint32_t last = 0;
    if (millis() - last < 200) return;  // the PMU is on I2C; do not hammer it
    last = millis();
    pmu.getIrqStatus();
    if (pmu.isPekeyShortPressIrq()) {
        screenOn = !screenOn;
        logger.flush();
        Serial.printf("[key] short press: screen %s, log flushed\n", screenOn ? "on" : "off");
    }
    if (pmu.isPekeyLongPressIrq()) {
        // The PMU will cut power itself. Get the card consistent first.
        Serial.println("[key] long press: flushing before power off");
        logger.flush();
    }
    pmu.clearIrqStatus();
}

// ---- button: tap marks, hold turns the page -------------------------------
static void buttonPoll() {
    static bool down = false;
    static uint32_t downAt = 0;
    static bool consumed = false;
    bool pressed = digitalRead(PIN_BUTTON) == LOW;

    if (pressed && !down) {
        down = true;
        downAt = millis();
        consumed = false;
    } else if (pressed && down && !consumed && millis() - downAt >= PAGE_HOLD_MS) {
        page = (page + 1) % screens::PAGE_COUNT;
        markFlashUntil = 0;  // a page change dismisses the confirmation
        consumed = true;     // one action per press
    } else if (!pressed && down) {
        down = false;
        if (!consumed && millis() - downAt < MARK_TAP_MS) doMark();
    }
}

void setup() {
    Serial.begin(115200);
    // The console is native USB-CDC, so the port detaches and re-enumerates
    // when this app boots: anything printed before the host re-attaches is
    // lost. Wait briefly for it, but never block a car install on a laptop
    // being plugged in.
    for (uint32_t t0 = millis(); !Serial && millis() - t0 < 2500;) delay(10);
    Serial.println("\n[carnetbeam] drive-test companion");

    pinMode(PIN_BUTTON, INPUT_PULLUP);
    bool pmuOk = powerBegin();
    Serial.printf("[pmu] %s\n", pmuOk ? "AXP2101 up, rails on" : "AXP2101 NOT FOUND: rails are off");
    // Rail readback. Almost nothing on this board is powered until the PMU is
    // told to, and "the part is dead" and "its rail is off" look identical from
    // the bus -- ALDO2 feeds the IMU, so this is the first thing to rule out.
    if (pmuOk) {
        Serial.printf("[pmu] ALDO2(imu) %s %dmV  ALDO3(oled) %s %dmV  ALDO4(gnss) %s %dmV  BLDO1(sd) %s %dmV\n",
                      pmu.isEnableALDO2() ? "on" : "OFF", pmu.getALDO2Voltage(),
                      pmu.isEnableALDO3() ? "on" : "OFF", pmu.getALDO3Voltage(),
                      pmu.isEnableALDO4() ? "on" : "OFF", pmu.getALDO4Voltage(),
                      pmu.isEnableBLDO1() ? "on" : "OFF", pmu.getBLDO1Voltage());
    }

    Wire.begin(PIN_I2C0_SDA, PIN_I2C0_SCL);
    oled.begin();
    oled.setContrast(255);

    // The SPI bus the card and the IMU share. Begun once, here, and handed to
    // both -- the card's driver must not be the one to initialise it.
    periphSPI.begin(PIN_SPI_SCK, PIN_SPI_MISO, PIN_SPI_MOSI);

    // IMU first, with no card mounted: they share MISO, and a mounted card is
    // the most likely reason a previous attempt read a constant byte from every
    // register.
    bool imuOk = imu.begin(periphSPI);
    Serial.printf("[imu] %s (who_am_i 0x%02X, %s)\n", imuOk ? "QMI8658 ok" : "absent", imu.whoami, imu.why);

    bool magOk = mag.begin(Wire);
    Serial.printf("[mag] %s\n", magOk ? "QMC6309 ok" : "absent");

    sdOk = logger.begin(periphSPI);
    Serial.printf("[sd] %s\n", sdOk ? "mounted, logging to " SD_DIR : "no card: logging to RAM only");

    pinMode(PIN_GPS_EN, OUTPUT);
    digitalWrite(PIN_GPS_EN, HIGH);
    Serial2.begin(GPS_BAUD, SERIAL_8N1, PIN_GPS_RX, PIN_GPS_TX);
    Serial.println("[gps] UART2 open at 9600");
}

void loop() {
    static uint32_t lastRow = 0, lastFlush = 0, lastImu = 0, lastDraw = 0, lastRemount = 0, lastStatus = 0;
    uint32_t now = millis();

    while (Serial2.available()) gps.feed((char)Serial2.read());
    buttonPoll();
    powerKeyPoll();

    if (now - lastImu >= 1000 / IMU_HZ) {
        lastImu = now;
        imu.read();
    }

    // One row a second, which is the resolution the other two logs have.
    if (now - lastRow >= 1000) {
        lastRow = now;
        mag.read();
        // Longitudinal acceleration and turn rate from GPS. Only while there is
        // a fix: without one, speed and course are meaningless rather than zero.
        if (gps.fix.valid && lastMotionMs != 0) {
            double dt = (now - lastMotionMs) / 1000.0;
            double mps = gps.fix.speedKn * 0.514444;
            if (dt > 0.2) {
                gpsAccelMps2 = (float)((mps - lastSpeedMps) / dt);
                double dc = gps.fix.courseDeg - lastCourse;
                while (dc > 180) dc -= 360;      // shortest way round the circle
                while (dc < -180) dc += 360;
                gpsTurnDps = (float)(dc / dt);
            }
            lastSpeedMps = mps;
            lastCourse = gps.fix.courseDeg;
        } else if (gps.fix.valid) {
            lastSpeedMps = gps.fix.speedKn * 0.514444;
            lastCourse = gps.fix.courseDeg;
        } else {
            gpsAccelMps2 = gpsTurnDps = 0;
        }
        lastMotionMs = now;
        char t[32], line[320];
        isoNow(t, sizeof(t));
        snprintf(line, sizeof(line),
                 "%s,%d,%d,%.6f,%.6f,%.1f,%.2f,%.1f,%.1f,%.1f,%.1f,%.1f,%.1f,"
                 "%+.3f,%+.3f,%+.3f,%+.1f,%+.1f,%+.1f,%.3f,%d,%lu,%+.2f,%+.1f",
                 t, gps.fix.valid ? 1 : 0, gps.fix.sats, gps.fix.lat, gps.fix.lon, gps.fix.altM,
                 gps.fix.hdop, gps.fix.speedMph(), gps.fix.courseDeg,
                 mag.present ? mag.headingDeg : -1.0f, mag.x, mag.y, mag.z,
                 imu.ax, imu.ay, imu.az, imu.gx, imu.gy, imu.gz, imu.magnitudeG(),
                 pmu.getBattVoltage(), (unsigned long)(now / 1000), gpsAccelMps2, gpsTurnDps);
        logger.push(line);

        // One machine-readable line a second for the in-car recorder over USB.
        // The recorder's Pi has no network in the car, so this is where its log
        // gets GPS time to align against -- and the recorder writes it down
        // rather than stepping its clock mid-capture, which would be worse.
        Serial.printf("$CNB,%s,%d,%d,%.6f,%.6f,%.1f,%.1f,%.2f,%+.2f,%+.1f,%lu,%d\n", t,
                      gps.fix.valid ? 1 : 0, gps.fix.sats, gps.fix.lat, gps.fix.lon,
                      gps.fix.speedMph(), mag.present ? mag.headingDeg : -1.0f,
                      gps.fix.hdop, gpsAccelMps2, gpsTurnDps,
                      (unsigned long)logger.marks, pmu.getBattVoltage());
    }

    if (now - lastFlush >= LOG_FLUSH_MS) {
        lastFlush = now;
        logger.flush();
        sdOk = logger.mounted;
    }
    // A card pulled and pushed back should start working again on its own.
    if (!logger.mounted && now - lastRemount >= 30000) {
        lastRemount = now;
        logger.remount(periphSPI);
        if (logger.mounted) imu.configure();  // the bus was torn down and rebuilt
    }

    // A status line every 10 s, the way the mesh firmware does it. Without one
    // a board that boots fine looks identical to a board that hung, because
    // the setup lines are long gone by the time anyone attaches a console.
    if (now - lastStatus >= 10000) {
        lastStatus = now;
        static int railEchoes = 3;
        if (railEchoes-- > 0) {
            Serial.printf("[pmu] ALDO2(imu) %s %dmV  ALDO4(gnss) %s %dmV  BLDO1(sd) %s %dmV\n",
                          pmu.isEnableALDO2() ? "on" : "OFF", pmu.getALDO2Voltage(),
                          pmu.isEnableALDO4() ? "on" : "OFF", pmu.getALDO4Voltage(),
                          pmu.isEnableBLDO1() ? "on" : "OFF", pmu.getBLDO1Voltage());
            // The QMI8658 can be strapped for I2C (0x6A/0x6B) rather than SPI.
            // If this revision does that, no SPI select line will ever answer.
            // Scanning here rather than in setup, because setup's output is
            // lost when USB-CDC re-enumerates as the app boots.
            for (int b = 0; b < 2; b++) {
                TwoWire &w = (b == 0) ? Wire : Wire1;
                Serial.printf("[i2c%d]", b);
                for (uint8_t a = 1; a < 127; a++) {
                    w.beginTransmission(a);
                    if (w.endTransmission() == 0) Serial.printf(" 0x%02X", a);
                }
                Serial.println();
            }
        }
        Serial.printf("[status] up %lus  gps %s %dsat (nmea %lu bad %lu)  mag %s  imu %s(m0 0x%02X m3 0x%02X: %s)  "
                      "sd %s %luKB drop %lu  rows %lu marks %lu  batt %dmV %d%%  %.0fC\n",
                      (unsigned long)(now / 1000), gps.fix.valid ? "fix" : "nofix", gps.fix.sats,
                      (unsigned long)gps.stats.sentences, (unsigned long)gps.stats.badChecksum,
                      mag.present ? "ok" : "--", imu.present ? "ok" : "--", imu.whoami, imu.whoami3, imu.why,
                      logger.mounted ? "ok" : "--", (unsigned long)(logger.written / 1024),
                      (unsigned long)logger.dropped, (unsigned long)logger.rows,
                      (unsigned long)logger.marks, pmu.getBattVoltage(), pmu.getBatteryPercent(),
                      temperatureRead());
    }

    if (now - lastDraw >= 200) {
        lastDraw = now;
        if (!screenOn) {
            oled.clearBuffer();
            oled.sendBuffer();
            return;
        }
        screens::View v;
        v.page = page;
        v.fix = &gps.fix;
        v.gpsStats = &gps.stats;
        v.mag = &mag;
        v.imu = &imu;
        v.log = &logger;
        v.uptimeS = now / 1000;
        v.battMv = pmu.getBattVoltage();
        v.battPct = pmu.getBatteryPercent();
        v.charging = pmu.isCharging();
        v.chipTempC = temperatureRead();
        v.sdOk = sdOk;
        v.gpsAccelMps2 = gpsAccelMps2;
        v.gpsTurnDps = gpsTurnDps;
        v.markFlash = now < markFlashUntil;
        v.markNumber = lastMarkNumber;
        snprintf(v.markTime, sizeof(v.markTime), "%s", lastMarkTime);
        screens::render(oled, v);
    }
}
