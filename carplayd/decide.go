package main

import (
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
		if e.SSID != c.SSID || e.Freq != c.Freq() {
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
