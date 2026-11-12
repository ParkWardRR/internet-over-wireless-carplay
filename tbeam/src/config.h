// Pins, rails and constants for the LilyGO T-Beam S3 Supreme.
//
// Every value here is from the hardware record in tbeam-supreme-firmware-handoff.md,
// which took them from LilyGO's utilities.h and confirmed them on this board.
// Two of them contradict LilyGO's public docs and were learned the hard way:
// the RTC is on I2C bus 1, not bus 0, and the magnetometer is a QMC6309 at
// 0x7C, not a QMC6310 at 0x1C/0x3C.
#pragma once

// ---- SPI: the SD card and the IMU share one bus (not the radio's) ----
#define PIN_SPI_SCK   36
#define PIN_SPI_MISO  37
#define PIN_SPI_MOSI  35
#define PIN_SD_CS     47
#define PIN_IMU_CS    34
#define PIN_IMU_INT   33

// ---- I2C bus 0: OLED, BME280, magnetometer ----
#define PIN_I2C0_SDA  17
#define PIN_I2C0_SCL  18
#define ADDR_OLED     0x3C
#define ADDR_BME280   0x77
#define ADDR_MAG      0x7C   // QMC6309 (the V3.1 part), NOT a QMC6310 at 0x1C/0x3C

// ---- I2C bus 1: PMU, RTC ----
#define PIN_I2C1_SDA  42
#define PIN_I2C1_SCL  41
#define ADDR_PMU      0x34   // AXP2101
#define ADDR_RTC      0x51   // PCF8563 -- on bus 1, despite what LilyGO's docs say

// ---- GNSS: u-blox MAX-M10 on UART2 ----
#define PIN_GPS_RX     9     // ESP RX  <- GPS TX
#define PIN_GPS_TX     8
#define PIN_GPS_EN     7
#define PIN_GPS_PPS    6
#define GPS_BAUD       9600

// ---- Button: BOOT, active low. Also the ESP32-S3 boot strap. ----
#define PIN_BUTTON     0

// ---- OLED bezel insets. The case overhangs the glass: draw to the full
// 128x64 and the edges disappear under plastic. ----
#define OLED_W        128
#define OLED_H         64
#define OLED_INSET_L    5
#define OLED_INSET_R    5
#define OLED_INSET_T    4
#define OLED_INSET_B    4

// ---- Timing ----
#define MARK_TAP_MS      600    // shorter than this is a tap: a mark
#define PAGE_HOLD_MS    1500    // held this long: next page
#define MARK_FLASH_MS   2500    // how long the mark confirmation stays up
#define IMU_HZ           100
#define LOG_FLUSH_MS    2000
#define SD_DIR     "/carnet"    // NOT /tbeam: the mesh firmware owns that
