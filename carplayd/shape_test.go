package main

import (
	"errors"
	"strings"
	"testing"
)

// recorder captures the tc command lines applyShaping would run.
type recorder struct {
	calls  [][]string
	failOn string
}

func (r *recorder) run(name string, args ...string) error {
	r.calls = append(r.calls, append([]string{name}, args...))
	if r.failOn != "" && len(args) > 0 && strings.Contains(strings.Join(args, " "), r.failOn) {
		return errors.New("boom")
	}
	return nil
}

func (r *recorder) line(i int) string { return strings.Join(r.calls[i], " ") }

func TestApplyShapingBuildsTheHTBChain(t *testing.T) {
	r := &recorder{}
	if err := applyShaping("carb", "2mbit", r.run); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tc qdisc del dev carb root",
		"tc qdisc add dev carb root handle 1: htb default 10",
		"tc class add dev carb parent 1: classid 1:10 htb rate 2mbit ceil 2mbit",
		"tc qdisc add dev carb parent 1:10 handle 10: pfifo limit 16",
	}
	if len(r.calls) != len(want) {
		t.Fatalf("ran %d commands, want %d: %v", len(r.calls), len(want), r.calls)
	}
	for i, w := range want {
		if got := r.line(i); got != w {
			t.Errorf("step %d:\n got %q\nwant %q", i, got, w)
		}
	}
}

func TestApplyShapingDeletesFirstSoARejoinDoesNotStack(t *testing.T) {
	// A car that leaves and comes back must end up with one cap, not two.
	r := &recorder{}
	for i := 0; i < 3; i++ {
		if err := applyShaping("carb", "2mbit", r.run); err != nil {
			t.Fatal(err)
		}
	}
	dels := 0
	for _, c := range r.calls {
		if strings.Contains(strings.Join(c, " "), "qdisc del") {
			dels++
		}
	}
	if dels != 3 {
		t.Fatalf("want a del before each add, got %d dels in %d calls", dels, len(r.calls))
	}
	if got := r.line(0); !strings.Contains(got, "del") {
		t.Fatalf("the first command must be the del: %q", got)
	}
}

func TestNoRateMeansNoCommands(t *testing.T) {
	// The default must not silently throttle an unmeasured car.
	r := &recorder{}
	if err := applyShaping("cara", "", r.run); err != nil {
		t.Fatal(err)
	}
	clearShaping("cara", "", r.run)
	if len(r.calls) != 0 {
		t.Fatalf("unshaped link touched tc: %v", r.calls)
	}
}

func TestShapingFailureIsReportedNotSwallowed(t *testing.T) {
	// A box that cannot shape should still serve the car, but the daemon has to
	// be told, or a silently-missing cap looks exactly like a working one.
	for _, stage := range []string{"htb default", "classid", "pfifo"} {
		r := &recorder{failOn: stage}
		err := applyShaping("carb", "2mbit", r.run)
		if err == nil {
			t.Errorf("failure at %q was swallowed", stage)
			continue
		}
		if !strings.Contains(err.Error(), "carb") {
			t.Errorf("%q: error should name the interface: %v", stage, err)
		}
	}
}

func TestDeleteFailureIsNotAnError(t *testing.T) {
	// There may be no qdisc yet on a fresh interface.
	r := &recorder{failOn: "qdisc del"}
	if err := applyShaping("carb", "2mbit", r.run); err != nil {
		t.Fatalf("a failing del must not abort setup: %v", err)
	}
}

func TestClearShapingRemovesTheRoot(t *testing.T) {
	r := &recorder{}
	clearShaping("carb", "2mbit", r.run)
	if len(r.calls) != 1 || r.line(0) != "tc qdisc del dev carb root" {
		t.Fatalf("got %v", r.calls)
	}
}

func TestNormalizeRate(t *testing.T) {
	ok := map[string]string{
		"2mbit": "2mbit", "2Mbit": "2mbit", " 2mbit ": "2mbit",
		"1.5mbit": "1.5mbit", "512kbit": "512kbit", "1gbit": "1gbit", "900bit": "900bit",
		"": "", "0": "", "off": "", "none": "",
	}
	for in, want := range ok {
		got, err := normalizeRate(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"2mbps", "2MB/s", "2", "fast", "-2mbit", "0mbit", "mbit", "2 mbit per second"} {
		if got, err := normalizeRate(bad); err == nil {
			t.Errorf("%q should be rejected, got %q", bad, got)
		}
	}
}

func TestNormalizeRateRejectsByteUnitsExplicitly(t *testing.T) {
	// "2mbps" means bytes to tc, so accepting it would cap at 16 Mbit -- eight
	// times the intended rate, which is worse than no cap because it would look
	// configured while failing to protect anything.
	_, err := normalizeRate("2mbps")
	if err == nil || !strings.Contains(err.Error(), "8x") {
		t.Fatalf("the error must explain the factor of eight: %v", err)
	}
}

func TestParseShapeStats(t *testing.T) {
	// Real output from the F22 session on 2026-11-16.
	const out = `class htb 1:10 root leaf 10: prio 0 rate 2Mbit ceil 2Mbit burst 1600b cburst 1600b
 Sent 13244994 bytes 12338 pkt (dropped 611, overlimits 7928 requeues 0)
 backlog 0b 0p requeues 0
 lended: 10760 borrowed: 0 giants: 0
 tokens: 85937 ctokens: 85937`
	st := parseShapeStats(out)
	if st.Dropped != 611 || st.Overlimits != 7928 || st.BacklogPkt != 0 {
		t.Fatalf("got %+v", st)
	}
}

func TestParseShapeStatsReadsABacklog(t *testing.T) {
	const out = ` Sent 100 bytes 2 pkt (dropped 0, overlimits 0 requeues 0)
 backlog 24000b 16p requeues 0`
	st := parseShapeStats(out)
	if st.BacklogPkt != 16 {
		t.Fatalf("backlog packets = %d, want 16 (from %q)", st.BacklogPkt, "16p")
	}
}

func TestParseShapeStatsSurvivesJunk(t *testing.T) {
	// A different tc version, or an error on stdout, must not panic or invent
	// numbers -- it should just make the log less useful.
	for _, in := range []string{"", "Object \"class\" is unknown", "dropped", "backlog", "backlog 0b"} {
		st := parseShapeStats(in)
		if st != (shapeStats{}) {
			t.Errorf("%q -> %+v, want zero", in, st)
		}
	}
}
