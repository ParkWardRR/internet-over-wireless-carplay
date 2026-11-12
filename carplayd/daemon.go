package main

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type daemon struct {
	cfg          Config
	cmd          *wpaConn // requests
	evc          *wpaConn // attached for events
	events       chan string
	held         map[string]bool
	serving      *Car
	ra           *raSender
	below        time.Time
	lastSig      int
	missedPings  int
	m            *metrics
	evMu         sync.Mutex
	servingSince time.Time
	lastPhone    time.Time
	lastArbScan  time.Time
	firstSeen    map[string]time.Time
	lastSurvey   map[string]time.Time // last sighting/signal logged per car (survey)
	samples      []sample             // signal polls of the served car, last 25 s
	belowTrend   time.Time
	failSamples  []failSample // tx_failed readings, last failWindow
	failPerSec   int          // its rise per second across that window
	belowFail    time.Time    // since when failPerSec has been at or over the car's fail_rate
	relaxed      map[string]int // rejoin threshold after a trend leave: near the parked level
	huntTick     int
	phones       int
	evFile       *os.File
	up           health        // upstream reachability (route withdrawn while down)
	healthC      chan upstream // results of background checks
	checking     bool
	lastCheck    time.Time
	blocked      []netip.Prefix // what the firewall currently keeps phones from
}

// ev prints "EV <boottime> carplayd_<name> ..." (the harness's format) and
// appends it to the events file, each line prefixed with the wall clock.
//
// The boottime alone cannot be lined up against the in-car recorder, which
// stamps wall clock (docs/recon-box.md): correlating the two meant knowing the
// box's boot instant. Both now carry the same RFC 3339 field.
func (d *daemon) ev(name string, args ...string) {
	d.evMu.Lock()
	defer d.evMu.Unlock()
	l := fmt.Sprintf("%s EV %.3f carplayd_%s %s",
		time.Now().Format("2006-01-02T15:04:05.000Z07:00"), boottime(), name, strings.Join(args, " "))
	l = strings.TrimSpace(l)
	fmt.Println(l)
	if d.evFile != nil {
		d.evFile.WriteString(l + "\n")
	}
}

// dbg logs only with `debug = on`: the high-volume per-tick detail.
func (d *daemon) dbg(name string, args ...string) {
	if !d.cfg.Debug {
		return
	}
	d.ev("dbg_"+name, args...)
}

// logWPA records every control event wpa_supplicant sends. Only four prefixes
// drive carplayd's state machine and the rest used to be dropped silently,
// which hid exactly the ones a failed departure turns on: ASSOC-REJECT and
// AUTH-REJECT with their status codes, BEACON-LOSS, and the roam/auth timeouts.
// The two that fire every hunt tick stay behind `debug`.
func (d *daemon) logWPA(e string) {
	e = strings.TrimSpace(e)
	if e == "" {
		return
	}
	// These three fire on every hunt tick, once a second: NETWORK-NOT-FOUND
	// alone was 90% of a drive's log volume.
	if strings.HasPrefix(e, "CTRL-EVENT-SCAN-RESULTS") || strings.HasPrefix(e, "CTRL-EVENT-SCAN-STARTED") ||
		strings.HasPrefix(e, "CTRL-EVENT-NETWORK-NOT-FOUND") {
		d.dbg("wpa", e)
		return
	}
	d.ev("wpa", e)
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func writeSysctl(path, v string) error { return os.WriteFile(path, []byte(v), 0o644) }

// setup creates our STA, tunes it, sets up NAT66 if asked, and starts a
// private wpa_supplicant.
func (d *daemon) setup() error {
	c := d.cfg
	if _, err := net.InterfaceByName(c.STA); err != nil {
		if err := run("iw", "phy", c.Phy, "interface", "add", c.STA, "type", "managed"); err != nil {
			return err
		}
	}
	// Before the link comes up: no DAD (the link-local is usable at once, so
	// the first RA isn't held back), and never learn routes from car networks.
	for k, v := range map[string]string{"accept_dad": "0", "accept_ra": "0", "autoconf": "0"} {
		if err := writeSysctl(fmt.Sprintf("/proc/sys/net/ipv6/conf/%s/%s", c.STA, k), v); err != nil {
			return err
		}
	}
	if err := run("ip", "link", "set", c.STA, "up"); err != nil {
		return err
	}
	run("iw", "dev", c.STA, "set", "power_save", "off") // best effort; not all drivers allow it
	if err := d.setupNAT(); err != nil {
		return err
	}
	if err := d.applyFirewall(d.homeBlock()); err != nil {
		return err
	}
	d.healthC = make(chan upstream, 1)
	d.up.ok = true
	if c.Health {
		r := checkUpstream(c.HealthTargets, healthTimeout)
		d.up.ok = r.ok
		d.ev("upstream", map[bool]string{true: "ok", false: "down"}[r.ok], r.detail)
	}
	d.m.set(func(m *metrics) { m.upstream = d.up.ok })
	if err := os.MkdirAll(c.CtrlDir, 0o700); err != nil {
		return err
	}
	return d.startWPA()
}

const natChain = "CARPLAYD"

// setupNAT masquerades every car prefix out of the uplink (Debian/SBC; on
// OpenWrt fw4's masq6 does this and nat66 stays "none").
func (d *daemon) setupNAT() error {
	if d.cfg.NAT66 == "none" {
		return nil
	}
	if err := writeSysctl("/proc/sys/net/ipv6/conf/all/forwarding", "1"); err != nil {
		return err
	}
	if d.cfg.NAT66 == "nft" {
		return nil // in the carplayd nftables table (applyFirewall)
	}
	d.teardownNAT()
	if err := run("ip6tables", "-t", "nat", "-N", natChain); err != nil {
		return err
	}
	if err := run("ip6tables", "-t", "nat", "-A", "POSTROUTING", "-j", natChain); err != nil {
		return err
	}
	for _, car := range d.cfg.Cars {
		if err := run("ip6tables", "-t", "nat", "-A", natChain, "-s", car.Prefix.String(), "-o", d.cfg.Uplink, "-j", "MASQUERADE"); err != nil {
			return err
		}
	}
	d.ev("nat66", d.cfg.Uplink)
	return nil
}

func (d *daemon) teardownNAT() {
	if d.cfg.NAT66 != "ip6tables" {
		return
	}
	run("ip6tables", "-t", "nat", "-D", "POSTROUTING", "-j", natChain)
	run("ip6tables", "-t", "nat", "-F", natChain)
	run("ip6tables", "-t", "nat", "-X", natChain)
}

func (d *daemon) killWPA() {
	pidf := filepath.Join(d.cfg.CtrlDir, "wpa.pid")
	if b, err := os.ReadFile(pidf); err == nil {
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(b))); pid > 0 {
			if p, err := os.FindProcess(pid); err == nil {
				p.Kill()
			}
		}
		os.Remove(pidf)
		time.Sleep(200 * time.Millisecond)
	}
}

// startWPA (re)starts our private wpa_supplicant and attaches to it.
func (d *daemon) startWPA() error {
	c := d.cfg
	if d.cmd != nil {
		d.cmd.close()
	}
	if d.evc != nil {
		d.evc.close()
	}
	conf := filepath.Join(c.CtrlDir, "wpa.conf")
	if err := os.WriteFile(conf, []byte(wpaConfig(c)), 0o600); err != nil {
		return err
	}
	d.killWPA()
	os.Remove(filepath.Join(c.CtrlDir, c.STA))
	if err := run(c.WPABinary, "-B", "-D", "nl80211", "-i", c.STA, "-c", conf, "-P", filepath.Join(c.CtrlDir, "wpa.pid")); err != nil {
		return err
	}
	sock := filepath.Join(c.CtrlDir, c.STA)
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	var err error
	if d.cmd, err = dialWPA(c.CtrlDir, c.STA); err != nil {
		return err
	}
	if d.evc, err = dialWPA(c.CtrlDir, c.STA); err != nil {
		return err
	}
	d.events = make(chan string, 64)
	d.missedPings = 0
	return d.evc.events(d.events)
}

// alive pings wpa_supplicant; a datagram socket doesn't notice a dead peer,
// so two missed PONGs mean restart it.
func (d *daemon) alive() bool {
	r, err := d.cmd.cmd("PING")
	if err == nil && strings.TrimSpace(r) == "PONG" {
		d.missedPings = 0
		return true
	}
	d.missedPings++
	return d.missedPings < 2
}

func (d *daemon) restartWPA(why string) {
	d.ev("wpa_restart", why)
	d.m.inc("wpa_restarts")
	d.stopServing(false)
	for i := 0; ; i++ {
		if err := d.startWPA(); err == nil {
			return
		} else if i%10 == 0 {
			d.ev("error", "wpa_start", err.Error())
		}
		time.Sleep(time.Second)
	}
}

// shutdown says goodbye, leaves the car, stops wpa_supplicant and removes NAT.
func (d *daemon) shutdown() {
	d.stopServing(true)
	if d.cmd != nil {
		d.cmd.ok("DISCONNECT")
		d.cmd.close()
	}
	if d.evc != nil {
		d.evc.close()
	}
	d.killWPA()
	d.teardownNAT()
}
func (d *daemon) carByID(id string) *Car {
	for i := range d.cfg.Cars {
		if d.cfg.Cars[i].ID == id {
			return &d.cfg.Cars[i]
		}
	}
	return nil
}

func (d *daemon) netID(id string) int {
	for i, c := range d.cfg.Cars {
		if c.ID == id {
			return i // networks are added in config order
		}
	}
	return -1
}

func (d *daemon) freqs() string { return huntFreqs(d.cfg.Cars) }

// tickFreqs is what to scan on hunt tick n: the known channels every tick, and
// all of 2.4 GHz every 5th tick if some car's channel is unknown (a full sweep
// takes seconds on real radios; it shouldn't slow down the cars we know).
func tickFreqs(cars []Car, n int) string {
	var known []Car
	unknown := false
	for _, c := range cars {
		if c.Freq() == 0 {
			unknown = true
		} else {
			known = append(known, c)
		}
	}
	if unknown && (n%5 == 0 || len(known) == 0) {
		return huntFreqs(cars)
	}
	return huntFreqs(known)
}

// huntFreqs lists the frequencies to scan: each car's channel, or all of
// 2.4 GHz (1-13) if any car's channel is unknown.
func huntFreqs(cars []Car) string {
	seen := map[int]bool{}
	var f []string
	for _, c := range cars {
		if c.Freq() == 0 {
			var all []string
			for ch := 1; ch <= 13; ch++ {
				all = append(all, strconv.Itoa(2407+5*ch))
			}
			return strings.Join(all, ",")
		}
		if !seen[c.Freq()] {
			seen[c.Freq()] = true
			f = append(f, strconv.Itoa(c.Freq()))
		}
	}
	return strings.Join(f, ",")
}

func (d *daemon) hunt() {
	if d.serving != nil {
		return
	}
	d.huntTick++
	d.cmd.cmd("SCAN freq=" + tickFreqs(d.cfg.Cars, d.huntTick)) // FAIL-BUSY just means a scan is already running
}

func (d *daemon) onScanResults() {
	r, err := d.cmd.cmd("SCAN_RESULTS")
	if err != nil {
		return
	}
	seen := parseScanResults(r)
	now := time.Now()
	// Every BSS in the scan, not just the configured cars: a car that is on
	// but unseen, or on a channel we do not hunt, looks identical to a car
	// that is off unless the whole scan is on record.
	if d.cfg.Debug {
		args := []string{"n=" + strconv.Itoa(len(seen))}
		for _, e := range seen {
			args = append(args, fmt.Sprintf("%s/%d/%d/%q", e.BSSID, e.Freq, e.Signal, e.SSID))
		}
		d.dbg("scan", args...)
	}
	for _, c := range d.cfg.Cars {
		if e, found := best(c, seen); found {
			if _, ok := d.firstSeen[c.ID]; !ok {
				d.firstSeen[c.ID] = now
				d.ev("seen", c.ID, strconv.Itoa(e.Freq), strconv.Itoa(e.Signal), e.BSSID)
				d.lastSurvey[c.ID] = now
			} else if surveyDue(d.lastSurvey[c.ID], now, d.cfg.Survey) {
				d.ev("sighting", c.ID, strconv.Itoa(e.Freq), strconv.Itoa(e.Signal), e.BSSID)
				d.lastSurvey[c.ID] = now
			}
		} else if d.serving == nil || c.ID != d.serving.ID {
			if t, ok := d.firstSeen[c.ID]; ok {
				d.ev("gone", c.ID, fmt.Sprintf("%.0fs", now.Sub(t).Seconds()))
			}
			delete(d.firstSeen, c.ID)
		}
	}
	if d.serving != nil {
		d.arbitrate(seen, now)
		return
	}
	releaseHolds(d.cfg.Cars, d.held, seen)
	for id := range d.relaxed {
		if !d.held[id] {
			delete(d.relaxed, id)
		}
	}
	car, e, ok := pick(relaxRejoin(d.cfg.Cars, d.relaxed), d.held, seen)
	if !ok {
		return
	}
	if err := d.cmd.ok(fmt.Sprintf("SELECT_NETWORK %d", d.netID(car.ID))); err == nil {
		d.ev("select", car.ID, strconv.Itoa(e.Signal), e.BSSID)
	}
}

// arbitrate runs while serving: switch to another car only per shouldSwitch.
func (d *daemon) arbitrate(seen []scanEntry, now time.Time) {
	var others []Car
	for _, c := range d.cfg.Cars {
		if c.ID != d.serving.ID {
			others = append(others, c)
		}
	}
	car, e, ok := pick(others, d.held, seen)
	if !ok {
		return
	}
	if !preempt(d.serving.Priority, car.Priority, d.servingSince, d.lastPhone, now, preemptGrace) &&
		!shouldSwitch(now, d.servingSince, d.lastPhone, d.cfg.IdleSwitch, d.firstSeen[car.ID]) {
		return
	}
	from := d.serving.ID
	d.ev("switch", from, car.ID, strconv.Itoa(e.Signal))
	d.m.inc("switches")
	d.stopServing(true)
	d.cmd.ok("DISCONNECT")
	if err := d.cmd.ok(fmt.Sprintf("SELECT_NETWORK %d", d.netID(car.ID))); err == nil {
		d.ev("select", car.ID, strconv.Itoa(e.Signal), e.BSSID)
	}
}

func (d *daemon) onConnected() {
	st, err := d.cmd.cmd("STATUS")
	if err != nil {
		return
	}
	kv := parseKV(st)
	car := d.carByID(kv["id_str"])
	if car == nil {
		d.ev("connected_unknown", kv["ssid"])
		return
	}
	// What was actually negotiated (the scan only shows what the car offers).
	d.ev("connected", car.ID, kv["bssid"], "freq="+kv["freq"], "key_mgmt="+kv["key_mgmt"],
		"pairwise="+kv["pairwise_cipher"], "group="+kv["group_cipher"], "wifi_generation="+kv["wifi_generation"])
	d.m.inc("joins")
	if t, ok := d.firstSeen[car.ID]; ok {
		d.m.set(func(m *metrics) { m.detect = time.Since(t).Seconds() })
	}
	d.startServing(car)
}

func (d *daemon) startServing(car *Car) {
	addr := car.Prefix.Addr().Next().String() + "/64" // <prefix>::1
	if err := run("ip", "-6", "addr", "replace", addr, "dev", d.cfg.STA, "nodad"); err != nil {
		d.ev("error", "addr", err.Error())
	}
	d.freshHealth()
	id := car.ID
	ra, err := newRASender(d.cfg.STA, raParams{
		RouterLife: d.cfg.RouterLife, Prefix: car.Prefix,
		ValidLife: 24 * time.Hour, PreferredLife: 30 * time.Minute,
		DNS: d.cfg.DNS, DNSLife: dnsLife(d.cfg),
	}, d.cfg.RAInterval, func(name string, args ...string) { d.ev(name, append([]string{id}, args...)...) })
	if err != nil {
		d.ev("error", "ra", err.Error())
		return
	}
	d.serving, d.ra, d.below, d.belowTrend, d.samples = car, ra, time.Time{}, time.Time{}, nil
	d.resetFail() // the new station's counters start from their own zero
	d.servingSince, d.lastPhone, d.phones = time.Now(), time.Time{}, 0
	d.m.set(func(m *metrics) { m.serving, m.phones = car.ID, 0 })
	ra.start(!d.up.ok)
	d.ev("serving", car.ID, car.Prefix.String(), map[bool]string{true: "route", false: "withdrawn"}[d.up.ok])
}

func (d *daemon) stopServing(goodbye bool) {
	if d.serving == nil {
		return
	}
	car := d.serving
	if goodbye && d.ra != nil {
		d.ra.goodbye(d.neighbours())
		d.ev("goodbye", car.ID)
		d.m.inc("goodbyes")
	}
	if d.ra != nil {
		d.ra.close()
	}
	run("ip", "-6", "addr", "del", car.Prefix.Addr().Next().String()+"/64", "dev", d.cfg.STA)
	d.serving, d.ra = nil, nil
	d.m.set(func(m *metrics) { m.serving, m.phones = "", 0 })
}

// readFail updates the tx_failed rate from a station dump. A missing counter
// leaves the rate untouched rather than reading as zero, so one failed `iw`
// call cannot clear a failing link's evidence.
func (d *daemon) readFail(link map[string]string, now time.Time) {
	v, ok := link["tx_failed"]
	if !ok {
		return
	}
	cur, err := strconv.Atoi(v)
	if err != nil {
		return
	}
	// A station that re-associated restarts its counters; start over rather
	// than read the step down as a negative rate.
	if n := len(d.failSamples); n > 0 && cur < d.failSamples[n-1].n {
		d.failSamples = nil
	}
	d.failSamples = append(trimFail(d.failSamples, now), failSample{now, cur})
	if r, ok := failRateOf(d.failSamples); ok {
		d.failPerSec = r
	}
}

// resetFail forgets the tx_failed history, so a new link's counters are never
// compared against the previous one's.
func (d *daemon) resetFail() {
	d.failSamples, d.failPerSec, d.belowFail = nil, 0, time.Time{}
}

func (d *daemon) poll() {
	if d.serving == nil {
		return
	}
	now := time.Now()
	r, err := d.cmd.cmd("SIGNAL_POLL")
	if err != nil {
		return
	}
	sig, err := strconv.Atoi(parseKV(r)["RSSI"])
	if err != nil {
		return
	}
	d.lastSig = sig
	if out, err := exec.Command("ip", "-6", "neigh", "show", "dev", d.cfg.STA).Output(); err == nil {
		n := phonesIn(string(out), d.serving.Prefix)
		d.phones = n
		if n > 0 {
			d.lastPhone = now
		}
		d.m.set(func(m *metrics) { m.signal, m.phones = sig, n })
		// The station dump feeds both the survey line and the failing-link
		// rule, so it is read every poll whenever either wants it.
		surveying := surveyDue(d.lastSurvey[d.serving.ID], now, d.cfg.Survey)
		var link map[string]string
		if surveying || d.serving.FailRate > 0 {
			if st, err := exec.Command("iw", "dev", d.cfg.STA, "station", "dump").Output(); err == nil {
				link = iwStation(string(st))
				if surveying {
					d.dbg("station", strings.Fields(strings.ReplaceAll(string(st), "\n", " "))...)
				}
			}
		}
		if surveying {
			args := []string{d.serving.ID, strconv.Itoa(sig), "phones=" + strconv.Itoa(n)}
			for _, k := range linkFields {
				if v, ok := link[k]; ok {
					args = append(args, k+"="+v)
				}
			}
			d.ev("signal", args...)
			d.lastSurvey[d.serving.ID] = now
		}
		d.readFail(link, now)
		d.dbg("neigh", d.serving.ID, strings.Join(strings.Fields(strings.ReplaceAll(string(out), "\n", " | ")), " "))
	}
	// Leave while the link is still usable: below the absolute threshold, or
	// (drop > 0) well below where the signal sat 5-20 s ago, i.e. driving off.
	// A box hanging on at the edge of range makes the head unit relay the
	// phone's traffic to it at the slowest rates, starving CarPlay itself.
	d.samples = append(trimSamples(d.samples, now), sample{now, sig})
	var leave, trend bool
	d.below, leave = leaveCheck(d.below, now, sig, d.serving.Leave, d.cfg.LeaveFor)
	base, haveBase := baselineOf(d.samples, now)
	if d.serving.Drop > 0 && haveBase {
		d.belowTrend, trend = leaveCheck(d.belowTrend, now, sig, base-d.serving.Drop, d.cfg.LeaveFor)
	}
	// Leave while the link is still alive enough to say goodbye. Neither signal
	// rule catches a departure where the RSSI stays flat and the link simply
	// dies -- measured on a real drive: 35 s at a steady -82 with the trend
	// baseline tracking it, then death, no goodbye, and a phone left holding a
	// dead route. tx_failed rising is the only advance warning, and it gave
	// about 8 s of it.
	var failing bool
	if d.serving.FailRate > 0 {
		d.belowFail, failing = sustain(d.belowFail, now, d.failPerSec >= d.serving.FailRate, d.cfg.LeaveFor)
	}
	// The whole leave decision, every poll: which threshold is in play, how
	// long the signal has sat under it, and what the trend baseline makes of
	// it. A departure that the box got wrong is otherwise only visible as the
	// single leave line that did or did not happen.
	trace := []string{d.serving.ID, strconv.Itoa(sig),
		"leave=" + strconv.Itoa(d.serving.Leave), "below_for=" + since(d.below, now),
		"drop=" + strconv.Itoa(d.serving.Drop), "samples=" + strconv.Itoa(len(d.samples))}
	if haveBase {
		trace = append(trace, "base="+strconv.Itoa(base),
			"trend_leave="+strconv.Itoa(base-d.serving.Drop), "trend_below_for="+since(d.belowTrend, now))
	} else {
		trace = append(trace, "base=none")
	}
	trace = append(trace, "leave_for="+d.cfg.LeaveFor.String(),
		fmt.Sprintf("fail=%d/s fail_rate=%d fail_for=%s", d.failPerSec, d.serving.FailRate, since(d.belowFail, now)),
		fmt.Sprintf("fire_threshold=%t fire_trend=%t fire_failing=%t", leave, trend, failing))
	d.dbg("decide", trace...)
	if leave || trend || failing {
		car := d.serving
		why := "threshold"
		switch {
		case leave:
		case trend:
			why = "trend base=" + strconv.Itoa(base)
		default:
			why = "failing tx_failed=" + strconv.Itoa(d.failPerSec) + "/s"
		}
		d.ev("leave", car.ID, strconv.Itoa(sig), why)
		d.m.inc("leaves")
		d.stopServing(true)
		d.held[car.ID] = true
		if trend && !leave {
			// Rejoin only near the parked level: the absolute rejoin threshold
			// may be weaker than where we just left, which would mean join,
			// leave, join while driving off. A dip while parked recovers there.
			d.relaxed[car.ID] = max(base-3, car.Join)
		}
		d.cmd.ok("DISCONNECT")
		d.cmd.ok("DISABLE_NETWORK all")
		return
	}
	// Look for other cars: every 2 s during the preemption grace period (if a
	// higher-priority car exists), every 5 s once the served car has had no
	// phone for idle_switch.
	if len(d.cfg.Cars) > 1 {
		quiet := d.servingSince
		if d.lastPhone.After(quiet) {
			quiet = d.lastPhone
		}
		graceScan := d.lastPhone.IsZero() && now.Sub(d.servingSince) < preemptGrace && d.higherPriorityExists() && now.Sub(d.lastArbScan) >= 2*time.Second
		idleScan := now.Sub(quiet) >= d.cfg.IdleSwitch && now.Sub(d.lastArbScan) >= 5*time.Second
		if graceScan || idleScan {
			d.lastArbScan = now
			d.cmd.cmd("SCAN freq=" + d.freqs())
		}
	}
}

func (d *daemon) loop(stop <-chan os.Signal) {
	hunt := time.NewTicker(d.cfg.Hunt)
	pollT := time.NewTicker(d.cfg.Poll)
	healthT := time.NewTicker(d.cfg.HealthEvery)
	d.ev("start", d.cfg.STA, d.freqs())
	d.hunt()
	for {
		select {
		case <-stop:
			d.ev("stop")
			d.shutdown()
			return
		case e, ok := <-d.events:
			if !ok {
				d.restartWPA("event socket closed")
				continue
			}
			d.logWPA(e)
			switch {
			case strings.HasPrefix(e, "CTRL-EVENT-SCAN-RESULTS"):
				d.onScanResults()
			case strings.HasPrefix(e, "CTRL-EVENT-CONNECTED"):
				d.onConnected()
			case strings.HasPrefix(e, "CTRL-EVENT-DISCONNECTED"):
				// reason= and locally_generated= say whether the head unit
				// kicked us or we walked away, which the event alone never did.
				kv := parseEventArgs(e)
				why := []string{"reason=" + kv["reason"], "why=" + reasonName(kv["reason"]),
					"locally_generated=" + orZero(kv["locally_generated"]), "bssid=" + kv["bssid"]}
				if d.serving != nil {
					d.ev("link_lost", append([]string{d.serving.ID, strconv.Itoa(d.lastSig)}, why...)...)
					d.m.inc("link_lost")
					d.stopServing(false)
				} else {
					d.ev("disconnected_idle", why...)
				}
				d.cmd.ok("DISABLE_NETWORK all") // carplayd decides what to join next
			case strings.HasPrefix(e, "CTRL-EVENT-TERMINATING"):
				d.restartWPA("wpa_supplicant terminating")
			}
		case <-hunt.C:
			if !d.alive() {
				d.restartWPA("no PONG")
				continue
			}
			d.hunt()
		case <-pollT.C:
			d.poll()
		case <-healthT.C:
			if d.cfg.Health && d.serving != nil && !d.checking {
				d.checking = true
				go func() { d.healthC <- checkUpstream(d.cfg.HealthTargets, healthTimeout) }()
			}
			d.refreshFirewall()
		case r := <-d.healthC:
			d.checking = false
			d.onHealth(r)
		}
	}
}

const preemptGrace = 15 * time.Second

func (d *daemon) higherPriorityExists() bool {
	for _, c := range d.cfg.Cars {
		if c.Priority > d.serving.Priority {
			return true
		}
	}
	return false
}
