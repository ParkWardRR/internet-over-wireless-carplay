package main

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// feed plays n healthy polls with RSSI drawn around mean/sd.
func feed(p *profile, n int, mean, sd float64, rng *rand.Rand) {
	for i := 0; i < n; i++ {
		p.observe(int(mean+rng.NormFloat64()*sd), 0, true)
	}
}

// Until it has learned enough, the configured thresholds must be used
// untouched. A box that invented numbers from three samples would be worse
// than one that never learned at all.
func TestLearnFallsBackToConfigUntilReady(t *testing.T) {
	c := Car{ID: "A", Join: -80, Leave: -85, Rejoin: -77, FailRate: 5}
	p := &profile{ID: "A"}
	rng := rand.New(rand.NewSource(1))
	feed(p, minLearn-1, -79, 2, rng)
	if got, ok := p.derive(c); ok || got != c {
		t.Fatalf("derived before ready: ok=%t car=%+v", ok, got)
	}
	feed(p, 2, -79, 2, rng)
	if _, ok := p.derive(c); !ok {
		t.Fatal("still not ready after minLearn healthy samples")
	}
}

// The F22's measured healthy band (about -79 ± 2 dB behind the cinderblock
// wall). The learned thresholds must bracket it sensibly: join loose enough to
// work where it demonstrably works, rejoin near the normal level so it does not
// bounce back while driving away, leave well below as an outer guard only.
func TestLearnedThresholdsBracketTheMeasuredBand(t *testing.T) {
	c := Car{ID: "B", Join: -80, Leave: -85, Rejoin: -77, FailRate: 5}
	p := &profile{ID: "B"}
	feed(p, 600, -79, 2, rand.New(rand.NewSource(2)))
	got, ok := p.derive(c)
	if !ok {
		t.Fatal("not ready")
	}
	if !(got.Rejoin >= got.Join && got.Join > got.Leave) {
		t.Errorf("hysteresis invariant broken: join=%d leave=%d rejoin=%d", got.Join, got.Leave, got.Rejoin)
	}
	// rejoin should sit close to the healthy mean, join below it, leave well below.
	if got.Rejoin < -82 || got.Rejoin > -77 {
		t.Errorf("rejoin %d is not near the healthy mean of -79", got.Rejoin)
	}
	if got.Join > got.Rejoin || got.Join < -85 {
		t.Errorf("join %d unreasonable against a -79 mean", got.Join)
	}
	if got.Leave > -83 {
		t.Errorf("leave %d is not an outer guard below a -79 mean", got.Leave)
	}
	t.Logf("learned from -79±2: join=%d rejoin=%d leave=%d fail_rate=%d",
		got.Join, got.Rejoin, got.Leave, got.FailRate)
}

// The point of the whole exercise: swap the radio (or park somewhere new) and
// every number moves. The profile must follow instead of averaging the new
// world against the old one forever.
func TestLearnReconvergesAfterARadioChange(t *testing.T) {
	c := Car{ID: "A", Join: -80, Leave: -85, Rejoin: -77, FailRate: 5}
	p := &profile{ID: "A"}
	rng := rand.New(rand.NewSource(3))

	feed(p, 600, -79, 2, rng) // old dongle, behind the wall
	before, _ := p.derive(c)

	// New dongle with a better antenna: everything 15 dB stronger.
	feed(p, learnWindow, -64, 2, rng)
	after, ok := p.derive(c)
	if !ok {
		t.Fatal("not ready")
	}
	if after.Rejoin <= before.Rejoin+8 {
		t.Errorf("did not follow the shift: rejoin %d -> %d (expected ~+15)", before.Rejoin, after.Rejoin)
	}
	if !(after.Rejoin >= after.Join && after.Join > after.Leave) {
		t.Errorf("invariant broken after reconverge: join=%d leave=%d rejoin=%d", after.Join, after.Leave, after.Rejoin)
	}
	t.Logf("-79 band -> -64 band: rejoin %d -> %d, join %d -> %d", before.Rejoin, after.Rejoin, before.Join, after.Join)
}

// A failing link must never teach the profile that failure is normal.
func TestLearnIgnoresUnhealthyPolls(t *testing.T) {
	p := &profile{ID: "A"}
	feed(p, 300, -79, 1, rand.New(rand.NewSource(4)))
	mean, n := p.Healthy.Mean, p.Healthy.N
	for i := 0; i < 200; i++ {
		p.observe(-93, 20, false) // dying link
	}
	if p.Healthy.N != n || p.Healthy.Mean != mean {
		t.Errorf("unhealthy polls changed the profile: n %d->%d mean %.2f->%.2f",
			n, p.Healthy.N, mean, p.Healthy.Mean)
	}
}

// A link that shows steady failure noise while still working should raise its
// own fail threshold, never lower it below the configured floor.
func TestLearnedFailRateOnlyRises(t *testing.T) {
	c := Car{ID: "A", Join: -80, Leave: -85, Rejoin: -77, FailRate: 5}

	quiet := &profile{ID: "A"}
	feed(quiet, 600, -79, 2, rand.New(rand.NewSource(5)))
	if got, _ := quiet.derive(c); got.FailRate != 5 {
		t.Errorf("quiet link changed the floor: got %d, want 5", got.FailRate)
	}

	noisy := &profile{ID: "A"}
	for i := 0; i < 600; i++ {
		noisy.observe(-79, 4, true) // chatty but working
	}
	if got, _ := noisy.derive(c); got.FailRate <= 5 {
		t.Errorf("noisy link did not raise the threshold: got %d, want > 5", got.FailRate)
	}
}

// Learned values must stay inside physical sanity rails even if fed nonsense.
func TestLearnedValuesAreClamped(t *testing.T) {
	c := Car{ID: "A", Join: -80, Leave: -85, Rejoin: -77, FailRate: 5}
	p := &profile{ID: "A"}
	for i := 0; i < 600; i++ {
		p.observe(-200, 0, true)
	}
	got, _ := p.derive(c)
	for _, v := range []int{got.Join, got.Leave, got.Rejoin} {
		if v < minDBm || v > maxDBm {
			t.Errorf("value %d escaped the rails [%d,%d]", v, minDBm, maxDBm)
		}
	}
	if !(got.Rejoin >= got.Join && got.Join > got.Leave) {
		t.Errorf("invariant broken under clamping: join=%d leave=%d rejoin=%d", got.Join, got.Leave, got.Rejoin)
	}
}

// Learning has to survive a restart, and a corrupt or old state file must
// degrade to "learn again", never to broken thresholds.
func TestProfilesPersistAndTolerateGarbage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "learned.json")

	ps := newProfiles(path)
	feed(ps.get("A"), 300, -79, 2, rand.New(rand.NewSource(6)))
	if err := ps.save(); err != nil {
		t.Fatal(err)
	}
	back := loadProfiles(path)
	if back.Cars["A"] == nil || back.Cars["A"].Healthy.N != 300 {
		t.Fatalf("profile did not round-trip: %+v", back.Cars["A"])
	}

	for _, bad := range []string{"{", `{"version":999,"cars":{"A":{}}}`, ""} {
		if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		got := loadProfiles(path)
		if got == nil || got.Cars == nil {
			t.Fatalf("garbage state file %q produced an unusable result", bad)
		}
		if p := got.Cars["A"]; p != nil && p.ready() {
			t.Errorf("garbage state file %q produced usable thresholds", bad)
		}
	}
	if got := loadProfiles(filepath.Join(dir, "missing.json")); got.Cars == nil {
		t.Error("missing state file produced an unusable result")
	}
}
