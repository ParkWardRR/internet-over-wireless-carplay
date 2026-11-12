package main

import "testing"

// A deaf radio is the only thing that makes the car channel look empty, so a
// run of zero-BSS scans has to trigger recovery -- but only a long run, or an
// ordinary scan that happened to race the radio would restart the link.
func TestScanWatchDetectsADeafRadio(t *testing.T) {
	var w scanWatch
	for i := 1; i < staScanStall; i++ {
		if w.observe(0) {
			t.Fatalf("fired after only %d empty scans, want %d", i, staScanStall)
		}
	}
	if !w.observe(0) {
		t.Fatalf("did not fire after %d empty scans", staScanStall)
	}
	if w.stalls != 1 {
		t.Errorf("stall count %d, want 1", w.stalls)
	}
}

// Hearing anything at all clears the count, including a scan with no car in
// it: the question is whether the radio works, not whether a car is home.
func TestScanWatchResetsOnAnyResult(t *testing.T) {
	var w scanWatch
	for i := 0; i < staScanStall-1; i++ {
		w.observe(0)
	}
	w.observe(3) // neighbours, no cars
	if w.empty != 0 {
		t.Fatalf("a non-empty scan left %d empty scans counted", w.empty)
	}
	for i := 1; i < staScanStall; i++ {
		if w.observe(0) {
			t.Fatalf("fired after %d empty scans following a reset", i)
		}
	}
}

// Repeat stalls escalate; a recovery that actually worked clears the history
// so the next stall starts again at the gentler step.
func TestScanWatchEscalatesThenForgets(t *testing.T) {
	var w scanWatch
	stall := func() {
		for i := 0; i < staScanStall; i++ {
			w.observe(0)
		}
	}
	stall()
	if w.stalls >= staRecreateAfter {
		t.Fatalf("one stall already escalated (stalls=%d, threshold=%d)", w.stalls, staRecreateAfter)
	}
	stall()
	if w.stalls < staRecreateAfter {
		t.Errorf("two stalls did not reach the escalation threshold: %d", w.stalls)
	}
	w.recovered()
	if w.stalls != 0 || w.empty != 0 {
		t.Errorf("recovery did not clear the history: stalls=%d empty=%d", w.stalls, w.empty)
	}
}
