package main

import (
	"net/netip"
	"strings"
	"time"
)

// pick chooses which car to join from one scan. A car qualifies if its SSID
// (and BSSID, when pinned) is seen on its channel at or above its join
// threshold, or its stricter rejoin threshold while it is held off after we
// left it. Among qualifying cars the higher priority wins, then the stronger
// signal. ok is false when nothing qualifies.
func pick(cars []Car, held map[string]bool, seen []scanEntry) (car Car, entry scanEntry, ok bool) {
	for _, c := range cars {
		e, found := best(c, seen)
		if !found {
			continue
		}
		need := c.Join
		if held[c.ID] {
			need = c.Rejoin
		}
		if e.Signal < need {
			continue
		}
		if !ok || c.Priority > car.Priority || (c.Priority == car.Priority && e.Signal > entry.Signal) {
			car, entry, ok = c, e, true
		}
	}
	return car, entry, ok
}

// best returns the strongest scan entry belonging to car c.
func best(c Car, seen []scanEntry) (scanEntry, bool) {
	var out scanEntry
	found := false
	for _, e := range seen {
		if e.SSID != c.SSID || (c.Freq() != 0 && e.Freq != c.Freq()) {
			continue
		}
		if c.BSSID != "" && !strings.EqualFold(e.BSSID, c.BSSID) {
			continue
		}
		if !found || e.Signal > out.Signal {
			out, found = e, true
		}
	}
	return out, found
}

// releaseHolds clears the hold-off for cars absent from a scan: once the AP
// has gone (car off or far away), its return counts as a fresh arrival.
func releaseHolds(cars []Car, held map[string]bool, seen []scanEntry) {
	for _, c := range cars {
		if _, found := best(c, seen); !found {
			delete(held, c.ID)
		}
	}
}

// leaveCheck tracks how long the signal has been below the leave threshold.
// It returns the updated "below since" time (zero when above) and whether
// it has been below for at least leaveFor.
func leaveCheck(below time.Time, now time.Time, signal, leave int, leaveFor time.Duration) (time.Time, bool) {
	if signal >= leave {
		return time.Time{}, false
	}
	if below.IsZero() {
		below = now
	}
	return below, now.Sub(below) >= leaveFor
}

// shouldSwitch decides whether to leave the car we serve for another one that
// is also on. Only a single STA serves one car at a time, so: never abandon a
// car with a phone; switch only when the served car has had no phone for
// idle, and the other car appeared after we started serving this one (a car
// that just started probably has a driver getting in). Requiring "appeared
// later" also stops two phone-less cars from ping-ponging.
func shouldSwitch(now, servingSince, lastPhone time.Time, idle time.Duration, otherFirstSeen time.Time) bool {
	if otherFirstSeen.IsZero() || !otherFirstSeen.After(servingSince) {
		return false
	}
	quietSince := servingSince
	if lastPhone.After(quietSince) {
		quietSince = lastPhone
	}
	return now.Sub(quietSince) >= idle
}

// neighbour is one line of `ip -6 neigh show dev X`.
type neighbour struct {
	addr  netip.Addr
	mac   string
	state string
}

func parseNeigh(out string) []neighbour {
	var ns []neighbour
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		a, err := netip.ParseAddr(f[0])
		if err != nil {
			continue
		}
		n := neighbour{addr: a, state: f[len(f)-1]}
		for i := 1; i+1 < len(f); i++ {
			if f[i] == "lladdr" {
				n.mac = strings.ToLower(f[i+1])
			}
		}
		ns = append(ns, n)
	}
	return ns
}

func (n neighbour) alive() bool {
	switch n.state {
	case "REACHABLE", "STALE", "DELAY", "PROBE":
		return true
	}
	return false
}

// phonesIn counts the devices (unique link-layer addresses) with a live
// neighbour entry in prefix. One iPhone holds several addresses at once
// (stable + temporary), so counting addresses would overcount. Only phones
// autoconfigure in our prefix (the head unit didn't on the real car).
func phonesIn(neigh string, prefix netip.Prefix) int {
	macs := map[string]bool{}
	for _, n := range parseNeigh(neigh) {
		if n.alive() && n.mac != "" && prefix.Contains(n.addr) {
			macs[n.mac] = true
		}
	}
	return len(macs)
}

// withdrawTargets picks one address per device on the link for a unicast
// withdrawal RA: its link-local if we know it (a host acts on an RA to any of
// its addresses), else an address in prefix.
func withdrawTargets(neigh string, prefix netip.Prefix) []netip.Addr {
	best := map[string]netip.Addr{}
	var order []string
	for _, n := range parseNeigh(neigh) {
		if n.mac == "" || n.state == "FAILED" || n.state == "INCOMPLETE" {
			continue
		}
		if !n.addr.IsLinkLocalUnicast() && !prefix.Contains(n.addr) {
			continue
		}
		cur, ok := best[n.mac]
		if !ok {
			order = append(order, n.mac)
		}
		if !ok || (n.addr.IsLinkLocalUnicast() && !cur.IsLinkLocalUnicast()) {
			best[n.mac] = n.addr
		}
	}
	out := make([]netip.Addr, 0, len(order))
	for _, m := range order {
		out = append(out, best[m])
	}
	return out
}

// preempt lets a higher-priority car take over right after we started serving
// a lower one, while no phone has appeared yet: when two cars start within
// seconds of each other, the first beacon heard shouldn't decide. After the
// grace period, or once a phone is seen, only shouldSwitch applies.
func preempt(servingPrio, otherPrio int, servingSince, lastPhone, now time.Time, grace time.Duration) bool {
	return otherPrio > servingPrio && lastPhone.IsZero() && now.Sub(servingSince) < grace
}

// surveyDue: in survey mode (every > 0), log a car's signal again once
// `every` has passed since the last line for it.
func surveyDue(last, now time.Time, every time.Duration) bool {
	return every > 0 && now.Sub(last) >= every
}

// sample is one signal poll while serving.
type sample struct {
	t   time.Time
	sig int
}

// Trend window: the baseline is where the signal sat 5-20 s ago, so a car
// driving away is compared with where it was parked, not with its own fall.
const (
	trendFrom = 20 * time.Second
	trendTo   = 5 * time.Second
	trendKeep = 25 * time.Second
)

// baselineOf is the median signal of samples between trendFrom and trendTo
// ago; ok is false until there are at least 5 of them.
func baselineOf(samples []sample, now time.Time) (int, bool) {
	var v []int
	for _, s := range samples {
		if age := now.Sub(s.t); age >= trendTo && age <= trendFrom {
			v = append(v, s.sig)
		}
	}
	if len(v) < 5 {
		return 0, false
	}
	for i := 1; i < len(v); i++ { // insertion sort: at most ~16 values
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
	return v[len(v)/2], true
}

// trimSamples drops samples older than trendKeep.
func trimSamples(samples []sample, now time.Time) []sample {
	i := 0
	for i < len(samples) && now.Sub(samples[i].t) > trendKeep {
		i++
	}
	return samples[i:]
}

// iwStation pulls the counters we log from `iw dev X station dump`.
func iwStation(out string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), ":")
		if !ok {
			continue
		}
		switch k {
		case "tx retries", "tx failed", "rx bytes", "tx bytes", "beacon loss":
			m[strings.ReplaceAll(k, " ", "_")] = strings.TrimSpace(v)
		case "tx bitrate", "rx bitrate":
			if f := strings.Fields(v); len(f) > 0 {
				m[strings.ReplaceAll(k, " ", "_")] = f[0]
			}
		}
	}
	return m
}

// relaxRejoin returns cars with the rejoin threshold replaced for cars that
// left on a signal trend (relaxed[id] = near the level they were parked at).
// It can be stricter than Rejoin (the car left above it) or easier (a dip while
// parked at a weak spot).
func relaxRejoin(cars []Car, relaxed map[string]int) []Car {
	if len(relaxed) == 0 {
		return cars
	}
	out := append([]Car(nil), cars...)
	for i := range out {
		if v, ok := relaxed[out[i].ID]; ok {
			out[i].Rejoin = v
		}
	}
	return out
}
