package main

import (
	"testing"
	"time"
)

// The 2026-11-11 F32 departure, as logged by the box once a second. The signal
// stays flat while the link dies, so neither signal rule can fire; tx_failed is
// the only thing that moves. Taken from docs/test-log.md.
var f32Departure = []struct {
	sig      int
	txFailed int
}{
	{-82, 2848}, {-82, 2848}, {-82, 2848}, {-81, 2848}, {-82, 2848}, {-83, 2848},
	{-78, 2848}, {-77, 2848}, {-77, 2848}, {-81, 2848}, {-80, 2848}, {-79, 2848},
	{-81, 2848}, {-81, 2848},
	{-82, 2852}, {-82, 2854}, {-82, 2907}, {-82, 2911}, {-82, 2917},
	{-82, 2923}, {-82, 2928}, {-82, 2930}, // link died right after this
}

// Replaying the real departure: the failing rule must fire, and it must fire
// before the link died, so a goodbye can still go out. Neither signal rule
// gets a chance -- that is the whole point of the new rule.
func TestFailingLeaveCatchesTheFlatSignalDeparture(t *testing.T) {
	car := Car{ID: "A", Join: -80, Leave: -85, Rejoin: -77, Drop: 6, FailRate: 5}
	cfg := defaultConfig()
	cfg.LeaveFor = 2 * time.Second
	d := &daemon{cfg: cfg, serving: &car, m: newMetrics()}

	start := time.Now()
	firedAt := -1
	for i, s := range f32Departure {
		now := start.Add(time.Duration(i) * time.Second)
		d.readFail(map[string]string{"tx_failed": itoa(s.txFailed)}, now)

		// the two signal rules, exactly as poll() runs them
		d.samples = append(trimSamples(d.samples, now), sample{now, s.sig})
		var leave, trend bool
		d.below, leave = leaveCheck(d.below, now, s.sig, car.Leave, cfg.LeaveFor)
		if base, ok := baselineOf(d.samples, now); ok {
			d.belowTrend, trend = leaveCheck(d.belowTrend, now, s.sig, base-car.Drop, cfg.LeaveFor)
		}
		if leave || trend {
			t.Fatalf("sample %d (sig %d): a signal rule fired on the real trace; "+
				"it did not on the car, so the test data or the rules changed", i, s.sig)
		}

		var failing bool
		d.belowFail, failing = sustain(d.belowFail, now, d.failPerSec >= car.FailRate, cfg.LeaveFor)
		if failing && firedAt < 0 {
			firedAt = i
		}
	}
	if firedAt < 0 {
		t.Fatal("failing rule never fired: the box would again cling on until the link died")
	}
	lead := len(f32Departure) - 1 - firedAt
	if lead < 2 {
		t.Errorf("fired at sample %d, only %ds before the link died: too late to say goodbye", firedAt, lead)
	}
	t.Logf("failing rule fired at sample %d, %ds before the link died", firedAt, lead)
}

// A healthy link must never trip the rule. On the same drive, tx_failed rose by
// 0-2 per second for minutes while the box was serving normally.
func TestFailingLeaveQuietOnHealthyLink(t *testing.T) {
	car := Car{ID: "A", Leave: -85, FailRate: 5}
	cfg := defaultConfig()
	cfg.LeaveFor = 2 * time.Second
	d := &daemon{cfg: cfg, serving: &car, m: newMetrics()}
	start := time.Now()
	fails := 2840
	for i := 0; i < 120; i++ {
		if i%3 == 0 { // a stray failure every few seconds
			fails++
		}
		now := start.Add(time.Duration(i) * time.Second)
		d.readFail(map[string]string{"tx_failed": itoa(fails)}, now)
		var failing bool
		d.belowFail, failing = sustain(d.belowFail, now, d.failPerSec >= car.FailRate, cfg.LeaveFor)
		if failing {
			t.Fatalf("fired on a healthy link at sample %d (rate %d/s)", i, d.failPerSec)
		}
	}
}

// Counters restart when a station re-associates. A backwards jump is not a
// failing link, and must not be read as a huge failure rate.
func TestFailRateIgnoresCounterRestart(t *testing.T) {
	if _, ok := failRate(2930, 0, time.Second); ok {
		t.Error("a counter reset to 0 was treated as a valid rate")
	}
	if _, ok := failRate(-1, 50, time.Second); ok {
		t.Error("the first reading was treated as a valid rate")
	}
	if _, ok := failRate(10, 20, 0); ok {
		t.Error("zero elapsed time was treated as a valid rate")
	}
	if r, ok := failRate(2848, 2930, 8*time.Second); !ok || r != 10 {
		t.Errorf("want 10/s over 8s, got %d (ok=%t)", r, ok)
	}
}

// readFail must not clear a failing link's evidence when one `iw` call fails
// and the counter is simply absent.
func TestReadFailKeepsStateWhenCounterMissing(t *testing.T) {
	d := &daemon{}
	now := time.Now()
	d.readFail(map[string]string{"tx_failed": "100"}, now)
	d.readFail(map[string]string{"tx_failed": "180"}, now.Add(time.Second))
	if d.failPerSec != 80 {
		t.Fatalf("want 80/s, got %d", d.failPerSec)
	}
	d.readFail(nil, now.Add(2*time.Second)) // iw failed: no counters at all
	if d.failPerSec != 80 {
		t.Errorf("a missing counter cleared the rate: got %d, want it held at 80", d.failPerSec)
	}
}

// The relaxed rejoin may only ever be stricter than the configured one. An
// easier threshold let the box rejoin a car it had just left while still
// driving away, then cling on at 1 Mb/s (2026-11-11, F22).
func TestRelaxedRejoinNeverEasierThanConfigured(t *testing.T) {
	cars := []Car{{ID: "A", Join: -80, Leave: -85, Rejoin: -77}}
	// base -76 => max(base-3, …) = -79, which is easier than Rejoin -77.
	got := relaxRejoin(cars, map[string]int{"A": -79})
	if got[0].Rejoin != -77 {
		t.Errorf("relaxed rejoin went easier than configured: got %d, want -77", got[0].Rejoin)
	}
	// A stricter relaxed threshold must still be honoured.
	got = relaxRejoin(cars, map[string]int{"A": -73})
	if got[0].Rejoin != -73 {
		t.Errorf("a stricter relaxed rejoin was dropped: got %d, want -73", got[0].Rejoin)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// A car that was just left must not be rejoined on signal alone. Measured on
// the F22 departure: twelve seconds after a leave the signal had recovered to
// -76 dBm while the car was still driving away, and a level-based rule let it
// straight back in.
func TestRejoinHoldOff(t *testing.T) {
	cars := []Car{{ID: "A", Join: -80, Rejoin: -77, Leave: -85}, {ID: "B", Join: -80, Rejoin: -77, Leave: -85}}
	now := time.Now()
	left := map[string]time.Time{"A": now.Add(-5 * time.Second)}

	got := holdOff(cars, left, now, 20*time.Second)
	if len(got) != 1 || got[0].ID != "B" {
		t.Errorf("A left 5s ago and was not held off: %+v", ids(got))
	}
	// Once the hold-off has passed it is a candidate again.
	if got := holdOff(cars, left, now.Add(30*time.Second), 20*time.Second); len(got) != 2 {
		t.Errorf("A still held off after the window: %+v", ids(got))
	}
	// Disabled, and the no-history case, must not filter anything.
	if got := holdOff(cars, left, now, 0); len(got) != 2 {
		t.Errorf("hold-off of 0 filtered cars: %+v", ids(got))
	}
	if got := holdOff(cars, nil, now, 20*time.Second); len(got) != 2 {
		t.Errorf("empty history filtered cars: %+v", ids(got))
	}
}

func ids(cs []Car) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}
