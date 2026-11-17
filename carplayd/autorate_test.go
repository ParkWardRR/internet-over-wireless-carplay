package main

import (
	"strings"
	"testing"
	"time"
)

func base() rateInputs {
	return rateInputs{
		CurKbit: 2000, MinKbit: 512, MaxKbit: 6000,
		TargetRTT: 150 * time.Millisecond,
		Loaded:    true, Samples: 10,
	}
}

// The trap the whole design exists to avoid. At idle the phone's power save put
// round trips at 2051 ms with no load at all; a controller that believed that
// would ratchet to the floor while nothing was happening.
func TestIdleLatencyNeverMovesTheRate(t *testing.T) {
	in := base()
	in.Loaded = false
	in.LoadedP95 = 2051 * time.Millisecond
	v := decideRate(in)
	if v.Kbit != 2000 {
		t.Fatalf("idle 2051 ms moved the rate to %d: %s", v.Kbit, v.Reason)
	}
	if !strings.Contains(v.Reason, "no demand") {
		t.Errorf("the reason must say why it held: %q", v.Reason)
	}
}

func TestTooFewSamplesHolds(t *testing.T) {
	in := base()
	in.Samples = rateMinSamples - 1
	in.LoadedP95 = 900 * time.Millisecond
	if v := decideRate(in); v.Kbit != 2000 {
		t.Fatalf("acted on %d samples: %s", in.Samples, v.Reason)
	}
}

func TestOverTargetBacksOff(t *testing.T) {
	in := base()
	in.LoadedP95 = 400 * time.Millisecond
	v := decideRate(in)
	if v.Kbit != 1400 { // 2000 * 7/10
		t.Fatalf("got %d, want 1400: %s", v.Kbit, v.Reason)
	}
	if !strings.Contains(v.Reason, "down") {
		t.Errorf("reason %q", v.Reason)
	}
}

func TestWellUnderTargetProbesUpButOnlyUnderDemand(t *testing.T) {
	in := base()
	in.LoadedP95 = 80 * time.Millisecond // under 2/3 of 150 ms
	v := decideRate(in)
	if v.Kbit != 2500 { // 2000 * 5/4
		t.Fatalf("got %d, want 2500: %s", v.Kbit, v.Reason)
	}
	// Same latency, no demand: must not climb. Climbing without demand would
	// test a rate nobody asked for and risk a disconnect for nothing.
	in.Loaded = false
	if v := decideRate(in); v.Kbit != 2000 {
		t.Fatalf("climbed with no demand to %d: %s", v.Kbit, v.Reason)
	}
}

func TestDeadbandHolds(t *testing.T) {
	// Between 2/3 of target and target: settle, do not oscillate.
	for _, rtt := range []time.Duration{101 * time.Millisecond, 130 * time.Millisecond, 150 * time.Millisecond} {
		in := base()
		in.LoadedP95 = rtt
		v := decideRate(in)
		if v.Kbit != 2000 {
			t.Errorf("p95 %v moved the rate to %d: %s", rtt, v.Kbit, v.Reason)
		}
		if !strings.Contains(v.Reason, "deadband") {
			t.Errorf("p95 %v: reason %q", rtt, v.Reason)
		}
	}
}

func TestDownIsFasterThanUp(t *testing.T) {
	// Asymmetry is deliberate: too high can drop CarPlay and cost a head-unit
	// reset; too low only costs download speed.
	up := base()
	up.LoadedP95 = 10 * time.Millisecond
	down := base()
	down.LoadedP95 = 500 * time.Millisecond
	gainUp := decideRate(up).Kbit - 2000
	lossDown := 2000 - decideRate(down).Kbit
	if lossDown <= gainUp {
		t.Fatalf("decrease (%d) must exceed increase (%d)", lossDown, gainUp)
	}
}

func TestFloorAndCeilingAreRespected(t *testing.T) {
	in := base()
	in.CurKbit, in.LoadedP95 = 512, 900*time.Millisecond
	if v := decideRate(in); v.Kbit != 512 {
		t.Errorf("went below the floor to %d", v.Kbit)
	} else if !strings.Contains(v.Reason, "floor") {
		t.Errorf("reason %q", v.Reason)
	}
	in = base()
	in.CurKbit, in.LoadedP95 = 6000, 10*time.Millisecond
	if v := decideRate(in); v.Kbit != 6000 {
		t.Errorf("went above the ceiling to %d", v.Kbit)
	}
}

// A ceiling learned after an unexplained loss outranks the max and is applied
// even on an interval the controller would otherwise sit out.
func TestLearnedCeilingOutranksEverything(t *testing.T) {
	in := base()
	in.CurKbit, in.CeilKbit = 5000, 2800
	in.Loaded = false // an interval it would normally hold on
	v := decideRate(in)
	if v.Kbit != 2800 {
		t.Fatalf("ignored the learned ceiling: got %d (%s)", v.Kbit, v.Reason)
	}
	if !strings.Contains(v.Reason, "clamped") {
		t.Errorf("reason %q", v.Reason)
	}
	// and it caps an upward probe
	in = base()
	in.CurKbit, in.CeilKbit, in.LoadedP95 = 2600, 2800, 10*time.Millisecond
	if v := decideRate(in); v.Kbit != 2800 {
		t.Fatalf("probed past the learned ceiling to %d", v.Kbit)
	}
}

// Convergence: from either extreme, under a latency response that mimics a
// shared medium, the controller must settle rather than oscillate or run away.
func TestConvergesFromBothEnds(t *testing.T) {
	// Model: latency grows with rate; 3000 kbit is where it crosses target.
	rttAt := func(kbit int) time.Duration {
		return time.Duration(kbit) * 150 * time.Millisecond / 3000
	}
	for _, start := range []int{512, 6000} {
		in := base()
		in.CurKbit = start
		seen := map[int]int{}
		for i := 0; i < 60; i++ {
			in.LoadedP95 = rttAt(in.CurKbit)
			in.CurKbit = decideRate(in).Kbit
			if i > 40 {
				seen[in.CurKbit]++
			}
		}
		if in.CurKbit < 1500 || in.CurKbit > 3200 {
			t.Errorf("start %d settled at %d, outside a sane band around 3000", start, in.CurKbit)
		}
		if len(seen) > 3 {
			t.Errorf("start %d oscillated across %d values in the last 20 steps", start, len(seen))
		}
	}
}

func TestP95(t *testing.T) {
	if got := p95(nil); got != 0 {
		t.Errorf("no samples -> %v", got)
	}
	one := []time.Duration{42}
	if got := p95(one); got != 42 {
		t.Errorf("single sample -> %v", got)
	}
	// 20 samples with ONE outlier: p95 is nearest-rank, so the single 900 ms is
	// correctly excluded. That is the noise rejection we want -- one power-save
	// blip must not trigger a backoff.
	s := make([]time.Duration, 0, 20)
	for i := 0; i < 19; i++ {
		s = append(s, 10*time.Millisecond)
	}
	s = append(s, 900*time.Millisecond)
	if got := p95(s); got != 10*time.Millisecond {
		t.Errorf("one outlier in 20 should not move p95, got %v", got)
	}
	// Sustained lateness is different, and must surface: 3 bad in 20.
	s = s[:17]
	s = append(s, 900*time.Millisecond, 900*time.Millisecond, 900*time.Millisecond)
	if got := p95(s); got != 900*time.Millisecond {
		t.Errorf("3 late packets in 20 must surface, got %v", got)
	}
	// And the mean would have hidden it: 17x10 + 3x900 averages 143 ms, inside
	// the default 150 ms target, while p95 is 900 ms. This is why not the mean.
}

func TestParseRateKbit(t *testing.T) {
	ok := map[string]int{
		"2mbit": 2000, "512kbit": 512, "1.5mbit": 1500, "1gbit": 1000000,
		"off": 0, "": 0,
	}
	for in, want := range ok {
		got, err := parseRateKbit(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q -> %d kbit, want %d", in, got, want)
		}
	}
	for _, bad := range []string{"2mbps", "fast", "0.0001kbit"} {
		if _, err := parseRateKbit(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestKbitToTCRoundTrips(t *testing.T) {
	for _, k := range []int{512, 1500, 2000, 6000} {
		got, err := parseRateKbit(kbitToTC(k))
		if err != nil || got != k {
			t.Errorf("%d -> %q -> %d, %v", k, kbitToTC(k), got, err)
		}
	}
}

// --- config ----------------------------------------------------------------

// carConf injects keys into the sample config's existing [car A] section.
// Appending a new "[car A]" instead would create a *second* car and leave the
// assertions looking at the original -- which is exactly how these tests first
// failed.
func carConf(keys string) string {
	return strings.Replace(globals("sta = wlan0\n"), "[car A]\n", "[car A]\n"+keys, 1)
}

func TestAutoRateConfigDefaultsAndValidation(t *testing.T) {
	c := mustConfig(t, carConf("rate = auto\n")).Cars[0]
	if c.Rate != "auto" {
		t.Fatalf("rate = %q", c.Rate)
	}
	if c.RateMin != "512kbit" || c.RateMax != "3mbit" || c.RateRTT != 150*time.Millisecond {
		t.Fatalf("defaults not applied: min=%q max=%q rtt=%v", c.RateMin, c.RateMax, c.RateRTT)
	}
}

func TestAutoRateConfigHonoursOverrides(t *testing.T) {
	c := mustConfig(t, carConf("rate = auto\nrate_min = 1mbit\nrate_max = 3mbit\nrate_rtt = 90ms\n")).Cars[0]
	if c.RateMin != "1mbit" || c.RateMax != "3mbit" || c.RateRTT != 90*time.Millisecond {
		t.Fatalf("overrides lost: %+v", c)
	}
}

func TestAutoRateConfigRejectsNonsense(t *testing.T) {
	bad := map[string]string{
		"min above max":       "rate = auto\nrate_min = 4mbit\nrate_max = 1mbit\n",
		"min equal to max":    "rate = auto\nrate_min = 2mbit\nrate_max = 2mbit\n",
		"target under idle":   "rate = auto\nrate_rtt = 5ms\n",
		"bounds without auto": "rate = 2mbit\nrate_max = 6mbit\n",
		"byte units":          "rate = auto\nrate_max = 2mbps\n",
	}
	for name, frag := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := parseConfig(strings.NewReader(carConf(frag))); err == nil {
				t.Fatalf("accepted %q", frag)
			}
		})
	}
}

func TestFixedRateGetsNoAutoBounds(t *testing.T) {
	c := mustConfig(t, carConf("rate = 2mbit\n")).Cars[0]
	if c.Rate != "2mbit" {
		t.Fatalf("rate = %q", c.Rate)
	}
	if c.RateMin != "" || c.RateMax != "" || c.RateRTT != 0 {
		t.Errorf("a fixed car picked up auto bounds: %+v", c)
	}
}

// The ceiling must stay under the rate that actually broke the head unit.
// Measured 2026-11-16: the relay collapsed at about 3.8 Mbit/s of our traffic.
// The in-car airtime run that evening found ~20 Mbit/s of headroom, and that
// must NOT raise this: airtime is not the binding constraint, the relay is.
func TestDefaultRateMaxStaysUnderTheRelayBreakingPoint(t *testing.T) {
	c := mustConfig(t, carConf("rate = auto\n")).Cars[0]
	kbit, err := parseRateKbit(c.RateMax)
	if err != nil {
		t.Fatal(err)
	}
	const brokeAtKbit = 3800
	if kbit >= brokeAtKbit {
		t.Fatalf("default rate_max %s (%d kbit) is at or above the %d kbit that dropped CarPlay",
			c.RateMax, kbit, brokeAtKbit)
	}
	// And it must still be above the one value measured safe, or auto mode
	// could never reach a rate we know works.
	if kbit <= autoRateStartKbit {
		t.Fatalf("rate_max %d kbit is not above the measured-safe start %d kbit", kbit, autoRateStartKbit)
	}
}
