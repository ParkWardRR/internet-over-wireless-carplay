package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Learned thresholds.
//
// Absolute dBm thresholds cannot be configured for this problem. In the garage
// this was written for, the box sits behind a cinderblock wall 6-11 ft from the
// cars: a parked car reads -74..-82 dBm where free space would give about -40,
// the 5 ft further car often reads *stronger* than the nearer one, and parked
// signal overlaps the signal at which the link dies. Numbers tuned there mean
// nothing in another garage, change when a car parks three feet left, and all
// move at once when a radio is swapped.
//
// So carplayd learns, per car, what the link looks like while it is *working*,
// and derives its thresholds from that distribution. The configured values
// become the starting point and the outer bound, not the rule.
//
// What is deliberately not learned: fail_rate's floor. The failure-rate rule is
// already self-calibrating — a failing link's tx_failed rate is 7-21/s against
// 0-1/s healthy, regardless of radio or geometry — so learning only ever raises
// its threshold to clear observed noise, never lowers it.

const (
	// minLearn: healthy samples before learned thresholds are used at all. At
	// one poll a second that is two minutes of actually serving a car.
	minLearn = 120

	// learnWindow: the effective memory, in samples. A car that moves to a new
	// parking spot, or a radio swap that shifts every reading, converges in
	// roughly this many seconds of serving rather than being averaged against
	// months of history.
	learnWindow = 600

	// minSpread: a floor on the learned standard deviation, in dB. A link that
	// happens to sit very still would otherwise derive thresholds a hair apart
	// and flap on ordinary multipath.
	minSpread = 2.0

	// regimeShift / regimeFor: a reading this many standard deviations outside
	// the learned band, this many samples in a row, means the world changed
	// (new spot, new antenna) rather than a bad moment. The estimator then
	// re-converges fast instead of fighting its own history.
	regimeShift = 4.0
	regimeFor   = 30

	// minFailMargin: the smallest gap, in failures/second, left between a
	// link's healthy failure noise and the rate that counts as failing.
	minFailMargin = 3.0

	// Sanity rails for anything derived. No learned value escapes these.
	minDBm = -95
	maxDBm = -30
)

// ewma is a decaying mean and variance. During warm-up it averages evenly, so
// early estimates are not dominated by the first sample; after that it decays
// with a fixed weight, which is what lets it forget an old parking spot.
type ewma struct {
	Mean float64 `json:"mean"`
	Var  float64 `json:"var"`
	N    int     `json:"n"`
}

func (e *ewma) add(x float64) {
	e.N++
	a := 1.0 / float64(e.N)
	if a < 1.0/learnWindow {
		a = 1.0 / learnWindow
	}
	d := x - e.Mean
	e.Mean += a * d
	// Decaying variance of the same window.
	e.Var = (1 - a) * (e.Var + a*d*d)
}

func (e *ewma) sd() float64 {
	if e.Var <= 0 {
		return 0
	}
	return math.Sqrt(e.Var)
}

// restart throws the estimate away and reseeds it at x. Used once a regime
// change is confirmed: decaying towards the new world is far too slow (measured
// — a 15 dB antenna change only moved a decayed mean 6.5 dB over a full
// window), and after 30 consecutive readings outside the band there is nothing
// left worth keeping. `ready` then reports false until the new normal has been
// learned, so the box falls back to the configured thresholds in the meantime
// rather than acting on a half-learned mean.
func (e *ewma) restart(x float64) {
	e.Mean, e.Var, e.N = x, 0, 1
}

// profile is what has been learned about one car's link.
type profile struct {
	ID       string    `json:"id"`
	Healthy  ewma      `json:"healthy_dbm"` // RSSI while the link was working
	Quiet    ewma      `json:"quiet_fail"`  // tx_failed/s while the link was working
	Outliers int       `json:"-"`           // consecutive readings outside the band
	Updated  time.Time `json:"updated"`
}

// observe records one poll. healthy says whether the link was working at the
// time: thresholds are learned from working links only, or they would learn the
// failure they are supposed to detect.
func (p *profile) observe(rssi, failPerSec int, healthy bool) {
	if !healthy {
		return
	}
	// Regime change: persistently far outside the learned band.
	if p.Healthy.N >= minLearn {
		sd := math.Max(p.Healthy.sd(), minSpread)
		if math.Abs(float64(rssi)-p.Healthy.Mean) > regimeShift*sd {
			// Hold the estimate while counting. Folding outliers in inflates
			// the variance, which raises the very threshold used to detect
			// them, so the change is never recognised: measured, a 15 dB
			// antenna shift drove sd to ~15 dB and the detector never fired.
			// Holding also keeps a passing transient out of the learned band.
			if p.Outliers++; p.Outliers >= regimeFor {
				p.Healthy.restart(float64(rssi))
				p.Quiet.restart(float64(failPerSec))
				p.Outliers = 0
			}
			return
		}
		p.Outliers = 0
	}
	p.Healthy.add(float64(rssi))
	p.Quiet.add(float64(failPerSec))
	p.Updated = time.Now()
}

// ready reports whether enough has been learned to derive thresholds.
func (p *profile) ready() bool { return p.Healthy.N >= minLearn }

// derive returns the car with learned thresholds substituted, and whether it
// learned anything usable. The configured car is both the fallback and the
// bound: learning may loosen a threshold to where the link demonstrably works,
// but never past what the operator allowed.
func (p *profile) derive(c Car) (Car, bool) {
	if !p.ready() {
		return c, false
	}
	m := p.Healthy.Mean
	sd := math.Max(p.Healthy.sd(), minSpread)

	out := c
	// join: we have worked down here, so it is worth trying.
	out.Join = clampDBm(int(math.Round(m - 1.5*sd)))
	// rejoin: only come back when the link is near its normal self, which is
	// what stops the rejoin churn while driving away.
	out.Rejoin = clampDBm(int(math.Round(m - 0.5*sd)))
	// leave: an outer guard only. The failure-rate rule does the real work,
	// because in this environment RSSI cannot tell parked from dead.
	out.Leave = clampDBm(int(math.Round(m - 3.0*sd)))

	// fail_rate only ever rises above the configured floor, to clear whatever
	// failure noise this link shows while healthy. The absolute margin matters:
	// a link sitting at a steady 4/s has no variance for a multiple of the
	// standard deviation to work with, and would be left one stray failure
	// away from a spurious leave.
	if q := int(math.Round(p.Quiet.Mean + math.Max(4*p.Quiet.sd(), minFailMargin))); q > out.FailRate {
		out.FailRate = q
	}

	// The hysteresis invariant the config parser enforces must survive
	// learning: minDBm <= leave < join <= rejoin. Clamping first and adjusting
	// afterwards is what let a derived leave slip to -96.
	if out.Join > out.Rejoin {
		out.Join = out.Rejoin
	}
	if out.Leave >= out.Join {
		out.Leave = out.Join - 1
	}
	if out.Leave < minDBm {
		out.Leave = minDBm
		if out.Join <= out.Leave {
			out.Join = out.Leave + 1
		}
		if out.Rejoin < out.Join {
			out.Rejoin = out.Join
		}
	}
	return out, true
}

func clampDBm(v int) int {
	if v < minDBm {
		return minDBm
	}
	if v > maxDBm {
		return maxDBm
	}
	return v
}

// profiles is the learned state for every car, persisted so a restart does not
// start from zero.
// profiles is shared by every link, so each car's profile is reached through
// the mutex rather than by handing out pointers into the map.
type profiles struct {
	Version int                 `json:"version"`
	Cars    map[string]*profile `json:"cars"`
	path    string
	mu      sync.Mutex
}

const profilesVersion = 1

func newProfiles(path string) *profiles {
	return &profiles{Version: profilesVersion, Cars: map[string]*profile{}, path: path}
}

// get must be called with mu held.
func (ps *profiles) get(id string) *profile {
	p := ps.Cars[id]
	if p == nil {
		p = &profile{ID: id}
		ps.Cars[id] = p
	}
	return p
}

// Observe records one poll for a car.
func (ps *profiles) Observe(id string, rssi, failPerSec int, healthy bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.get(id).observe(rssi, failPerSec, healthy)
}

// Derive returns the car with learned thresholds substituted.
func (ps *profiles) Derive(c Car) (Car, bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.get(c.ID).derive(c)
}

// Snapshot is what the decide trace reports about a car's learned band.
// A nil receiver reports "nothing learned": the decide trace runs on every
// poll, so it must not depend on learning being configured at all.
func (ps *profiles) Snapshot(id string) (n int, mean, sd float64, ready bool) {
	if ps == nil {
		return 0, 0, 0, false
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	p := ps.get(id)
	return p.Healthy.N, p.Healthy.Mean, p.Healthy.sd(), p.ready()
}

// load reads persisted profiles. A missing file, unreadable file or version
// mismatch is not an error: learning simply starts over, which is always safe
// because the configured thresholds remain the fallback.
func loadProfiles(path string) *profiles {
	ps := newProfiles(path)
	if path == "" {
		return ps
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ps
	}
	var in profiles
	if err := json.Unmarshal(b, &in); err != nil || in.Version != profilesVersion {
		return ps
	}
	if in.Cars != nil {
		ps.Cars = in.Cars
	}
	return ps
}

// save writes the profiles atomically, so a power cut cannot leave a half file
// that would be discarded on the next boot.
func (ps *profiles) save() error {
	if ps.path == "" {
		return nil
	}
	ps.mu.Lock()
	b, err := json.MarshalIndent(ps, "", "  ")
	ps.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := ps.path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(ps.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, ps.path)
}
