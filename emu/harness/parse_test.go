package main

import "testing"

// carplayd stamps every event line with the wall clock so its log can be lined
// up against the in-car recorder; emu.sh and emuagent print a bare
// "EV <boottime> ...". The harness has to read both, and a format it silently
// drops is worse than one it rejects: the scenario still runs and simply never
// sees carplayd.
func TestParseEVBothFormats(t *testing.T) {
	in := "EV 12.5 emu_car_on A\n" +
		"2026-11-11T15:23:02.886-08:00 EV 745353.580 carplayd_leave A -83 trend\n" +
		"$ a command echoed into the log\n" +
		"tcpdump: listening on mon0\n"
	evs := parseEV(in)
	if len(evs) != 2 {
		t.Fatalf("want 2 events, got %d: %+v", len(evs), evs)
	}
	if evs[0].Name != "emu_car_on" || evs[0].T != 12.5 {
		t.Errorf("bare EV: got name %q t %v", evs[0].Name, evs[0].T)
	}
	if evs[1].Name != "carplayd_leave" || evs[1].T != 745353.580 {
		t.Errorf("stamped EV: got name %q t %v", evs[1].Name, evs[1].T)
	}
	if got := evs[1].Args; len(got) != 3 || got[0] != "A" || got[1] != "-83" {
		t.Errorf("stamped EV args: %+v", got)
	}
}
