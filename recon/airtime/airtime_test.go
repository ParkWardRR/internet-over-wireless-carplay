package main

import (
	"encoding/binary"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

// The whole tool rests on these numbers. A wrong preamble or a wrong bits-per-
// symbol silently rescales every airtime figure, and nothing downstream would
// look obviously wrong -- so the known-good values are pinned here.

func TestHTAirtimeAgainstStandardValues(t *testing.T) {
	// HT20, 1 spatial stream, long GI. Hand-computed from 802.11:
	// nsym = ceil((22 + 8*len) / NDBPS), airtime = 36 + nsym*4.
	cases := []struct {
		mcs, n int
		want   float64
	}{
		{7, 1500, 36 + math.Ceil((22+12000)/260.0)*4},
		{0, 1500, 36 + math.Ceil((22+12000)/26.0)*4},
		{7, 64, 36 + math.Ceil((22+512)/260.0)*4},
		{3, 500, 36 + math.Ceil((22+4000)/104.0)*4},
	}
	for _, c := range cases {
		got := frameUS(c.n, rateInfo{HT: true, MCS: c.mcs})
		if got != c.want {
			t.Errorf("MCS %d, %d bytes: got %.1f us, want %.1f", c.mcs, c.n, got, c.want)
		}
	}
}

// The reason airtime and not bytes: the same frame at the slowest and fastest
// rate this link uses differs by about an order of magnitude.
func TestSlowFramesCostFarMoreAirtime(t *testing.T) {
	fast := frameUS(1500, rateInfo{HT: true, MCS: 7})
	slow := frameUS(1500, rateInfo{HT: true, MCS: 0})
	if ratio := slow / fast; ratio < 8 || ratio > 12 {
		t.Fatalf("MCS0/MCS7 airtime ratio %.1f, expected about 10", ratio)
	}
	// And a 1 Mbit/s CCK beacon is worse still.
	cck := frameUS(1500, rateInfo{Rate500: 2, CCK: true})
	if cck < slow*2 {
		t.Errorf("1 Mbit/s CCK (%.0f us) should dwarf MCS0 (%.0f us)", cck, slow)
	}
}

func TestShortGIIsShorter(t *testing.T) {
	long := frameUS(1500, rateInfo{HT: true, MCS: 7})
	short := frameUS(1500, rateInfo{HT: true, MCS: 7, ShortGI: true})
	if short >= long {
		t.Fatalf("short GI %.1f should be under long GI %.1f", short, long)
	}
	// 3.6 vs 4.0 us per symbol: 10% off the data portion, not the preamble.
	if ratio := (long - htPreambleUS) / (short - htPreambleUS); ratio < 1.1 || ratio > 1.12 {
		t.Errorf("data-portion ratio %.3f, expected ~1.111", ratio)
	}
}

func TestLegacyOFDMAndCCK(t *testing.T) {
	// 6 Mbit/s OFDM: NDBPS 24, preamble 20.
	got := frameUS(100, rateInfo{Rate500: 12})
	want := ofdmPreambleUS + math.Ceil((22+800)/24.0)*symbolUS
	if got != want {
		t.Errorf("6 Mbit/s 100 bytes: %.1f, want %.1f", got, want)
	}
	// 1 Mbit/s CCK: 192 us preamble, 8 us per byte.
	got = frameUS(100, rateInfo{Rate500: 2, CCK: true})
	if want = cckLongPreambleUS + 800; got != want {
		t.Errorf("1 Mbit/s 100 bytes: %.1f, want %.1f", got, want)
	}
}

func TestUnknownRateIsUnaccountedNotFree(t *testing.T) {
	// A frame whose rate radiotap did not report must come back 0 so the
	// caller can count it as unaccounted. Charging it zero airtime silently
	// would make a busy channel look idle.
	for _, r := range []rateInfo{
		{},
		{HT: true, MCS: 9}, // out of HT20 1SS range
		{HT: true, MCS: -1},
		{Rate500: 0},
	} {
		if got := frameUS(1500, r); got != 0 {
			t.Errorf("%+v should be unaccounted, got %.1f us", r, got)
		}
		if got := frameCostUS(1500, r, true); got != 0 {
			t.Errorf("%+v cost should be 0, got %.1f us", r, got)
		}
	}
}

func TestUnicastCostsAnAckAndBroadcastDoesNot(t *testing.T) {
	bare := frameUS(1000, rateInfo{HT: true, MCS: 5})
	uni := frameCostUS(1000, rateInfo{HT: true, MCS: 5}, true)
	bcast := frameCostUS(1000, rateInfo{HT: true, MCS: 5}, false)
	if bcast != bare {
		t.Errorf("broadcast should cost just the frame: %.1f vs %.1f", bcast, bare)
	}
	if uni-bare != sifsUS+ackUS {
		t.Errorf("unicast overhead %.1f, want %.1f", uni-bare, sifsUS+ackUS)
	}
}

// A full second of back-to-back 1500-byte frames at MCS 7 must come out near
// 100% busy and nowhere above it -- the sanity check that the units are right.
func TestAFullSecondOfTrafficIsAboutFullyBusy(t *testing.T) {
	per := frameCostUS(1500, rateInfo{HT: true, MCS: 7}, true)
	n := int(1e6 / per)
	total := per * float64(n)
	busy := total / 1e6 * 100
	if busy < 95 || busy > 100 {
		t.Fatalf("%d frames of %.1f us = %.1f%% busy, expected just under 100", n, per, busy)
	}
	// Back-to-back 1500-byte frames at MCS 7, paying only SIFS and an ACK,
	// come to about 46 Mbit/s of payload against the 65 Mbit/s PHY rate.
	//
	// That is HIGHER than the 25-30 Mbit/s a real TCP transfer gets on this
	// link, and the difference is the point: the model charges occupancy only.
	// DIFS, the random backoff, TCP's own ACKs and any contention are not in
	// it, because counting contention would make a quiet channel look busy.
	//
	// Consequence for reading the output: busy_pct is a FLOOR on true
	// utilisation and headroom_mbps is therefore OPTIMISTIC. Our traffic is
	// also relayed by the head unit, so it crosses the air twice and costs
	// double the headroom it appears to.
	mbps := float64(n*1500*8) / 1e6
	if mbps < 42 || mbps > 50 {
		t.Errorf("implied payload %.1f Mbit/s, expected ~46 for back-to-back MCS7 with ACKs", mbps)
	}
}

// --- radiotap ---------------------------------------------------------------

// buildRT makes a radiotap header with flags, rate, signal and MCS present,
// which is what these adapters emit.
func buildRT(rate500 byte, sig int8, mcs byte, sgi bool) []byte {
	present := uint32(1<<1 | 1<<2 | 1<<5 | 1<<19)
	b := []byte{0, 0, 0, 0}
	p := make([]byte, 4)
	binary.LittleEndian.PutUint32(p, present)
	b = append(b, p...)
	b = append(b, 0)         // 1 Flags
	b = append(b, rate500)   // 2 Rate
	b = append(b, byte(sig)) // 5 signal
	known := byte(mcsKnownIndex | mcsKnownGI)
	fl := byte(0)
	if sgi {
		fl |= mcsFlagSGI
	}
	b = append(b, known, fl, mcs) // 19 MCS
	binary.LittleEndian.PutUint16(b[2:4], uint16(len(b)))
	return b
}

func TestParseRadiotapReachesMCS(t *testing.T) {
	rt := parseRadiotap(buildRT(2, -61, 7, true))
	if !rt.OK {
		t.Fatal("not parsed")
	}
	if rt.Signal != -61 {
		t.Errorf("signal %d, want -61", rt.Signal)
	}
	if !rt.Rate.HT || rt.Rate.MCS != 7 || !rt.Rate.ShortGI {
		t.Fatalf("MCS not reached: %+v", rt.Rate)
	}
	// HT wins over the legacy rate byte: a frame is one or the other.
	if got := rt.Rate.Mbps(); got < 70 || got > 73 {
		t.Errorf("MCS7 SGI = %.1f Mbit/s, want ~72.2", got)
	}
}

func TestParseRadiotapLegacyCCK(t *testing.T) {
	// Rate only, no MCS field: a 1 Mbit/s beacon.
	present := uint32(1 << 2)
	b := []byte{0, 0, 0, 0}
	p := make([]byte, 4)
	binary.LittleEndian.PutUint32(p, present)
	b = append(b, p...)
	b = append(b, 2) // 1 Mbit/s
	binary.LittleEndian.PutUint16(b[2:4], uint16(len(b)))
	rt := parseRadiotap(b)
	if rt.Rate.HT {
		t.Fatal("should not be HT")
	}
	if !rt.Rate.CCK {
		t.Error("1 Mbit/s must be marked CCK")
	}
	if rt.Rate.Mbps() != 1 {
		t.Errorf("got %.1f Mbit/s", rt.Rate.Mbps())
	}
}

func TestParseRadiotapRejectsJunk(t *testing.T) {
	for _, b := range [][]byte{nil, {0}, {1, 0, 8, 0, 0, 0, 0, 0}, {0, 0, 200, 0}} {
		if rt := parseRadiotap(b); rt.OK && rt.HeaderLen > len(b) {
			t.Errorf("%v accepted with header beyond the buffer", b)
		}
	}
}

// --- frame ------------------------------------------------------------------

func TestUnicastDetectionFromAddr1(t *testing.T) {
	mk := func(a1 byte) []byte {
		w := make([]byte, 24)
		w[0] = 0x08 // data
		w[4] = a1
		return w
	}
	if f, _ := parseFrame(mk(0x02)); !f.Unicast {
		t.Error("02:.. is a unicast (locally administered) address")
	}
	if f, _ := parseFrame(mk(0xff)); f.Unicast {
		t.Error("ff:.. is a group address and is not acknowledged")
	}
	if f, _ := parseFrame(mk(0x33)); f.Unicast {
		t.Error("33:33:.. is IPv6 multicast and is not acknowledged")
	}
}

func TestRetryFlag(t *testing.T) {
	w := make([]byte, 24)
	w[1] = 0x08
	if f, _ := parseFrame(w); !f.Retry {
		t.Error("retry bit not read")
	}
}

// --- stall watchdog ---------------------------------------------------------

// The gap this closes: the AR9271 goes deaf in monitor mode while the
// interface stays up, so the process keeps running and records nothing. With
// Restart=always that is invisible, because the process never exits.
func TestStallWatchdogExitsAndExplainsItself(t *testing.T) {
	if stallAfter < 10*time.Second {
		t.Errorf("stallAfter %v is too eager: a brief lull must not restart a drive", stallAfter)
	}
	if stallCheck >= stallAfter {
		t.Errorf("stallCheck %v must be well under stallAfter %v", stallCheck, stallAfter)
	}
	if exitStall == 0 {
		t.Error("a stall must exit non-zero, or systemd will not restart it")
	}
}

// lastFrame has to be set on every frame, or the watchdog fires mid-drive on a
// perfectly healthy capture. This pins the contract rather than the plumbing.
func TestLastFrameIsRecent(t *testing.T) {
	lastFrame.Store(time.Now().UnixNano())
	if idle := time.Since(time.Unix(0, lastFrame.Load())); idle > time.Second {
		t.Fatalf("idle %v right after a store", idle)
	}
	// And an old value must read as stalled.
	lastFrame.Store(time.Now().Add(-2 * stallAfter).UnixNano())
	if idle := time.Since(time.Unix(0, lastFrame.Load())); idle < stallAfter {
		t.Fatalf("idle %v should exceed stallAfter %v", idle, stallAfter)
	}
}

// --- role classification ----------------------------------------------------

// The bug this pins: the first version classified a station on first sight,
// from whichever frame arrived first, and filed the loudest station on the
// channel -- an iPhone at -36 dBm pushing 650 kB/s, every byte addressed to a
// head unit -- as "other". The report then claimed CarPlay used 11.9% of the
// airtime when the real figure was about five times that.
func TestRoleIsDecidedByBehaviourNotFrameOrder(t *testing.T) {
	cars := map[string]bool{"8c:57:9b:8c:e8:64": true}
	box := map[string]bool{"24:ec:99:9b:32:34": true}

	phone := &stat{Bytes: 650487, ToHU: 650475}
	if got := role("d2:af:8e:31:1e:7d", phone, cars, box); got != "phone" {
		t.Errorf("a station sending ~100%% of its bytes to a head unit is a phone, got %q", got)
	}
	if got := role("8c:57:9b:8c:e8:64", &stat{}, cars, box); got != "hu" {
		t.Errorf("head unit misread as %q", got)
	}
	if got := role("24:ec:99:9b:32:34", &stat{}, cars, box); got != "box" {
		t.Errorf("our own radio misread as %q", got)
	}
	// A neighbour's AP sends nothing to our head units.
	if got := role("88:dc:97:07:b8:db", &stat{Bytes: 2766, ToHU: 0}, cars, box); got != "other" {
		t.Errorf("a neighbour misread as %q", got)
	}
	// A station with a minority of traffic to a head unit is not a phone.
	if got := role("aa:bb:cc:dd:ee:ff", &stat{Bytes: 1000, ToHU: 100}, cars, box); got != "other" {
		t.Errorf("10%% to a head unit should not make it a phone, got %q", got)
	}
	// Zero bytes must not divide by zero or guess.
	if got := role("aa:bb:cc:dd:ee:00", &stat{}, cars, box); got != "other" {
		t.Errorf("empty station -> %q", got)
	}
}

func TestPercentile(t *testing.T) {
	v := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	for _, c := range []struct {
		p    float64
		want float64
	}{{0.5, 5}, {0.95, 10}, {1, 10}, {0.05, 1}} {
		if got := pctl(v, c.p); got != c.want {
			t.Errorf("p%.2f = %v, want %v", c.p, got, c.want)
		}
	}
	if got := pctl(nil, .5); got != 0 {
		t.Errorf("empty -> %v", got)
	}
}

// A file written by the old binary has the wrong role in every second. The
// report must recompute rather than inherit it, or the one real dataset this
// project has stays mis-analysed.
func TestReportRederivesRoleFromStoredBytes(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/run.jsonl"
	// Exactly the shape of the bug: a phone stored as "other", every byte of
	// its traffic addressed to a head unit.
	line := `{"t":"2026-11-17T05:30:00Z","frames":100,"bytes":650000,"air_us":442000,` +
		`"busy_pct":44.2,"bad_fcs":0,"unaccounted":0,"best_mbps":65,"headroom_mbps":36,` +
		`"stations":{` +
		`"d2:af:8e:31:1e:7d":{"frames":80,"retries":3,"bytes":650000,"air_us":442000,` +
		`"unaccounted":0,"sig":-36,"max_mbps":65,"min_mbps":52,"role":"other","to_hu_bytes":649900,"to_hu_air_us":441000},` +
		`"8c:57:9b:8c:e8:64":{"frames":20,"retries":0,"bytes":14000,"air_us":106000,` +
		`"unaccounted":0,"sig":-44,"max_mbps":65,"min_mbps":1,"role":"hu","to_hu_bytes":0,"to_hu_air_us":0}}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := reportFile(path, &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "CarPlay active 1 s") {
		t.Errorf("the second was not recognised as CarPlay-active:\n%s", out)
	}
	// The phone must now be labelled phone, and its airtime must land in
	// CarPlay's cost rather than vanishing into "other".
	if !strings.Contains(out, "phone") {
		t.Errorf("no station classified as phone:\n%s", out)
	}
	if strings.Contains(out, "No CarPlay-active seconds") {
		t.Errorf("still mis-analysed:\n%s", out)
	}
}

// One level up from the first-frame bug: the per-station summary took Role
// from whichever second came last, so a single quiet second in which the phone
// sent little relabelled it "other" in the table -- even after the per-second
// classification was fixed. The role must come from the totals.
func TestAggregateRoleComesFromTotalsNotTheLastSecond(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/run.jsonl"
	busy := `{"t":"2026-11-17T05:30:00Z","frames":80,"bytes":650000,"air_us":442000,"busy_pct":44.2,` +
		`"best_mbps":65,"stations":{"d2:af:8e:31:1e:7d":{"frames":80,"bytes":650000,"air_us":442000,` +
		`"sig":-36,"role":"other","to_hu_bytes":649900}}}`
	// A quiet second: a few bytes, none of them to a head unit.
	quiet := `{"t":"2026-11-17T05:30:01Z","frames":2,"bytes":200,"air_us":600,"busy_pct":0.1,` +
		`"best_mbps":65,"stations":{"d2:af:8e:31:1e:7d":{"frames":2,"bytes":200,"air_us":600,` +
		`"sig":-36,"role":"other","to_hu_bytes":0}}}`
	if err := os.WriteFile(path, []byte(busy+"\n"+quiet+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := reportFile(path, &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	// 649900 of 650200 bytes went to a head unit: overwhelmingly a phone.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "d2:af:8e:31:1e:7d") {
			if !strings.Contains(line, "phone") {
				t.Fatalf("summary mislabels the phone from the last second:\n%s", line)
			}
			return
		}
	}
	t.Fatalf("station missing from the summary:\n%s", out)
}
