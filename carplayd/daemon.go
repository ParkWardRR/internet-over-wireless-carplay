package main

import (
	"fmt"
	"net"
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
	evFile       *os.File
}

// ev prints "EV <boottime> carplayd_<name> ..." (the harness's format) and
// appends it to the events file.
func (d *daemon) ev(name string, args ...string) {
	d.evMu.Lock()
	defer d.evMu.Unlock()
	l := fmt.Sprintf("EV %.3f carplayd_%s %s", boottime(), name, strings.Join(args, " "))
	l = strings.TrimSpace(l)
	fmt.Println(l)
	if d.evFile != nil {
		d.evFile.WriteString(l + "\n")
	}
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
	if err := os.MkdirAll(c.CtrlDir, 0o700); err != nil {
		return err
	}
	return d.startWPA()
}

const natChain = "CARPLAYD"

// setupNAT masquerades every car prefix out of the uplink (Debian/SBC; on
// OpenWrt fw4's masq6 does this and nat66 stays "none").
func (d *daemon) setupNAT() error {
	if d.cfg.NAT66 != "ip6tables" {
		return nil
	}
	if err := writeSysctl("/proc/sys/net/ipv6/conf/all/forwarding", "1"); err != nil {
		return err
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

func (d *daemon) freqs() string {
	seen := map[int]bool{}
	var f []string
	for _, c := range d.cfg.Cars {
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
	d.cmd.cmd("SCAN freq=" + d.freqs()) // FAIL-BUSY just means a scan is already running
}

func (d *daemon) onScanResults() {
	r, err := d.cmd.cmd("SCAN_RESULTS")
	if err != nil {
		return
	}
	seen := parseScanResults(r)
	now := time.Now()
	for _, c := range d.cfg.Cars {
		if _, found := best(c, seen); found {
			if _, ok := d.firstSeen[c.ID]; !ok {
				d.firstSeen[c.ID] = now
				d.ev("seen", c.ID)
			}
		} else if d.serving == nil || c.ID != d.serving.ID {
			delete(d.firstSeen, c.ID)
		}
	}
	if d.serving != nil {
		d.arbitrate(seen, now)
		return
	}
	releaseHolds(d.cfg.Cars, d.held, seen)
	car, e, ok := pick(d.cfg.Cars, d.held, seen)
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
	if !ok || !shouldSwitch(now, d.servingSince, d.lastPhone, d.cfg.IdleSwitch, d.firstSeen[car.ID]) {
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
	d.ev("connected", car.ID, kv["bssid"])
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
	ra, err := newRASender(d.cfg.STA, raParams{
		RouterLife: d.cfg.RouterLife, Prefix: car.Prefix,
		ValidLife: 24 * time.Hour, PreferredLife: 30 * time.Minute,
		DNS: d.cfg.DNS, DNSLife: 3 * d.cfg.RAInterval,
	})
	if err != nil {
		d.ev("error", "ra", err.Error())
		return
	}
	d.serving, d.ra, d.below = car, ra, time.Time{}
	d.servingSince, d.lastPhone = time.Now(), time.Time{}
	d.m.set(func(m *metrics) { m.serving, m.phones = car.ID, 0 })
	go ra.serveRS(func(src net.IP) { d.ev("rs", car.ID, src.String()) })
	if err := ra.advertise(); err != nil {
		d.ev("error", "ra_send", err.Error())
	}
	d.ev("serving", car.ID, car.Prefix.String())
}

func (d *daemon) stopServing(goodbye bool) {
	if d.serving == nil {
		return
	}
	car := d.serving
	if goodbye && d.ra != nil {
		d.ra.goodbye()
		time.Sleep(100 * time.Millisecond)
		d.ra.goodbye() // twice: one can be lost on a weak link
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
		if n > 0 {
			d.lastPhone = now
		}
		d.m.set(func(m *metrics) { m.signal, m.phones = sig, n })
	}
	var leave bool
	d.below, leave = leaveCheck(d.below, now, sig, d.serving.Leave, d.cfg.LeaveFor)
	if leave {
		car := d.serving
		d.ev("leave", car.ID, strconv.Itoa(sig))
		d.m.inc("leaves")
		d.stopServing(true)
		d.held[car.ID] = true
		d.cmd.ok("DISCONNECT")
		d.cmd.ok("DISABLE_NETWORK all")
		return
	}
	// With no phone on the served car for a while, look (every 5 s) for
	// another car someone may be about to drive.
	if len(d.cfg.Cars) > 1 && now.Sub(d.lastArbScan) >= 5*time.Second {
		quiet := d.servingSince
		if d.lastPhone.After(quiet) {
			quiet = d.lastPhone
		}
		if now.Sub(quiet) >= d.cfg.IdleSwitch {
			d.lastArbScan = now
			d.cmd.cmd("SCAN freq=" + d.freqs())
		}
	}
}

func (d *daemon) loop(stop <-chan os.Signal) {
	hunt := time.NewTicker(d.cfg.Hunt)
	pollT := time.NewTicker(d.cfg.Poll)
	raT := time.NewTicker(d.cfg.RAInterval)
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
			switch {
			case strings.HasPrefix(e, "CTRL-EVENT-SCAN-RESULTS"):
				d.onScanResults()
			case strings.HasPrefix(e, "CTRL-EVENT-CONNECTED"):
				d.onConnected()
			case strings.HasPrefix(e, "CTRL-EVENT-DISCONNECTED"):
				if d.serving != nil {
					d.ev("link_lost", d.serving.ID)
					d.m.inc("link_lost")
					d.stopServing(false)
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
		case <-raT.C:
			if d.ra != nil {
				d.ra.advertise()
			}
		}
	}
}
