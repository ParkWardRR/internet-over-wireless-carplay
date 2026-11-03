package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type daemon struct {
	cfg     Config
	cmd     *wpaConn // requests
	evc     *wpaConn // attached for events
	events  chan string
	held    map[string]bool
	serving *Car
	ra      *raSender
	below   time.Time
	lastSig int
	evFile  *os.File
}

// ev prints "EV <boottime> carplayd_<name> ..." (the harness's format) and
// appends it to the events file.
func (d *daemon) ev(name string, args ...string) {
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

// setup creates our STA, tunes it, and starts a private wpa_supplicant.
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

	if err := os.MkdirAll(c.CtrlDir, 0o700); err != nil {
		return err
	}
	conf := filepath.Join(c.CtrlDir, "wpa.conf")
	if err := os.WriteFile(conf, []byte(wpaConfig(c)), 0o600); err != nil {
		return err
	}
	pidf := filepath.Join(c.CtrlDir, "wpa.pid")
	if b, err := os.ReadFile(pidf); err == nil {
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(b))); pid > 0 {
			if p, err := os.FindProcess(pid); err == nil {
				p.Kill()
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	os.Remove(filepath.Join(c.CtrlDir, c.STA))
	if err := run(c.WPABinary, "-B", "-D", "nl80211", "-i", c.STA, "-c", conf, "-P", pidf); err != nil {
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
	return d.evc.events(d.events)
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
	if d.serving != nil {
		return
	}
	r, err := d.cmd.cmd("SCAN_RESULTS")
	if err != nil {
		return
	}
	seen := parseScanResults(r)
	releaseHolds(d.cfg.Cars, d.held, seen)
	car, e, ok := pick(d.cfg.Cars, d.held, seen)
	if !ok {
		return
	}
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
	}
	if d.ra != nil {
		d.ra.close()
	}
	run("ip", "-6", "addr", "del", car.Prefix.Addr().Next().String()+"/64", "dev", d.cfg.STA)
	d.serving, d.ra = nil, nil
}

func (d *daemon) poll() {
	if d.serving == nil {
		return
	}
	r, err := d.cmd.cmd("SIGNAL_POLL")
	if err != nil {
		return
	}
	sig, err := strconv.Atoi(parseKV(r)["RSSI"])
	if err != nil {
		return
	}
	d.lastSig = sig
	var leave bool
	d.below, leave = leaveCheck(d.below, time.Now(), sig, d.serving.Leave, d.cfg.LeaveFor)
	if leave {
		car := d.serving
		d.ev("leave", car.ID, strconv.Itoa(sig))
		d.stopServing(true)
		d.held[car.ID] = true
		d.cmd.ok("DISCONNECT")
		d.cmd.ok("DISABLE_NETWORK all")
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
			d.stopServing(true)
			return
		case e, ok := <-d.events:
			if !ok {
				d.ev("error", "wpa_supplicant went away")
				return
			}
			switch {
			case strings.HasPrefix(e, "CTRL-EVENT-SCAN-RESULTS"):
				d.onScanResults()
			case strings.HasPrefix(e, "CTRL-EVENT-CONNECTED"):
				d.onConnected()
			case strings.HasPrefix(e, "CTRL-EVENT-DISCONNECTED"):
				if d.serving != nil {
					d.ev("link_lost", d.serving.ID)
					d.stopServing(false)
				}
				d.cmd.ok("DISABLE_NETWORK all") // carplayd decides what to join next
			}
		case <-hunt.C:
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
