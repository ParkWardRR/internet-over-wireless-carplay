package main

import (
	"os"
	"strings"
	"testing"
)

// Replaying a real capture through the parser, to check that what it reports
// matches what the capture actually contains.
//
// This exists because the two disagreed. On the 2026-11-11 F32 departure the
// events log showed about fifty disassoc lines from the box inside three
// milliseconds, each with a slightly different RSSI, while tshark finds exactly
// one disassoc frame and four deauths in the same capture. One of the two is
// wrong, and a frame count is not something the live parser may invent: the
// whole airtime argument rests on it.
//
// The capture holds the owner's phone traffic, so it is not in git. Point the
// test at one:
//
//	CARRECON_PCAP=/path/to.pcap CARRECON_CARS=bc:30:... CARRECON_BOX=54:78:... go test -run Replay -v
func TestReplayCountsFramesOnce(t *testing.T) {
	path := os.Getenv("CARRECON_PCAP")
	if path == "" {
		t.Skip("set CARRECON_PCAP (and CARRECON_CARS, CARRECON_BOX) to replay a real capture")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	ev, err := os.CreateTemp(t.TempDir(), "ev")
	if err != nil {
		t.Fatal(err)
	}
	r := &recon{
		cars: map[string]bool{}, box: strings.ToLower(os.Getenv("CARRECON_BOX")),
		stats: map[string]*stat{}, beacons: map[string]*stat{},
		stream: map[string]bool{}, quiet: map[string]int{}, ev: ev,
	}
	for _, c := range strings.Split(strings.ToLower(os.Getenv("CARRECON_CARS")), ",") {
		if c = strings.TrimSpace(c); c != "" {
			r.cars[c] = true
		}
	}
	if len(r.cars) == 0 {
		t.Fatal("CARRECON_CARS is required")
	}

	// readPcap returns the error that ended the stream; EOF is success here.
	_ = r.readPcap(f)
	r.flush(r.sec)

	b, _ := os.ReadFile(ev.Name())
	counts := map[string]int{}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) > 1 {
			counts[f[1]]++
		}
	}
	t.Logf("frames parsed: %d", r.frames.Load())
	for _, k := range []string{"deauth", "disassoc", "assoc_req", "assoc_resp", "eapol", "beacon", "sec", "stream_start", "stream_stop"} {
		t.Logf("  %-12s %d", k, counts[k])
	}
	// The counts the test can assert without the capture in hand: the parser
	// must not report a management frame more than once per frame it read.
	mgmt := counts["deauth"] + counts["disassoc"] + counts["assoc_req"] + counts["assoc_resp"]
	if got := uint64(mgmt); got > r.frames.Load() {
		t.Errorf("reported %d management events from %d frames", got, r.frames.Load())
	}
	if want := os.Getenv("CARRECON_WANT_DISASSOC"); want != "" {
		if got := counts["disassoc"]; itoaEq(got, want) == false {
			t.Errorf("disassoc: parser says %d, capture says %s", got, want)
		}
	}
}

func itoaEq(got int, want string) bool {
	var n int
	for _, c := range want {
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(c-'0')
	}
	return n == got
}
