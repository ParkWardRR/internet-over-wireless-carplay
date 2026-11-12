// A small NMEA reader: RMC and GGA only, which is everything a drive log needs
// (time, position, speed, course, fix quality, satellite count, altitude).
//
// Checksum-verified, no dynamic allocation, no Arduino headers, so it builds
// and is tested on the host.
#pragma once
#include <stdint.h>
#include <string.h>

namespace nmea {

struct Fix {
    bool valid = false;      // RMC status A, i.e. a usable position
    int sats = 0;            // from GGA
    int quality = 0;         // GGA fix quality: 0 none, 1 GPS, 2 DGPS
    double lat = 0, lon = 0; // decimal degrees, south and west negative
    double altM = 0;
    double hdop = 0;
    double speedKn = 0;      // RMC speed over ground, knots
    double courseDeg = 0;    // RMC track made good, degrees true
    // UTC from RMC. year is full (2026), 0 when unknown.
    int year = 0, mon = 0, day = 0, hour = 0, min = 0;
    double sec = 0;

    double speedKmh() const { return speedKn * 1.852; }
    double speedMph() const { return speedKn * 1.15078; }
};

// Counters, so a drive log can show whether the GPS is talking at all even
// with no fix -- the handoff notes a fix has never been seen on this board
// indoors, and "no sentences" and "no fix" are different problems.
struct Stats {
    uint32_t sentences = 0, badChecksum = 0, rmc = 0, gga = 0;
};

class Parser {
  public:
    Fix fix;
    Stats stats;

    // Feed one character. Returns true when a sentence completed and parsed.
    bool feed(char c) {
        if (c == '$') {
            len_ = 0;
            inLine_ = true;
            return false;
        }
        if (!inLine_) return false;
        if (c == '\r' || c == '\n') {
            inLine_ = false;
            if (len_ == 0) return false;
            buf_[len_] = 0;
            return line();
        }
        if (len_ < sizeof(buf_) - 1) buf_[len_++] = c;
        return false;
    }

  private:
    char buf_[120];
    size_t len_ = 0;
    bool inLine_ = false;

    // "<body>*HH": the checksum is the XOR of every byte of the body.
    bool line() {
        char *star = strchr(buf_, '*');
        if (!star || star[1] == 0 || star[2] == 0) return false;
        uint8_t want = (uint8_t)((hex(star[1]) << 4) | hex(star[2]));
        *star = 0;
        uint8_t got = 0;
        for (char *p = buf_; *p; p++) got ^= (uint8_t)*p;
        stats.sentences++;
        if (got != want) {
            stats.badChecksum++;
            return false;
        }
        // Talker id is two characters (GP, GN, GL...); match on the last three.
        const char *type = buf_ + 2;
        if (!strncmp(type, "RMC", 3)) return rmc();
        if (!strncmp(type, "GGA", 3)) return gga();
        return false;
    }

    static uint8_t hex(char c) {
        if (c >= '0' && c <= '9') return (uint8_t)(c - '0');
        if (c >= 'A' && c <= 'F') return (uint8_t)(c - 'A' + 10);
        if (c >= 'a' && c <= 'f') return (uint8_t)(c - 'a' + 10);
        return 0;
    }

    // field(n) returns a pointer to the nth comma-separated field, or "".
    const char *field(int n) {
        const char *p = buf_;
        for (int i = 0; i < n; i++) {
            p = strchr(p, ',');
            if (!p) return "";
            p++;
        }
        return p;
    }

    static double num(const char *s) {
        double v = 0, f = 0, scale = 1;
        int sign = 1;
        if (*s == '-') { sign = -1; s++; }
        while (*s >= '0' && *s <= '9') v = v * 10 + (*s++ - '0');
        if (*s == '.') {
            s++;
            while (*s >= '0' && *s <= '9') { f = f * 10 + (*s++ - '0'); scale *= 10; }
        }
        return sign * (v + f / scale);
    }
    static int integer(const char *s) { return (int)num(s); }

    // NMEA gives ddmm.mmmm, not degrees: the minutes are the last two integer
    // digits plus the fraction. Getting this wrong puts you 60x off.
    //
    // Not named `degrees`: that is an Arduino macro, and a member function
    // sharing a macro name fails to compile in a way that points at the line
    // after it. Avoid `abs`, `min`, `max`, `round` and `radians` here too.
    static double ddmmToDeg(const char *v, const char *hemi) {
        double raw = num(v);
        if (raw == 0) return 0;
        int deg = (int)(raw / 100);
        double minutes = raw - deg * 100;
        double d = deg + minutes / 60.0;
        if (*hemi == 'S' || *hemi == 'W') d = -d;
        return d;
    }

    bool rmc() {
        // 1 time 2 status 3 lat 4 N/S 5 lon 6 E/W 7 speed 8 course 9 date
        const char *t = field(1);
        if (strlen(t) >= 6) {
            fix.hour = (t[0] - '0') * 10 + (t[1] - '0');
            fix.min = (t[2] - '0') * 10 + (t[3] - '0');
            fix.sec = num(t + 4);
        }
        fix.valid = field(2)[0] == 'A';
        fix.lat = ddmmToDeg(field(3), field(4));
        fix.lon = ddmmToDeg(field(5), field(6));
        fix.speedKn = num(field(7));
        fix.courseDeg = num(field(8));
        const char *d = field(9);
        if (strlen(d) >= 6) {
            fix.day = (d[0] - '0') * 10 + (d[1] - '0');
            fix.mon = (d[2] - '0') * 10 + (d[3] - '0');
            fix.year = 2000 + (d[4] - '0') * 10 + (d[5] - '0');
        }
        stats.rmc++;
        return true;
    }

    bool gga() {
        // 6 quality 7 sats 8 hdop 9 altitude
        fix.quality = integer(field(6));
        fix.sats = integer(field(7));
        fix.hdop = num(field(8));
        fix.altM = num(field(9));
        stats.gga++;
        return true;
    }
};

}  // namespace nmea
