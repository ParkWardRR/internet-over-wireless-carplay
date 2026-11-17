package main

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
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

// Learning must reach the LEAVE decision, not just join and rejoin.
//
// This exists because it did not. An edit wiring the learned car into the leave
// block was lost before it was ever committed -- `git log -S` finds it in no
// commit -- so for a whole evening the box collected 141 healthy samples,
// persisted them, used them for join/rejoin, and then made every leave decision
// against the configured thresholds. Nothing failed, no test went red, and the
// one trace field that would have shown it was lost in the same incident.
//
// The check is behavioural: feed decideLeave a signal that the CONFIGURED
// thresholds call fine and the LEARNED ones call a departure, and require the
// departure. Asserting on d.learned() alone would not have caught it -- that
// function was never broken; its caller simply did not exist.
func TestLearnedThresholdsReachTheLeaveDecision(t *testing.T) {
	// A link that lives around -85 dBm, configured as if it were far stronger,
	// so learning has to make the leave MORE patient than the configuration.
	// Drop and FailRate are off so only the absolute threshold can fire.
	configured := Car{ID: "A", Join: -80, Leave: -70, Rejoin: -77}
	cfg := defaultConfig()
	cfg.Learn = true
	cfg.LeaveFor = 2 * time.Second
	cfg.Cars = []Car{configured}

	d := &daemon{cfg: cfg, serving: &configured, prof: newProfiles(""), m: newMetrics()}
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 600; i++ {
		d.prof.Observe("A", int(-85+rng.NormFloat64()*2), 0, true)
	}

	sc := d.learned(configured)
	// INVERTED 2026-11-16. This used to prove the learned leave reaches the
	// decision by making it MORE eager than configured and showing the box
	// left. A learned leave can no longer be more eager -- see
	// TestLearnedLeaveNeverMoreEagerThanConfigured and derive() -- because the
	// F32 parked at -51 dBm, learned a leave near -60 against a configured
	// -85, and abandoned a car it had served happily.
	//
	// The intent still holds and is still worth proving: the learned number,
	// not the configured one, has to reach decideLeave. So the lever is now
	// patience instead of eagerness. The configured leave here is deliberately
	// too eager for a link that actually lives at -85, and the test shows the
	// box STAYS at a signal the configuration alone would have left on.
	if sc.Leave >= configured.Leave {
		t.Fatalf("learned leave %d did not move below the configured %d for an -85 dBm link; "+
			"the rest of this test proves nothing", sc.Leave, configured.Leave)
	}
	// A signal that is fine for this link but under the configured threshold.
	sig := (sc.Leave + configured.Leave) / 2
	if !(sig > sc.Leave && sig < configured.Leave) {
		t.Fatalf("test setup: %d does not sit between learned %d and configured %d",
			sig, sc.Leave, configured.Leave)
	}

	start := time.Now()
	var why string
	for i := 0; i < 10 && why == ""; i++ {
		why = d.decideLeave(sig, start.Add(time.Duration(i)*time.Second)).why
	}
	if why != "" {
		t.Errorf("at %d dBm the box left (%q): learned leave is %d, so the decision is still "+
			"using the configured %d", sig, why, sc.Leave, configured.Leave)
	}

	// The pick path and the leave path must agree on the same car, or a box can
	// join on one set of numbers and leave on another.
	if pickSide := d.learnedCars()[0]; pickSide != sc {
		t.Errorf("join path sees %+v but leave path sees %+v", pickSide, sc)
	}

	// With learning off nothing is substituted -- and the same signal now ends
	// the session, because the configured threshold is the eager one. That
	// contrast is the proof: the only difference between staying and leaving at
	// this signal is whether the learned number reached the decision.
	d.cfg.Learn = false
	if off := d.learned(configured); off != configured {
		t.Fatalf("learn=off still substituted: %+v", off)
	}
	d.below, d.belowTrend, d.belowFail = time.Time{}, time.Time{}, time.Time{}
	var offWhy string
	for i := 0; i < 10 && offWhy == ""; i++ {
		offWhy = d.decideLeave(sig, start.Add(time.Duration(20+i)*time.Second)).why
	}
	if offWhy == "" {
		t.Errorf("learn=off stayed at %d dBm despite the configured leave being %d; "+
			"then the learned value proved nothing", sig, configured.Leave)
	}
}

// The F32 flap, 2026-11-16. It parked far closer than it ever had, its healthy
// mean learned to -51.1 dBm, and the derived leave went to roughly -60 against
// a configured -85 -- so the box gave up on a car at a signal it had served
// happily. A learned leave may hold on longer than configured; it may never
// give up sooner.
func TestLearnedLeaveNeverMoreEagerThanConfigured(t *testing.T) {
	cfg := Car{ID: "A", Join: -80, Leave: -85, Rejoin: -77, FailRate: 5}
	for _, tc := range []struct {
		name       string
		mean, vari float64
	}{
		{"the real F32 numbers", -51.1, 8.4},
		{"parked very close", -40, 4},
		{"parked normally", -70, 12},
		{"zero variance", -55, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := newProfiles("")
			p := ps.get("A")
			p.Healthy = ewma{Mean: tc.mean, Var: tc.vari, N: 500}
			p.Quiet = ewma{Mean: 0.5, Var: 0.2, N: 500}
			got, ok := ps.Derive(cfg)
			if !ok {
				t.Fatal("profile should be ready with n=500")
			}
			if got.Leave > cfg.Leave {
				t.Errorf("derived leave %d is more eager than the configured %d", got.Leave, cfg.Leave)
			}
			// and the hysteresis invariant must still hold
			if !(got.Leave < got.Join && got.Join <= got.Rejoin) {
				t.Errorf("hysteresis broken: leave=%d join=%d rejoin=%d", got.Leave, got.Join, got.Rejoin)
			}
		})
	}
}

// The clamp must not stop learning from being MORE patient than configured --
// that direction is the point of learning on a link where parked and dying
// signal overlap.
func TestLearnedLeaveMayStillBeMorePatient(t *testing.T) {
	cfg := Car{ID: "A", Join: -80, Leave: -70, Rejoin: -77, FailRate: 5}
	ps := newProfiles("")
	p := ps.get("A")
	p.Healthy = ewma{Mean: -78, Var: 9, N: 500}
	p.Quiet = ewma{Mean: 0.5, Var: 0.2, N: 500}
	got, _ := ps.Derive(cfg)
	if got.Leave >= cfg.Leave {
		t.Fatalf("a car that lives at -78 should learn a leave below the configured -70, got %d", got.Leave)
	}
}
