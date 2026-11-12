package main

import "fmt"

// Radio stalls.
//
// The AR9271 is documented to stop receiving while the interface stays up and
// the driver reports no error (qca/open-ath9k-htc-firmware#126, measured worse
// on busy channels). wpa_supplicant keeps answering PING, so `alive` cannot see
// it; the link just never finds anything again.
//
// The signal that separates a deaf radio from an absent car is that the car
// channel is never empty. Channel 11 here carries a handful of neighbourhood
// APs at all times -- a scan that returns *zero* BSSes, repeatedly, is the
// radio, because even with every car switched off the neighbours are there.

const (
	// staScanStall: consecutive empty scans that mean the radio is deaf. Scans
	// run once per hunt tick, so this is that many seconds of total silence.
	staScanStall = 30

	// staRecreateAfter: stalls in a row before the STA netdev is destroyed and
	// rebuilt, rather than just restarting wpa_supplicant on it.
	staRecreateAfter = 2
)

// scanWatch counts empty scans and decides when the radio has stalled.
type scanWatch struct {
	empty  int // consecutive scans that saw nothing at all
	stalls int // stalls reported since the last good scan
}

// observe records one scan's BSS count and reports whether to recover. Any
// result at all clears the count, including one that holds no car: the point is
// whether the radio hears anything.
func (w *scanWatch) observe(bsses int) bool {
	if bsses > 0 {
		w.empty = 0
		return false
	}
	w.empty++
	if w.empty >= staScanStall {
		w.empty = 0
		w.stalls++
		return true
	}
	return false
}

// recovered is called once the radio proves it works again.
func (w *scanWatch) recovered() {
	w.empty, w.stalls = 0, 0
}

// recoverRadio brings a deaf radio back. It restarts wpa_supplicant first, and
// on a repeat stall destroys and rebuilds the STA netdev on this phy.
//
// It deliberately does NOT reload ath9k_htc. The module is shared by every
// AR9271 on the box, so reloading it to fix one car's dongle would take down
// every other car's link at the same time -- the opposite of what a per-radio
// recovery is for. carrecon can reload the driver because it owns the only
// adapter it uses; carplayd cannot.
func (d *daemon) recoverRadio() {
	d.linkEv("stall", fmt.Sprintf("empty_scans>=%d consecutive=%d", staScanStall, d.scans.stalls))
	d.m.inc("radio_stalls")
	if d.scans.stalls >= staRecreateAfter {
		d.linkEv("stall_recover", "recreate="+d.cfg.STA)
		d.stopServing(false)
		d.killWPA()
		// Dropping the netdev makes setup's "add the STA if absent" path
		// rebuild it on the next start, which is the deepest reset available
		// without disturbing the other radios.
		if err := run("iw", "dev", d.cfg.STA, "del"); err != nil {
			d.linkEv("error", "sta_del", err.Error())
		}
		if err := d.setupSTA(); err != nil {
			d.linkEv("error", "sta_add", err.Error())
		}
	}
	d.restartWPA("radio stalled")
}
