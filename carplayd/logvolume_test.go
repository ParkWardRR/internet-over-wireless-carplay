package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// evLines gives a daemon somewhere to write and reads back what it wrote.
func evLines(t *testing.T, d *daemon) func() []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	d.out = &evWriter{f: f}
	return func() []string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, l := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(l) != "" {
				out = append(out, l)
			}
		}
		return out
	}
}

// With debug on and both cars switched off -- which is most of this box's life
// -- wpa_supplicant emits the same three events once a second forever. Logging
// each of them was about 90% of the log volume, on an SD card.
func TestHuntChatterIsRateLimited(t *testing.T) {
	cfg := defaultConfig()
	cfg.Debug = true
	d := &daemon{cfg: cfg, label: "wlan0"}
	read := evLines(t, d)

	for i := 0; i < 100; i++ {
		for _, e := range huntChatter {
			d.logWPA(e)
		}
	}
	got := read()
	if len(got) != len(huntChatter) {
		t.Errorf("100 hunt ticks produced %d lines, want %d (one per event)", len(got), len(huntChatter))
		for _, l := range got {
			t.Log(l)
		}
	}

	// Once the window passes, the repeat says how much was hidden, so the log
	// never implies the box went quiet when it was in fact still hunting.
	for p := range d.wpaLast {
		d.wpaLast[p] = time.Now().Add(-2 * scanRepeat)
	}
	d.logWPA(huntChatter[0])
	last := read()[len(got)]
	if !strings.Contains(last, "x100_since_last") {
		t.Errorf("the repeat did not report the suppressed count: %s", last)
	}
}

// Anything that is not hunt chatter is something that actually happened. The
// rate limit must never touch it -- the whole reason every supplicant event is
// logged is that the ones previously dropped (ASSOC-REJECT, BEACON-LOSS, the
// auth timeouts) are what a failed departure turns on.
func TestRealEventsAreNeverSuppressed(t *testing.T) {
	cfg := defaultConfig()
	cfg.Debug = true
	d := &daemon{cfg: cfg, label: "wlan0"}
	read := evLines(t, d)

	real := []string{
		"CTRL-EVENT-ASSOC-REJECT status_code=17",
		"CTRL-EVENT-BEACON-LOSS",
		"CTRL-EVENT-DISCONNECTED reason=3",
		"CTRL-EVENT-AUTH-REJECT status_code=1",
		"CTRL-EVENT-SSID-TEMP-DISABLED",
	}
	for i := 0; i < 3; i++ {
		for _, e := range real {
			d.logWPA(e)
		}
	}
	if got := read(); len(got) != 3*len(real) {
		t.Errorf("real events were rate-limited: %d lines, want %d", len(got), 3*len(real))
	}
}

// The scan line is the whole BSS list, once a second. It repeats on a change of
// the BSSID set or on the timer, and not otherwise -- but a car appearing must
// show up at once, since that is the moment the box exists to notice.
func TestScanLogRepeatsOnChangeNotOnTimer(t *testing.T) {
	cfg := defaultConfig()
	cfg.Debug = true
	d := &daemon{cfg: cfg, label: "wlan0"}
	read := evLines(t, d)

	neighbours := []scanEntry{{BSSID: "aa:bb:cc:dd:ee:01", Freq: 2462, Signal: -70, SSID: "Neighbour"}}
	car := scanEntry{BSSID: "aa:bb:cc:dd:ee:02", Freq: 2462, Signal: -78, SSID: "BMW00001 CarPlay"}

	now := time.Now()
	for i := 0; i < 20; i++ {
		// The neighbour's RSSI wanders, as a real one does: that must not count
		// as a change, or the rate limit buys nothing.
		seen := []scanEntry{{BSSID: neighbours[0].BSSID, Freq: 2462, Signal: -70 - i%4, SSID: "Neighbour"}}
		d.logScan(seen, now.Add(time.Duration(i)*time.Second))
	}
	quiet := read()
	if len(quiet) != 1 {
		t.Errorf("20 identical scans produced %d scan lines, want 1", len(quiet))
	}
	// The car switches on: a new BSSID in the set, which must not wait.
	d.logScan(append(neighbours, car), now.Add(21*time.Second))
	after := read()
	if len(after) != len(quiet)+1 {
		t.Fatalf("a new BSS did not log immediately: %d lines, want %d", len(after), len(quiet)+1)
	}
	if !strings.Contains(after[len(after)-1], "ee:02") {
		t.Errorf("the new BSS is not in the line that was logged: %s", after[len(after)-1])
	}
}
