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

// holdOff drops cars that left too recently to be rejoined, whatever their
// signal looks like now.
//
// A level-based rule cannot do this on its own. On the 2026-11-11 F22
// departure the signal genuinely recovered to -76 dBm twelve seconds after a
// leave -- multipath through the garage wall is not monotonic in distance -- so
// the box rejoined a car it was actively driving away from, then had to leave
// again fourteen seconds later. Only elapsed time distinguishes "the link
// recovered" from "we are still leaving".
func holdOff(cars []Car, leftAt map[string]time.Time, now time.Time, d time.Duration) []Car {
	if d <= 0 || len(leftAt) == 0 {
		return cars
	}
	out := make([]Car, 0, len(cars))
	for _, c := range cars {
		if t, ok := leftAt[c.ID]; ok && now.Sub(t) < d {
			continue
		}
		out = append(out, c)
	}
	return out
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

// sustain tracks how long a condition has held. It returns the updated "true
// since" time (zero while false) and whether it has held for at least `for_`.
// leaveCheck is the signal-threshold special case of this.
func sustain(since time.Time, now time.Time, cond bool, for_ time.Duration) (time.Time, bool) {
	if !cond {
		return time.Time{}, false
	}
	if since.IsZero() {
		since = now
	}
	return since, now.Sub(since) >= for_
}

// failRate is the per-second rise in the driver's tx_failed counter. It reports
// ok = false when there is nothing to compare: no previous reading, no elapsed
// time, or a counter that went backwards (the station re-associated and its
// counters restarted, which is not a failing link).
func failRate(prev, cur int, elapsed time.Duration) (int, bool) {
	if prev < 0 || cur < prev || elapsed <= 0 {
		return 0, false
	}
	return int(float64(cur-prev) / elapsed.Seconds()), true
}

// failSample is one tx_failed reading.
type failSample struct {
	t time.Time
	n int
}

// failWindow: the failure rate is measured across a trailing window, not
// between consecutive polls. The poll-to-poll delta is far too noisy to hold a
// threshold against — on the real departure it alternated above and below from
// one second to the next (53, 4, 6, 6, 5 per second), which kept restarting
// the sustain timer and pushed the leave to one second before the link died.
const failWindow = 5 * time.Second

// failRateOf is the per-second rise in tx_failed across the window.
func failRateOf(s []failSample) (int, bool) {
	if len(s) < 2 {
		return 0, false
	}
	first, last := s[0], s[len(s)-1]
	return failRate(first.n, last.n, last.t.Sub(first.t))
}

// trimFail drops readings older than failWindow.
func trimFail(s []failSample, now time.Time) []failSample {
	i := 0
	for i < len(s) && now.Sub(s[i].t) > failWindow {
		i++
	}
	return s[i:]
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

// since renders how long ago t was, or "-" when it is unset (not below any
// threshold right now).
func since(t, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return now.Sub(t).Truncate(100 * time.Millisecond).String()
}

// parseEventArgs pulls the "key=value" arguments out of a wpa_supplicant
// control event line, e.g.
//
//	CTRL-EVENT-DISCONNECTED bssid=xx:xx:xx:xx:xx:xx reason=3 locally_generated=1
func parseEventArgs(e string) map[string]string {
	m := map[string]string{}
	for _, f := range strings.Fields(e) {
		if k, v, ok := strings.Cut(f, "="); ok {
			m[k] = v
		}
	}
	return m
}

func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

// reasonName names an IEEE 802.11 reason code (802.11-2020 table 9-49), so a
// departure's log says what happened without a standards lookup. The codes
// this investigation expects: 3 (the sender is leaving), 4 (inactivity, i.e.
// the AP gave up on us), 15 (4-way handshake timeout, out of range mid-rekey).
func reasonName(code string) string {
	switch code {
	case "":
		return "unknown"
	case "1":
		return "unspecified"
	case "2":
		return "prev_auth_invalid"
	case "3":
		return "sta_leaving"
	case "4":
		return "inactivity"
	case "5":
		return "ap_busy"
	case "6":
		return "class2_from_nonauth"
	case "7":
		return "class3_from_nonassoc"
	case "8":
		return "sta_left_bss"
	case "9":
		return "assoc_without_auth"
	case "14":
		return "mic_failure"
	case "15":
		return "4way_handshake_timeout"
	case "16":
		return "group_key_timeout"
	case "17":
		return "ie_mismatch"
	case "23":
		return "ieee8021x_auth_failed"
	case "34":
		return "poor_rssi_or_low_ack"
	}
	return "code" + code
}

// iwStation pulls the counters we log from `iw dev X station dump`.
//
// Values that carry a unit or a per-chain list ("-70 [-70] dBm", "39.0 MBit/s",
// "12 seconds") keep only their first field; plain counts are taken whole.
func iwStation(out string) map[string]string {
	whole := map[string]bool{
		"tx retries": true, "tx failed": true, "rx bytes": true, "tx bytes": true,
		"rx packets": true, "tx packets": true, "beacon loss": true, "beacon rx": true,
		"rx drop misc": true, "authorized": true, "tx retried": true,
	}
	first := map[string]bool{
		"tx bitrate": true, "rx bitrate": true, "signal": true, "signal avg": true,
		"beacon signal avg": true, "expected throughput": true,
		"connected time": true, "inactive time": true,
		"rx duration": true, "tx duration": true,
	}
	m := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), ":")
		if !ok {
			continue
		}
		key := strings.ReplaceAll(k, " ", "_")
		switch {
		case whole[k]:
			m[key] = strings.TrimSpace(v)
		case first[k]:
			if f := strings.Fields(v); len(f) > 0 {
				m[key] = f[0]
			}
		}
	}
	return m
}

// linkFields is what the `signal` event reports from the station dump, in the
// order it reads best: rates, then how the radio sees the AP, then the error
// and volume counters, then how long the link has been up and idle.
var linkFields = []string{
	"tx_bitrate", "rx_bitrate",
	"signal", "signal_avg", "beacon_signal_avg", "beacon_rx", "beacon_loss",
	"tx_retries", "tx_failed", "rx_drop_misc",
	"tx_packets", "rx_packets", "tx_bytes", "rx_bytes",
	"expected_throughput", "connected_time", "inactive_time",
}

// relaxRejoin returns cars with the rejoin threshold replaced for cars that
// left on a signal trend (relaxed[id] = near the level they were parked at).
// Only ever stricter than Rejoin, never easier: an "easier" relaxed threshold
// is what let the box rejoin a car it had just left while still driving away
// (docs/test-log.md, 2026-11-11 F22) and then cling on at 1 Mb/s.
func relaxRejoin(cars []Car, relaxed map[string]int) []Car {
	if len(relaxed) == 0 {
		return cars
	}
	out := append([]Car(nil), cars...)
	for i := range out {
		if v, ok := relaxed[out[i].ID]; ok {
			out[i].Rejoin = max(v, out[i].Rejoin)
		}
	}
	return out
}
