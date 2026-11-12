package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func drain(t *testing.T) (*bridge, func() string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "ev")
	if err != nil {
		t.Fatal(err)
	}
	b := &bridge{out: f}
	return b, func() string {
		x, _ := os.ReadFile(f.Name())
		return string(x)
	}
}

// A telemetry line must be recorded with the Pi's own clock beside the Beam's
// GPS time. That pairing is the whole point: it is what lets the two logs be
// aligned afterwards instead of stepping the Pi's clock during a capture.
func TestTelemetryRecordsBothClocks(t *testing.T) {
	b, read := drain(t)
	b.handle("$CNB,2026-11-12T02:43:06.000Z,1,9,34.012345,-118.498765,23.4,255.5,0.90,+0.35,-2.1,3,4034")
	got := read()
	for _, want := range []string{"beam ", "gps_time=2026-11-12T02:43:06.000Z", "fix=1", "sats=9",
		"mph=23.4", "heading=255.5", "accel=+0.35", "turn=-2.1", "marks=3", "pi_skew_ms="} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if b.lines != 1 {
		t.Errorf("line count %d, want 1", b.lines)
	}
}

// A mark is the one record that must land immediately and unmistakably.
func TestMarkForwarded(t *testing.T) {
	b, read := drain(t)
	b.handle("$CNBMARK,2026-11-12T02:44:01.000Z,4,31.2,34.012345,-118.498765,187.0")
	got := read()
	for _, want := range []string{"beam_mark ", "mark=4", "mph=31.2", "heading=187.0", "pi_skew_ms="} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if b.marks != 1 {
		t.Errorf("mark count %d, want 1", b.marks)
	}
}

// Before the GPS has said anything the Beam sends "boot+<n>s", which is not a
// timestamp. Treating it as one would invent a skew of decades and poison the
// alignment it exists to provide.
func TestBootRelativeTimeIsNotAClock(t *testing.T) {
	if _, ok := parseGPSTime("boot+12.345s"); ok {
		t.Error("boot-relative time was accepted as a clock")
	}
	b, read := drain(t)
	b.handle("$CNB,boot+12.345s,0,0,0.000000,0.000000,0.0,-1.0,99.99,+0.00,+0.0,0,4000")
	if got := read(); !strings.Contains(got, "pi_skew_ms=unknown") {
		t.Errorf("want pi_skew_ms=unknown, got %q", got)
	}
}

// GPS time arrives before a position fix, which is exactly when the clock
// alignment is most needed -- the Pi may have been powered up for hours.
func TestTimeAcceptedWithoutAFix(t *testing.T) {
	gt, ok := parseGPSTime("2026-11-12T02:43:06.000Z")
	if !ok {
		t.Fatal("rejected a valid GPS time")
	}
	b, read := drain(t)
	b.handle("$CNB,2026-11-12T02:43:06.000Z,0,0,0.000000,0.000000,0.0,-1.0,99.99,+0.00,+0.0,0,4000")
	got := read()
	if strings.Contains(got, "pi_skew_ms=unknown") {
		t.Errorf("skew not computed despite a valid time: %q", got)
	}
	if !strings.Contains(got, "fix=0") {
		t.Errorf("want fix=0 recorded: %q", got)
	}
	_ = gt
}

// Skew sign has to be unambiguous or a correction gets applied backwards.
func TestSkewSign(t *testing.T) {
	gps := time.Date(2026, 11, 12, 2, 43, 6, 0, time.UTC)
	if d := skew(gps.Add(250*time.Millisecond), gps); d.Milliseconds() != 250 {
		t.Errorf("pi ahead should be positive, got %dms", d.Milliseconds())
	}
	if d := skew(gps.Add(-250*time.Millisecond), gps); d.Milliseconds() != -250 {
		t.Errorf("pi behind should be negative, got %dms", d.Milliseconds())
	}
}

// Malformed or truncated lines must be ignored rather than logged as data.
func TestGarbageIgnored(t *testing.T) {
	b, read := drain(t)
	for _, l := range []string{"", "garbage", "$CNB", "$CNB,1,2", "[status] up 10s", "$CNBMARK,x"} {
		b.handle(l)
	}
	if b.lines != 0 || b.marks != 0 {
		t.Errorf("counted garbage: lines=%d marks=%d", b.lines, b.marks)
	}
	if got := read(); strings.Contains(got, "beam ") || strings.Contains(got, "beam_mark ") {
		t.Errorf("garbage was logged as data: %q", got)
	}
}
