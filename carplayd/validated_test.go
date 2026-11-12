package main

import (
	"strings"
	"testing"
	"time"
)

// The counters must exist, be declared before the chain that uses them, and
// count without a verdict of their own -- a verdict there would change what the
// firewall does, which is not what a measurement is allowed to do.
func TestRulesetCountsThePhoneValidationProbe(t *testing.T) {
	cfg := mustConfig(t, sampleConf)
	cfg.Firewall, cfg.NAT66, cfg.Uplink = "nft", "nft", "eth0"
	rs := nftRuleset(cfg, nil)

	for _, c := range cfg.Cars {
		id := counterID(c.ID)
		if !strings.Contains(rs, "counter probe_"+id+" {}") {
			t.Errorf("no probe counter declared for car %s", c.ID)
		}
		if !strings.Contains(rs, `counter name "probe_`+id+`"`) {
			t.Errorf("probe counter for car %s is declared but never used", c.ID)
		}
		if !strings.Contains(rs, `counter name "fwd_`+id+`"`) {
			t.Errorf("no forwarded-bytes counter in use for car %s", c.ID)
		}
		// Declaration before use: nft rejects a reference to an unknown counter.
		if strings.Index(rs, "counter probe_"+id+" {}") > strings.Index(rs, `counter name "probe_`+id+`"`) {
			t.Errorf("car %s: counter is used before it is declared", c.ID)
		}
	}
	// The counting rules carry no verdict, or they would override the isolation
	// rules that follow them.
	for _, l := range strings.Split(rs, "\n") {
		if !strings.Contains(l, "counter name") {
			continue
		}
		for _, verdict := range []string{"accept", "drop", "reject", "masquerade"} {
			if strings.Contains(l, verdict) {
				t.Errorf("counting rule has a %s verdict, which changes the firewall: %s", verdict, strings.TrimSpace(l))
			}
		}
	}
	// The probe rule must match the phone's traffic, not ours: source is the
	// car prefix, inbound on the car link.
	for _, l := range strings.Split(rs, "\n") {
		if strings.Contains(l, `counter name "probe_`) {
			if !strings.Contains(l, "ip6 saddr") || !strings.Contains(l, "iifname") || !strings.Contains(l, "tcp dport 80") {
				t.Errorf("probe rule does not match plain HTTP from the car prefix: %s", strings.TrimSpace(l))
			}
		}
	}
}

// A car ID is free-form in the config file; an nftables identifier is not.
func TestCounterIDIsASafeIdentifier(t *testing.T) {
	for in, want := range map[string]string{
		"A": "A", "F32": "F32", "the blue one": "the_blue_one",
		"a-b.c": "a_b_c", "": "unnamed", "x\"; drop table inet carplayd; #": "x___drop_table_inet_carplayd___",
	} {
		if got := counterID(in); got != want {
			t.Errorf("counterID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseNftCounters(t *testing.T) {
	const out = `{"nftables":[{"metainfo":{"version":"1.0.6"}},
	  {"counter":{"family":"inet","name":"probe_A","table":"carplayd","handle":1,"packets":7,"bytes":812}},
	  {"counter":{"family":"inet","name":"fwd_A","table":"carplayd","handle":2,"packets":900,"bytes":1360000}}]}`
	got := parseNftCounters([]byte(out))
	if got["probe_A"].Packets != 7 || got["probe_A"].Bytes != 812 {
		t.Errorf("probe_A = %+v", got["probe_A"])
	}
	if got["fwd_A"].Bytes != 1360000 {
		t.Errorf("fwd_A = %+v", got["fwd_A"])
	}
	// Garbage must not panic or invent counters: nft's output format is not
	// ours, and a version that changes it should degrade to "unknown".
	for _, bad := range []string{"", "{", "not json at all", `{"nftables":[]}`, `{"nftables":[{"rule":{}}]}`} {
		if g := parseNftCounters([]byte(bad)); len(g) != 0 {
			t.Errorf("garbage %q produced counters: %+v", bad, g)
		}
	}
}

// validator wires a daemon to a fake counter source and returns the events it
// writes, so the real decision path runs with no nftables and no car.
func validator(t *testing.T, probe *int64) (*daemon, func() []string) {
	t.Helper()
	cfg := mustConfig(t, sampleConf)
	cfg.Firewall, cfg.Debug = "nft", true
	car := cfg.Cars[0]
	d := &daemon{cfg: cfg, serving: &car, m: newMetrics()}
	d.counters = func() map[string]nftCounter {
		id := counterID(car.ID)
		return map[string]nftCounter{
			"probe_" + id: {Packets: *probe, Bytes: *probe * 116},
			"fwd_" + id:   {Packets: 900, Bytes: 1360000},
		}
	}
	d.resetValidation()
	return d, evLines(t, d)
}

// The 2026-11-11 trip in one test. A phone sits on our prefix pushing real
// bytes, iOS never validates the network, apps never use it, and nothing in any
// log said so. Now it does.
func TestUnvalidatedPhoneIsReported(t *testing.T) {
	var probe int64
	d, read := validator(t, &probe)
	start := time.Now()

	// Inside the grace period: no complaint yet, a slow probe is normal.
	d.checkValidated(1, start)
	d.checkValidated(1, start.Add(validateGrace-time.Second))
	if n := count(read(), "phone_unvalidated"); n != 0 {
		t.Errorf("warned inside the %s grace period (%d times)", validateGrace, n)
	}
	// Past it, with the phone still holding the prefix and still no probe.
	d.checkValidated(1, start.Add(validateGrace+time.Second))
	if n := count(read(), "phone_unvalidated"); n != 1 {
		t.Fatalf("want exactly one phone_unvalidated, got %d", n)
	}
	// And only once: this is polled every second for as long as the car is
	// parked, which would otherwise be a warning per second all night.
	for i := 0; i < 30; i++ {
		d.checkValidated(1, start.Add(validateGrace+time.Duration(2+i)*time.Second))
	}
	if n := count(read(), "phone_unvalidated"); n != 1 {
		t.Errorf("warning repeated: %d events", n)
	}
	// The owner taps Check For Internet Access: iOS probes, and the box says so.
	probe = 3
	d.checkValidated(1, start.Add(2*validateGrace))
	if n := count(read(), "carplayd_validated"); n != 1 {
		t.Errorf("want one validated event after the probe, got %d", n)
	}
}

// A probe from an earlier session must not count: the nftables counters are
// cumulative for the table's lifetime, while iOS re-evaluates per association.
// Without a per-session baseline the box would report every later session as
// validated and never warn again.
func TestValidationBaselineIsPerSession(t *testing.T) {
	probe := int64(7) // left over from hours ago
	d, read := validator(t, &probe)
	start := time.Now()

	for i := 0; i <= 25; i++ {
		d.checkValidated(1, start.Add(time.Duration(i)*time.Second))
	}
	if n := count(read(), "carplayd_validated"); n != 0 {
		t.Errorf("an old probe counted as this session's: %d validated events", n)
	}
	if n := count(read(), "phone_unvalidated"); n != 1 {
		t.Errorf("want the warning despite the stale counter, got %d events", n)
	}
	// A real probe now, on top of the stale 7, must register.
	probe = 8
	d.checkValidated(1, start.Add(30*time.Second))
	if n := count(read(), "carplayd_validated"); n != 1 {
		t.Errorf("a new probe above the baseline was missed: %d events", n)
	}
}

// No phone means nothing to validate: the grace period must not run down while
// the car is parked with the owner's phone elsewhere, or the warning would fire
// the instant they got in.
func TestNoPhoneDoesNotStartTheClock(t *testing.T) {
	var probe int64
	d, read := validator(t, &probe)
	start := time.Now()

	for i := 0; i < 60; i++ {
		d.checkValidated(0, start.Add(time.Duration(i)*time.Second))
	}
	d.checkValidated(1, start.Add(60*time.Second)) // they get in
	d.checkValidated(1, start.Add(70*time.Second)) // 10 s later, still in grace
	if n := count(read(), "phone_unvalidated"); n != 0 {
		t.Errorf("warned %d times though the phone had been present for 10s", n)
	}
}

func count(lines []string, needle string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, needle) {
			n++
		}
	}
	return n
}
