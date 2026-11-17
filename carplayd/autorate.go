package main

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

// Automatic rate selection for a car link.
//
// WHY A CONTROLLER AND NOT A NUMBER. The right cap is not a property of the
// protocol, it is a property of this car, this parking spot and this moment.
// The link is 1x1 HT20 at 52-65 Mbit/s PHY, every byte we forward crosses the
// air twice because the head unit relays it, and CarPlay's own screen and audio
// stream shares the same channel. 2 Mbit/s was picked by hand from one
// measurement; it was both too low for a quiet link and, on the evidence of a
// residual stutter, possibly still too high for a busy one.
//
// WHAT IT STEERS BY. Round-trip time to the phone, measured *while we are
// actually sending*. That is the one signal on the box that moves with the
// shared medium: unshaped it reached 2051 ms, capped it sat at 30-130 ms. We
// cannot see CarPlay's own stream — it never passes through the box — so we
// cannot measure it directly. Latency to the phone is the proxy, because
// CarPlay's packets queue behind the same airtime and the same relay.
//
// THE TRAP THIS DESIGN AVOIDS. At idle the same measurement is garbage: the
// phone's Wi-Fi power save puts round trips at 4 ms one second and 2051 ms the
// next, with no load at all. A naive controller would read that as congestion
// and ratchet itself down to the floor while nothing was happening. So every
// decision is gated on there having been real demand in the interval — measured
// as the shaper actually hitting its limit — and an idle interval holds the
// rate rather than moving it.
//
// DIRECTION ASYMMETRY. Down fast, up slow: a rate that is too high can drop the
// CarPlay session and cost a head-unit reset, while a rate that is too low only
// costs download speed. Hence a 30% multiplicative decrease against a 25%
// increase, and an increase only ever under demonstrated demand.

const (
	// rateDeadbandNum/Den: the fraction of the target below which we probe
	// upward. Between that and the target the rate holds, so the controller
	// settles instead of oscillating around the boundary.
	rateDeadbandNum = 2
	rateDeadbandDen = 3

	rateUpNum, rateUpDen     = 5, 4  // +25%
	rateDownNum, rateDownDen = 7, 10 // -30%

	// rateMinSamples: fewer round-trip samples than this in an interval is not
	// a measurement, it is noise. Hold.
	rateMinSamples = 5
)

type rateInputs struct {
	CurKbit   int
	MinKbit   int
	MaxKbit   int
	CeilKbit  int // learned "never again" bound; 0 = none
	TargetRTT time.Duration
	LoadedP95 time.Duration // p95 round trip measured while sending
	Loaded    bool          // the shaper hit its limit this interval
	Samples   int
}

type rateVerdict struct {
	Kbit   int
	Reason string
}

// decideRate is pure so the policy can be tested without a radio, a phone or a
// car. Everything that needs hardware is in the caller.
func decideRate(in rateInputs) rateVerdict {
	cur := in.CurKbit
	clamp := func(v int) int {
		if in.CeilKbit > 0 && v > in.CeilKbit {
			v = in.CeilKbit
		}
		if in.MaxKbit > 0 && v > in.MaxKbit {
			v = in.MaxKbit
		}
		if v < in.MinKbit {
			v = in.MinKbit
		}
		return v
	}
	// A ceiling learned after an unexplained loss outranks everything: honour
	// it even on an interval we would otherwise not act on.
	if c := clamp(cur); c != cur && (in.CeilKbit > 0 || cur > in.MaxKbit || cur < in.MinKbit) {
		return rateVerdict{c, fmt.Sprintf("clamped to bounds (ceil=%d max=%d min=%d)", in.CeilKbit, in.MaxKbit, in.MinKbit)}
	}
	if !in.Loaded || in.Samples < rateMinSamples {
		return rateVerdict{cur, "hold: no demand this interval (idle round trips are power save, not congestion)"}
	}
	target := in.TargetRTT
	switch {
	case in.LoadedP95 > target:
		next := clamp(cur * rateDownNum / rateDownDen)
		if next == cur {
			return rateVerdict{cur, fmt.Sprintf("hold: p95 %v over target %v but already at the floor %d kbit", in.LoadedP95, target, in.MinKbit)}
		}
		return rateVerdict{next, fmt.Sprintf("down: p95 %v over target %v", in.LoadedP95, target)}
	case in.LoadedP95 <= target*rateDeadbandNum/rateDeadbandDen:
		next := clamp(cur * rateUpNum / rateUpDen)
		if next == cur {
			return rateVerdict{cur, fmt.Sprintf("hold: p95 %v has headroom but already at the ceiling %d kbit", in.LoadedP95, clamp(in.MaxKbit))}
		}
		return rateVerdict{next, fmt.Sprintf("up: p95 %v under %d/%d of target %v, and demand exists", in.LoadedP95, rateDeadbandNum, rateDeadbandDen, target)}
	default:
		return rateVerdict{cur, fmt.Sprintf("hold: p95 %v inside the deadband below target %v", in.LoadedP95, target)}
	}
}

// p95 returns the 95th percentile of a set of round-trip samples, which is the
// statistic the controller steers by. The mean hides exactly the behaviour that
// breaks CarPlay audio: a stream does not care about the average, it cares
// about the late packet. Returns 0 for no samples.
func p95(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := make([]time.Duration, len(d))
	copy(s, d)
	// insertion sort: these slices are a handful of samples
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	// index of the 95th percentile, nearest-rank
	i := (len(s)*95 + 99) / 100
	if i > len(s) {
		i = len(s)
	}
	return s[i-1]
}

// kbitToTC renders a rate for tc. kbit throughout: tc accepts it, and integer
// kbit avoids the float formatting that would make "1.5mbit" and "1500kbit"
// two different strings for one rate.
func kbitToTC(kbit int) string { return fmt.Sprintf("%dkbit", kbit) }

// --- the driver: everything below needs a radio, a phone and a shaper --------

// autoRateEvery is the decision interval. Long enough that a change has been
// felt by TCP before the next decision, short enough to react within a track.
const autoRateEvery = 15 * time.Second

// autoRateStartKbit is where an unlearned car starts: the one value measured
// safe on a real car (2 Mbit/s held 120 s of saturation with no disconnect).
// Not the maximum -- an unmeasured car should climb into its headroom under
// observation, not begin by probing the region that broke a head unit.
const autoRateStartKbit = 2000

// effectiveRate is the tc rate string to apply on a join: a fixed value as
// configured, or for auto mode the learned rate if this car has one.
func (d *daemon) effectiveRate(car *Car) string {
	if car.Rate != "auto" {
		return car.Rate
	}
	lo, _ := parseRateKbit(car.RateMin)
	hi, _ := parseRateKbit(car.RateMax)
	k := autoRateStartKbit
	if learned := d.prof.rate(car.ID); learned > 0 {
		k = learned
	}
	if ceil := d.prof.rateCeil(car.ID); ceil > 0 && k > ceil {
		k = ceil
	}
	if hi > 0 && k > hi {
		k = hi
	}
	if k < lo {
		k = lo
	}
	return kbitToTC(k)
}

// phoneAddr returns one phone address on the serving car's prefix, or "".
func (d *daemon) phoneAddr(car *Car) string {
	ns, err := d.net().Neighbours(d.cfg.STA)
	if err != nil {
		return ""
	}
	for _, n := range ns {
		if !car.Prefix.Contains(n.addr) || n.addr == car.Prefix.Addr().Next() {
			continue
		}
		return n.addr.String()
	}
	return ""
}

var rttRe = regexp.MustCompile(`time=([0-9.]+) ms`)

// pingRTT samples the round trip to the phone through the head unit's relay.
// Shelling out to ping6 rather than opening a raw socket: the daemon already
// shells to iw, ip and nft, and ping6 handles the interface binding and the
// per-probe timeout that this would otherwise have to reimplement.
func pingRTT(sta, addr string, n int) []time.Duration {
	out, _ := exec.Command("ping6", "-c", strconv.Itoa(n), "-i", "0.2", "-W", "1", "-I", sta, addr).Output()
	var d []time.Duration
	for _, m := range rttRe.FindAllStringSubmatch(string(out), -1) {
		if f, err := strconv.ParseFloat(m[1], 64); err == nil {
			d = append(d, time.Duration(f*float64(time.Millisecond)))
		}
	}
	return d
}

// tuneRate is one decision. It is a no-op unless this link is serving a car in
// auto mode and a phone is present.
func (d *daemon) tuneRate() {
	d.servingMu.Lock()
	car := d.serving
	d.servingMu.Unlock()
	if car == nil || car.Rate != "auto" {
		return
	}
	addr := d.phoneAddr(car)
	if addr == "" {
		return // nothing to measure against
	}
	st := d.shapeStatsNow()
	loaded := st.Overlimits > d.lastShape.Overlimits
	d.lastShape = st

	samples := pingRTT(d.cfg.STA, addr, 12)
	lo, _ := parseRateKbit(car.RateMin)
	hi, _ := parseRateKbit(car.RateMax)
	in := rateInputs{
		CurKbit: d.rateKbit, MinKbit: lo, MaxKbit: hi,
		CeilKbit: d.prof.rateCeil(car.ID), TargetRTT: car.RateRTT,
		LoadedP95: p95(samples), Loaded: loaded, Samples: len(samples),
	}
	if in.CurKbit == 0 {
		in.CurKbit, _ = parseRateKbit(d.effectiveRate(car))
	}
	v := decideRate(in)
	if v.Kbit == in.CurKbit {
		return // held; the reason is only interesting when something changed
	}
	if err := applyShaping(d.cfg.STA, kbitToTC(v.Kbit), run); err != nil {
		d.ev("error", "autorate", err.Error())
		return
	}
	d.rateKbit = v.Kbit
	d.prof.setRate(car.ID, v.Kbit)
	d.ev("autorate", car.ID, fmt.Sprintf("rate=%dkbit", v.Kbit),
		fmt.Sprintf("was=%dkbit", in.CurKbit), fmt.Sprintf("p95=%v", in.LoadedP95),
		fmt.Sprintf("target=%v", car.RateRTT), "why="+v.Reason)
}

// shapeStatsNow reads the shaper's counters off the live qdisc.
func (d *daemon) shapeStatsNow() shapeStats {
	out, err := exec.Command("tc", "-s", "class", "show", "dev", d.cfg.STA).Output()
	if err != nil {
		return shapeStats{}
	}
	return parseShapeStats(string(out))
}

// penaliseRate records that the link died while the signal was healthy, which
// is the signature of us having overdriven the head unit rather than of the car
// driving away. The rate in force becomes a ceiling, 30% under it, and persists
// so the next session does not rediscover it the same way.
//
// Heuristic, and labelled as one: a head unit dropping CarPlay and a car
// leaving abruptly are not perfectly separable from the box. Gating on a
// healthy signal is what keeps an ordinary departure from ratcheting the cap
// down over time.
func (d *daemon) penaliseRate(car *Car, sig, leaveAt int) {
	if car.Rate != "auto" || d.rateKbit == 0 || sig <= leaveAt {
		return
	}
	ceil := d.rateKbit * rateDownNum / rateDownDen
	if lo, _ := parseRateKbit(car.RateMin); ceil < lo {
		ceil = lo
	}
	if prev := d.prof.rateCeil(car.ID); prev > 0 && prev <= ceil {
		return
	}
	d.prof.setRateCeil(car.ID, ceil)
	d.ev("autorate_ceiling", car.ID, fmt.Sprintf("ceil=%dkbit", ceil),
		fmt.Sprintf("lost_at=%dkbit", d.rateKbit), fmt.Sprintf("signal=%d", sig),
		"why=link lost while the signal was healthy, so treat the rate as unsafe")
}
