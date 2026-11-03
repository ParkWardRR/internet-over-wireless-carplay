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

// phonesIn counts neighbours with an address in prefix that look alive, from
// `ip -6 neigh show dev X` output. Only phones autoconfigure in our prefix (the
// head unit didn't on the real car), so this is the phone count.
func phonesIn(neigh string, prefix netip.Prefix) int {
	n := 0
	for _, l := range strings.Split(neigh, "\n") {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		a, err := netip.ParseAddr(f[0])
		if err != nil || !prefix.Contains(a) {
			continue
		}
		switch f[len(f)-1] {
		case "REACHABLE", "STALE", "DELAY", "PROBE":
			n++
		}
	}
	return n
}
