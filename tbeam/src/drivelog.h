// The drive log on the microSD card.
//
// Two files per boot, under /carnet (the mesh firmware owns /tbeam):
//
//	drive-<n>.csv   one row a second: time, position, speed, heading, motion
//	marks-<n>.csv   one row per button tap: the instant something happened
//
// Writes never block the loop. Rows go into a ring, the loop flushes it every
// couple of seconds, and a missing or failing card just fills the ring and
// increments a counter -- the handoff is explicit that a logger which blocks on
// a flaky card will stall everything around it.
#pragma once
#include <Arduino.h>
#include <SD.h>
#include <SPI.h>

#include "config.h"

namespace drivelog {

static constexpr size_t RING = 12288;
static constexpr int MARK_HISTORY = 5;

struct Mark {
    bool used = false;
    uint32_t number = 0;
    char time[16] = {0};
    int mph = 0;
};

class Log {
  public:
    uint32_t rows = 0, marks = 0, dropped = 0, written = 0;
    bool mounted = false;

    bool begin(SPIClass &spi) {
        if (!SD.begin(PIN_SD_CS, spi)) return false;
        if (!SD.exists(SD_DIR)) SD.mkdir(SD_DIR);
        // A new pair of files per boot, so one drive never overwrites another.
        for (int i = 1; i < 1000; i++) {
            snprintf(drivePath_, sizeof(drivePath_), "%s/drive-%03d.csv", SD_DIR, i);
            if (!SD.exists(drivePath_)) {
                snprintf(markPath_, sizeof(markPath_), "%s/marks-%03d.csv", SD_DIR, i);
                break;
            }
        }
        mounted = true;
        appendLine(drivePath_,
                   "iso_time,fix,sats,lat,lon,alt_m,hdop,speed_mph,course_deg,"
                   "heading_deg,mag_x,mag_y,mag_z,ax,ay,az,gx,gy,gz,g_mag,batt_mv,uptime_s,"
                   "gps_accel_mps2,gps_turn_dps");
        appendLine(markPath_, "iso_time,mark,speed_mph,lat,lon,heading_deg,g_mag,note");
        return true;
    }

    // push queues a row. It is never allowed to fail slowly.
    void push(const char *line) {
        size_t n = strlen(line);
        if (n + 1 > RING - used_) {
            dropped++;
            return;
        }
        memcpy(ring_ + used_, line, n);
        ring_[used_ + n] = '\n';
        used_ += n + 1;
        rows++;
    }

    void mark(const char *isoTime, int mph, double lat, double lon, float heading, float g) {
        marks++;
        char line[160];
        snprintf(line, sizeof(line), "%s,%lu,%d,%.6f,%.6f,%.1f,%.3f,", isoTime,
                 (unsigned long)marks, mph, lat, lon, heading, g);
        push(line);  // queued into the drive ring too, so the row order is kept
        // Marks also go straight to their own file: this is the one record that
        // must survive a power cut in the next two seconds.
        if (mounted) {
            char m[160];
            snprintf(m, sizeof(m), "%s,%lu,%d,%.6f,%.6f,%.1f,%.3f,", isoTime,
                     (unsigned long)marks, mph, lat, lon, heading, g);
            appendLine(markPath_, m);
        }
        // Keep a short history for the display.
        for (int i = MARK_HISTORY - 1; i > 0; i--) hist_[i] = hist_[i - 1];
        hist_[0].used = true;
        hist_[0].number = marks;
        hist_[0].mph = mph;
        snprintf(hist_[0].time, sizeof(hist_[0].time), "%s", isoTime + 11);  // HH:MM:SS
    }

    const Mark &recent(int i) const { return hist_[i < MARK_HISTORY ? i : MARK_HISTORY - 1]; }

    // flush writes at most one chunk, so a slow card costs one loop a little
    // time rather than stopping the sensors.
    void flush() {
        if (!mounted || used_ == 0) return;
        File f = SD.open(drivePath_, FILE_APPEND);
        if (!f) {
            mounted = false;  // retried by remount()
            return;
        }
        size_t n = f.write((const uint8_t *)ring_, used_);
        f.close();
        written += n;
        if (n < used_) {
            dropped++;
            memmove(ring_, ring_ + n, used_ - n);
            used_ -= n;
        } else {
            used_ = 0;
        }
    }

    void remount(SPIClass &spi) {
        if (mounted) return;
        SD.end();
        mounted = SD.begin(PIN_SD_CS, spi);
    }

  private:
    char ring_[RING];
    size_t used_ = 0;
    char drivePath_[32] = {0};
    char markPath_[32] = {0};
    Mark hist_[MARK_HISTORY];

    void appendLine(const char *path, const char *line) {
        File f = SD.open(path, FILE_APPEND);
        if (!f) return;
        f.println(line);
        f.close();
    }
};

}  // namespace drivelog
