// The OLED. Five pages, and one overlay that matters more than any of them.
//
// This is a tool used at arm's length in a moving car, so the design rule is
// that each page answers one question from a glance, and the thing you actually
// came to do -- mark the instant CarPlay dropped -- needs no page at all: tap
// the button anywhere and a full-screen confirmation tells you it landed.
//
// Everything is drawn inside the bezel insets. The case overhangs the glass, so
// pixels outside them are hidden under plastic.
#pragma once
#include <U8g2lib.h>

#include "config.h"
#include "drivelog.h"
#include "nmea.h"
#include "sensors.h"

namespace screens {

enum Page { PAGE_DRIVE = 0, PAGE_MARKS, PAGE_MOTION, PAGE_GPS, PAGE_SYS, PAGE_COUNT };

static const char *pageName(int p) {
    switch (p) {
        case PAGE_DRIVE: return "DRIVE";
        case PAGE_MARKS: return "MARKS";
        case PAGE_MOTION: return "MOTION";
        case PAGE_GPS: return "GPS";
        case PAGE_SYS: return "SYS";
    }
    return "?";
}

// What the renderer needs. Passed in rather than reached for, so the layout can
// be rendered on the host by the preview tool.
struct View {
    int page = PAGE_DRIVE;
    const nmea::Fix *fix = nullptr;
    const nmea::Stats *gpsStats = nullptr;
    sensors::Mag *mag = nullptr;
    sensors::Imu *imu = nullptr;
    drivelog::Log *log = nullptr;
    uint32_t uptimeS = 0;
    int battMv = 0, battPct = 0;
    bool charging = false;
    float chipTempC = 0;
    bool sdOk = false;
    float gpsAccelMps2 = 0, gpsTurnDps = 0;   // motion from GPS, used when the IMU is absent
    // Mark confirmation overlay.
    bool markFlash = false;
    uint32_t markNumber = 0;
    char markTime[16] = {0};
};

static const int X0 = OLED_INSET_L, Y0 = OLED_INSET_T;
static const int W = OLED_W - OLED_INSET_L - OLED_INSET_R;
static const int H = OLED_H - OLED_INSET_T - OLED_INSET_B;

static const char *cardinal(float deg) {
    static const char *c[] = {"N", "NE", "E", "SE", "S", "SW", "W", "NW"};
    int i = (int)((deg + 22.5f) / 45.0f) & 7;
    return c[i];
}

// A compass rose with a needle. The one genuinely graphical element: a heading
// in degrees takes a second to read, an arrow takes none.
static void compass(U8G2 &u, int cx, int cy, int r, float headingDeg, bool haveHeading) {
    u.drawCircle(cx, cy, r);
    u.setFont(u8g2_font_4x6_tf);
    u.drawStr(cx - 1, cy - r + 6, "N");
    if (!haveHeading) {
        u.setFont(u8g2_font_5x7_tf);
        u.drawStr(cx - 8, cy + 3, "--");
        return;
    }
    float a = headingDeg * (float)M_PI / 180.0f;
    int tipX = cx + (int)(sinf(a) * (r - 2));
    int tipY = cy - (int)(cosf(a) * (r - 2));
    int tailX = cx - (int)(sinf(a) * (r / 2));
    int tailY = cy + (int)(cosf(a) * (r / 2));
    u.drawLine(tailX, tailY, tipX, tipY);
    u.drawDisc(tipX, tipY, 2);
}

static void drawDrive(U8G2 &u, const View &v) {
    char s[32];
    bool haveMag = v.mag && v.mag->present;
    compass(u, X0 + 20, Y0 + 26, 19, haveMag ? v.mag->headingDeg : 0, haveMag);

    int rx = X0 + 46;
    // Speed, big. The number you watch to know when the departure started.
    u.setFont(u8g2_font_logisoso18_tn);
    double mph = v.fix ? v.fix->speedMph() : 0;
    snprintf(s, sizeof(s), "%d", (int)(mph + 0.5));
    u.drawStr(rx, Y0 + 20, s);
    u.setFont(u8g2_font_5x7_tf);
    u.drawStr(rx + 30, Y0 + 20, "mph");

    // Heading, numeric, next to the rose it belongs to.
    u.setFont(u8g2_font_6x10_tf);
    if (haveMag) {
        snprintf(s, sizeof(s), "%03d %s", (int)v.mag->headingDeg, cardinal(v.mag->headingDeg));
    } else {
        snprintf(s, sizeof(s), "no mag");
    }
    u.drawStr(rx, Y0 + 32, s);

    // Fix and marks: whether this drive is being recorded usefully at all.
    u.setFont(u8g2_font_5x7_tf);
    if (v.fix && v.fix->valid) {
        snprintf(s, sizeof(s), "FIX %dsat", v.fix->sats);
    } else if (v.gpsStats && v.gpsStats->sentences > 0) {
        snprintf(s, sizeof(s), "no fix %dsat", v.fix ? v.fix->sats : 0);
    } else {
        snprintf(s, sizeof(s), "no gps data");
    }
    u.drawStr(X0, Y0 + H - 1, s);
    snprintf(s, sizeof(s), "M:%lu %s", (unsigned long)(v.log ? v.log->marks : 0), v.sdOk ? "SD" : "!SD");
    u.drawStr(X0 + 66, Y0 + H - 1, s);
}

static void drawMarks(U8G2 &u, const View &v) {
    char s[40];
    u.setFont(u8g2_font_5x7_tf);
    if (!v.log || v.log->marks == 0) {
        u.drawStr(X0, Y0 + 20, "no marks yet");
        u.drawStr(X0, Y0 + 32, "tap BOOT when");
        u.drawStr(X0, Y0 + 42, "CarPlay drops");
        return;
    }
    snprintf(s, sizeof(s), "%lu marks", (unsigned long)v.log->marks);
    u.drawStr(X0, Y0 + 8, s);
    // Newest first: the one you just made is the one you want to see.
    int y = Y0 + 20;
    for (int i = 0; i < drivelog::MARK_HISTORY && y < Y0 + H; i++) {
        const drivelog::Mark &m = v.log->recent(i);
        if (!m.used) break;
        snprintf(s, sizeof(s), "%lu %s %dmph", (unsigned long)m.number, m.time, m.mph);
        u.drawStr(X0, y, s);
        y += 10;
    }
}

static void drawMotion(U8G2 &u, const View &v) {
    char s[40];
    u.setFont(u8g2_font_6x10_tf);
    if (!v.imu || !v.imu->present) {
        // The IMU does not answer on this board (vendor example fails too), so
        // this page shows the GPS-derived motion that stands in for it rather
        // than an empty screen.
        u.setFont(u8g2_font_5x7_tf);
        u.drawStr(X0, Y0 + 8, "IMU absent - GPS motion:");
        u.setFont(u8g2_font_6x10_tf);
        snprintf(s, sizeof(s), "%+.2f m/s2", v.gpsAccelMps2);
        u.drawStr(X0, Y0 + 21, s);
        snprintf(s, sizeof(s), "%+.0f deg/s turn", v.gpsTurnDps);
        u.drawStr(X0, Y0 + 33, s);
        u.setFont(u8g2_font_5x7_tf);
        snprintf(s, sizeof(s), "who_am_i 0x%02X", v.imu ? v.imu->whoami : 0);
        u.drawStr(X0, Y0 + 45, s);
        u.drawStr(X0, Y0 + H - 1, "see README: hardware");
        return;
    }
    snprintf(s, sizeof(s), "%.2fg peak %.2f", v.imu->magnitudeG(), v.imu->peakG);
    u.drawStr(X0, Y0 + 9, s);
    u.setFont(u8g2_font_5x7_tf);
    snprintf(s, sizeof(s), "ax%+.2f ay%+.2f az%+.2f", v.imu->ax, v.imu->ay, v.imu->az);
    u.drawStr(X0, Y0 + 21, s);
    snprintf(s, sizeof(s), "pitch%+4.0f roll%+4.0f", v.imu->pitchDeg(), v.imu->rollDeg());
    u.drawStr(X0, Y0 + 31, s);
    snprintf(s, sizeof(s), "gyro %+.0f %+.0f %+.0f", v.imu->gx, v.imu->gy, v.imu->gz);
    u.drawStr(X0, Y0 + 41, s);
    snprintf(s, sizeof(s), "rd %lu fail %lu", (unsigned long)v.imu->reads, (unsigned long)v.imu->failures);
    u.drawStr(X0, Y0 + 51, s);
}

static void drawGps(U8G2 &u, const View &v) {
    char s[40];
    u.setFont(u8g2_font_5x7_tf);
    if (!v.fix) return;
    snprintf(s, sizeof(s), "lat %+.6f", v.fix->lat);
    u.drawStr(X0, Y0 + 8, s);
    snprintf(s, sizeof(s), "lon %+.6f", v.fix->lon);
    u.drawStr(X0, Y0 + 18, s);
    snprintf(s, sizeof(s), "alt %.0fm hdop %.1f", v.fix->altM, v.fix->hdop);
    u.drawStr(X0, Y0 + 28, s);
    snprintf(s, sizeof(s), "%02d:%02d:%02.0fZ q%d %dsat", v.fix->hour, v.fix->min, v.fix->sec,
             v.fix->quality, v.fix->sats);
    u.drawStr(X0, Y0 + 38, s);
    if (v.gpsStats) {
        snprintf(s, sizeof(s), "nmea %lu bad %lu", (unsigned long)v.gpsStats->sentences,
                 (unsigned long)v.gpsStats->badChecksum);
        u.drawStr(X0, Y0 + 48, s);
    }
}

static void drawSys(U8G2 &u, const View &v) {
    char s[40];
    u.setFont(u8g2_font_5x7_tf);
    snprintf(s, sizeof(s), "batt %dmV %d%% %s", v.battMv, v.battPct, v.charging ? "chg" : "   ");
    u.drawStr(X0, Y0 + 8, s);
    snprintf(s, sizeof(s), "chip %.0fC up %lus", v.chipTempC, (unsigned long)v.uptimeS);
    u.drawStr(X0, Y0 + 18, s);
    if (v.log) {
        snprintf(s, sizeof(s), "sd %luKB drop %lu", (unsigned long)(v.log->written / 1024),
                 (unsigned long)v.log->dropped);
        u.drawStr(X0, Y0 + 28, s);
        snprintf(s, sizeof(s), "rows %lu marks %lu", (unsigned long)v.log->rows, (unsigned long)v.log->marks);
        u.drawStr(X0, Y0 + 38, s);
    }
    snprintf(s, sizeof(s), "imu %s mag %s", v.imu && v.imu->present ? "ok" : "--",
             v.mag && v.mag->present ? "ok" : "--");
    u.drawStr(X0, Y0 + 48, s);
    u.setFont(u8g2_font_4x6_tf);
    u.drawStr(X0, Y0 + H - 1, "tap=mark hold=page pwr=screen");
}

// The overlay. A mark is the whole reason this board is in the car, so the
// confirmation takes the entire screen -- at a glance, while driving, with no
// reading required.
static void drawMarkFlash(U8G2 &u, const View &v) {
    char s[24];
    u.drawFrame(X0, Y0, W, H);
    u.setFont(u8g2_font_6x10_tf);
    u.drawStr(X0 + 4, Y0 + 12, "MARKED");
    u.setFont(u8g2_font_logisoso18_tn);
    snprintf(s, sizeof(s), "%lu", (unsigned long)v.markNumber);
    u.drawStr(X0 + 4, Y0 + 36, s);
    u.setFont(u8g2_font_6x10_tf);
    u.drawStr(X0 + 40, Y0 + 36, v.markTime);
    u.setFont(u8g2_font_5x7_tf);
    u.drawStr(X0 + 4, Y0 + H - 2, v.sdOk ? "written to card" : "NOT WRITTEN: no card");
}

static void render(U8G2 &u, const View &v) {
    u.clearBuffer();
    if (v.markFlash) {
        drawMarkFlash(u, v);
        u.sendBuffer();
        return;
    }
    // Page name, small, top right, so a glance says which page without
    // spending a line on a title bar.
    u.setFont(u8g2_font_4x6_tf);
    const char *n = pageName(v.page);
    u.drawStr(OLED_W - OLED_INSET_R - (int)strlen(n) * 4, Y0 + 5, n);
    switch (v.page) {
        case PAGE_DRIVE: drawDrive(u, v); break;
        case PAGE_MARKS: drawMarks(u, v); break;
        case PAGE_MOTION: drawMotion(u, v); break;
        case PAGE_GPS: drawGps(u, v); break;
        default: drawSys(u, v); break;
    }
    u.sendBuffer();
}

}  // namespace screens
