// The two motion sensors: a QMC6309 magnetometer on I2C bus 0, and a QMI8658
// accelerometer/gyro on the SD card's SPI bus.
#pragma once
#include <Arduino.h>
#include <SPI.h>
#include <Wire.h>

#include <SensorQMI8658.hpp>

#include "config.h"

namespace sensors {

static bool i2cRead(TwoWire &w, uint8_t addr, uint8_t reg, uint8_t *buf, size_t n) {
    w.beginTransmission(addr);
    w.write(reg);
    if (w.endTransmission(false) != 0) return false;
    if (w.requestFrom((int)addr, (int)n) != (int)n) return false;
    for (size_t i = 0; i < n; i++) buf[i] = (uint8_t)w.read();
    return true;
}
static bool i2cWrite(TwoWire &w, uint8_t addr, uint8_t reg, uint8_t v) {
    w.beginTransmission(addr);
    w.write(reg);
    w.write(v);
    return w.endTransmission() == 0;
}

// ---- QMC6309 magnetometer -------------------------------------------------
//
// This init sequence is the one already proven on this board: chip id 0x90 at
// register 0x00 on address 0x7C, soft reset through 0x0B, then set range and
// mode. The OLED also answers at 0x3C, so identification is by chip id, never
// by address alone.
struct Mag {
    TwoWire *w = nullptr;
    bool present = false;
    float x = 0, y = 0, z = 0;      // microtesla
    float headingDeg = 0;
    uint32_t reads = 0, failures = 0;
    static constexpr float UT_PER_COUNT = 1000.0f / 80.0f;  // 8 gauss range

    bool begin(TwoWire &bus) {
        w = &bus;
        uint8_t id = 0;
        if (!i2cRead(bus, ADDR_MAG, 0x00, &id, 1) || id != 0x90) return false;
        i2cWrite(bus, ADDR_MAG, 0x0B, 0x80);  // soft reset
        delay(10);
        i2cWrite(bus, ADDR_MAG, 0x0B, 0x00);
        delay(10);
        // 0x0B: range field; 0x0A: mode continuous + output data rate
        if (!i2cWrite(bus, ADDR_MAG, 0x0B, 0x02 << 2)) return false;
        if (!i2cWrite(bus, ADDR_MAG, 0x0A, 0x03 | (0x02 << 4))) return false;
        present = true;
        return true;
    }

    bool read() {
        if (!present) return false;
        uint8_t d[6];
        if (!i2cRead(*w, ADDR_MAG, 0x01, d, 6)) {
            failures++;
            return false;
        }
        int16_t rx = (int16_t)((d[1] << 8) | d[0]);
        int16_t ry = (int16_t)((d[3] << 8) | d[2]);
        int16_t rz = (int16_t)((d[5] << 8) | d[4]);
        x = rx * UT_PER_COUNT;
        y = ry * UT_PER_COUNT;
        z = rz * UT_PER_COUNT;
        // Flat-earth heading. No tilt compensation and no calibration, so it is
        // a bearing trend for a drive log, not a navigation instrument.
        float h = atan2f(y, x) * 180.0f / (float)M_PI;
        if (h < 0) h += 360.0f;
        headingDeg = h;
        reads++;
        return true;
    }
};

// ---- QMI8658 IMU, on the SD card's SPI bus --------------------------------
//
// A hand-rolled driver on this board reads a constant 0x3E from every register,
// on both SPI modes, with the card's select held high and before the card is
// mounted. That was reproduced here, so the transaction is not obviously the
// problem -- which leaves the driver itself.
//
// So this uses SensorLib's SensorQMI8658, the driver LilyGO's own QMI8658
// example for this board uses, handed the already-begun SD SPI instance and
// CS 34 exactly as that example does. An owner report (LilyGO issue #140) has
// it working this way. The hand-rolled probe is kept purely as a diagnostic:
// when SensorLib fails, `why` reports what WHO_AM_I actually returned on each
// SPI mode, which is the thing worth knowing next.
struct Imu {
    static constexpr uint8_t WHO_AM_I = 0x00;
    static constexpr uint8_t WHO_AM_I_VALUE = 0x05;
    static constexpr uint32_t PROBE_HZ = 100000;

    SensorQMI8658 qmi;
    bool present = false;
    uint8_t whoami = 0;          // raw WHO_AM_I from the diagnostic probe (mode 0)
    uint8_t whoami3 = 0;         // the same read clocked in mode 3
    int chipId = -1;             // what SensorLib reports, if it attached
    const char *why = "not probed";
    float ax = 0, ay = 0, az = 0;   // g
    float gx = 0, gy = 0, gz = 0;   // deg/s
    uint32_t reads = 0, failures = 0;
    float peakG = 0;                // largest |a| since the last mark

    static void deselectCard() {
        pinMode(PIN_SD_CS, OUTPUT);
        digitalWrite(PIN_SD_CS, HIGH);
    }

    // The diagnostic read: one register, CS held across both bytes, no dummy.
    static uint8_t rawProbe(SPIClass &bus, uint8_t mode) {
        deselectCard();
        pinMode(PIN_IMU_CS, OUTPUT);
        digitalWrite(PIN_IMU_CS, HIGH);
        bus.beginTransaction(SPISettings(PROBE_HZ, MSBFIRST, mode));
        digitalWrite(PIN_IMU_CS, LOW);
        delayMicroseconds(2);
        bus.transfer((uint8_t)(WHO_AM_I | 0x80));
        uint8_t v = bus.transfer(0x00);
        digitalWrite(PIN_IMU_CS, HIGH);
        bus.endTransaction();
        delayMicroseconds(10);
        return v;
    }

    // Call before the SD card is mounted: the two share MISO.
    bool begin(SPIClass &bus) {
        deselectCard();
        delay(10);

        // SensorLib first. -1 for the pins means "use the bus as already begun".
        if (qmi.begin(bus, PIN_IMU_CS, -1, -1, -1)) {
            chipId = qmi.getChipID();
            qmi.configAccelerometer(SensorQMI8658::ACC_RANGE_8G, SensorQMI8658::ACC_ODR_125Hz,
                                    SensorQMI8658::LPF_MODE_0);
            qmi.configGyroscope(SensorQMI8658::GYR_RANGE_1024DPS, SensorQMI8658::GYR_ODR_112_1Hz,
                                SensorQMI8658::LPF_MODE_3);
            qmi.enableAccelerometer();
            qmi.enableGyroscope();
            present = true;
            why = "sensorlib attached";
            return true;
        }

        // It did not attach. Report what the bus actually says, on both modes,
        // because "absent" and "answering the wrong thing" need different work.
        uint8_t m0 = rawProbe(bus, SPI_MODE0);
        uint8_t m3 = rawProbe(bus, SPI_MODE3);
        whoami = m0;
        whoami3 = m3;
        if (m0 == WHO_AM_I_VALUE || m3 == WHO_AM_I_VALUE) {
            why = "who_am_i ok but sensorlib begin failed";
        } else if (m0 == 0xFF && m3 == 0xFF) {
            why = "miso idle high: rail off or not selected";
        } else if (m0 == 0x00 && m3 == 0x00) {
            why = "miso idle low";
        } else if (m0 == m3) {
            why = "same byte on both spi modes: something else drives miso";
        } else {
            why = "unexpected who_am_i";
        }
        return false;
    }

    void configure() {
        if (!present) return;
        qmi.enableAccelerometer();
        qmi.enableGyroscope();
    }

    bool read() {
        if (!present) return false;
        if (!qmi.getDataReady()) return false;
        bool ok = qmi.getAccelerometer(ax, ay, az);
        ok = qmi.getGyroscope(gx, gy, gz) && ok;
        if (!ok) {
            failures++;
            return false;
        }
        float mag = sqrtf(ax * ax + ay * ay + az * az);
        if (mag > peakG) peakG = mag;
        reads++;
        return true;
    }

    float magnitudeG() const { return sqrtf(ax * ax + ay * ay + az * az); }
    float pitchDeg() const { return atan2f(-ax, sqrtf(ay * ay + az * az)) * 180.0f / (float)M_PI; }
    float rollDeg() const { return atan2f(ay, az) * 180.0f / (float)M_PI; }
};

}  // namespace sensors
