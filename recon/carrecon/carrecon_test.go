package main

import (
	"bytes"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	hu    = "02:00:00:00:01:00"
	box   = "02:00:00:00:00:00"
	phone = "02:00:00:00:03:00"
)

func hw(s string) []byte { m, _ := net.ParseMAC(s); return m }

// rt builds a radiotap header with flags, rate and dBm signal.
func rt(signal int8, rate500k byte) []byte {
	// present: flags(1) | rate(2) | dbm signal(5)
	b := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0 /*flags*/, rate500k, byte(signal)}
	binary.LittleEndian.PutUint16(b[2:], uint16(len(b)))
	binary.LittleEndian.PutUint32(b[4:], 1<<1|1<<2|1<<5)
	return b
}

// dot11 builds an 802.11 header + body.
func dot11(ftype, subtype int, toDS, fromDS, retry, protected bool, a1, a2, a3 string, body []byte) []byte {
	fc1 := byte(0)
	if toDS {
		fc1 |= 1
	}
	if fromDS {
		fc1 |= 2
	}
	if retry {
		fc1 |= 8
	}
	if protected {
		fc1 |= 0x40
	}
	h := []byte{byte(ftype<<2 | subtype<<4), fc1, 0, 0}
	h = append(h, hw(a1)...)
	h = append(h, hw(a2)...)
	h = append(h, hw(a3)...)
	h = append(h, 0, 0) // seq
	return append(h, body...)
}

func TestParseFrame(t *testing.T) {
	// Deauth from the head unit to the phone, reason 7.
	f, ok := parseFrame(append(rt(-55, 2), dot11(0, 12, false, false, false, false, phone, hu, hu, []byte{7, 0})...), 0)
	if !ok || f.ftype != 0 || f.subtype != 12 || f.reason != 7 || f.signal != -55 || f.rateKbps != 1000 || f.bssid() != hu || f.station() != phone {
		t.Fatalf("deauth %+v", f)
	}
	// EAPOL (unprotected QoS data, LLC/SNAP 888e) from the AP to the phone.
	eap := append([]byte{0, 0}, 0xaa, 0xaa, 3, 0, 0, 0, 0x88, 0x8e, 1, 3)
	f, _ = parseFrame(append(rt(-50, 0), dot11(2, 8, false, true, false, false, phone, hu, hu, eap)...), 0)
	if !f.eapol || f.bssid() != hu || f.station() != phone {
		t.Fatalf("eapol %+v", f)
	}
	// Protected data phone → HU, retry.
	f, _ = parseFrame(append(rt(-48, 0), dot11(2, 8, true, false, true, true, hu, phone, hu, make([]byte, 1000))...), 0)
	if f.eapol || !f.retry || !f.protected || f.bssid() != hu || f.station() != phone {
		t.Fatalf("data %+v", f)
	}
}

func pcapStream(frames []([]byte), start time.Time, step time.Duration) []byte {
	var b bytes.Buffer
	gh := make([]byte, 24)
	binary.LittleEndian.PutUint32(gh[0:], 0xa1b2c3d4)
	binary.LittleEndian.PutUint16(gh[4:], 2)
	binary.LittleEndian.PutUint16(gh[6:], 4)
	binary.LittleEndian.PutUint32(gh[16:], 65535)
	binary.LittleEndian.PutUint32(gh[20:], 127)
	b.Write(gh)
	for i, fr := range frames {
		ts := start.Add(time.Duration(i) * step)
		rh := make([]byte, 16)
		binary.LittleEndian.PutUint32(rh[0:], uint32(ts.Unix()))
		binary.LittleEndian.PutUint32(rh[4:], uint32(ts.Nanosecond()/1000))
		binary.LittleEndian.PutUint32(rh[8:], uint32(len(fr)))
		binary.LittleEndian.PutUint32(rh[12:], uint32(len(fr)))
		b.Write(rh)
		b.Write(fr)
	}
	return b.Bytes()
}

// A phone streams to the head unit for 5 s (30 KB/s), then stops; the
// head unit deauths it. Expect stream_start, stream_stop after 3 quiet
// seconds, the deauth, and marks for both.
func TestStreamStopAndDeauth(t *testing.T) {
	dir := t.TempDir()
	ev, _ := os.Create(filepath.Join(dir, "events.log"))
	r := &recon{cars: map[string]bool{hu: true}, box: box, stats: map[string]*stat{}, stream: map[string]bool{}, quiet: map[string]int{}, ev: ev}
	var frames [][]byte
	data := append(rt(-50, 0), dot11(2, 8, true, false, false, true, hu, phone, hu, make([]byte, 1500))...)
	toBox := append(rt(-50, 0), dot11(2, 8, true, false, true, true, hu, phone, box, make([]byte, 600))...)
	for s := 0; s < 5; s++ { // 10 frames per 0.5 s step pattern: 20 frames/s ≈ 30 KB/s
		for i := 0; i < 20; i++ {
			frames = append(frames, data)
		}
		frames = append(frames, toBox)
	}
	for s := 0; s < 5; s++ { // only a trickle afterwards
		frames = append(frames, toBox)
		for i := 0; i < 20; i++ {
			frames = append(frames, append(rt(-50, 2), dot11(0, 8, false, false, false, false, "ff:ff:ff:ff:ff:ff", hu, hu, make([]byte, 40))...)) // beacons only
		}
	}
	frames = append(frames, append(rt(-60, 2), dot11(0, 12, false, false, false, false, phone, hu, hu, []byte{4, 0})...))
	for i := 0; i < 21; i++ { // one more second so everything flushes
		frames = append(frames, toBox)
	}
	start := time.Unix(1_800_000_000, 0)
	r.readPcap(bytes.NewReader(pcapStream(frames, start, time.Second/21)))
	r.flush(r.sec)
	ev.Close()
	log, _ := os.ReadFile(filepath.Join(dir, "events.log"))
	s := string(log)
	for _, want := range []string{"stream_start phone=" + phone, "stream_stop phone=" + phone, "deauth from=" + hu + "(hu) to=" + phone + "(sta) reason=4", "mark stream_stop", "mark deauth", "to_box="} {
		if !strings.Contains(s, want) {
			t.Errorf("events lack %q:\n%s", want, s)
		}
	}
	if i, j := strings.Index(s, "stream_stop"), strings.Index(s, "deauth from"); i < 0 || i > j {
		t.Errorf("stream_stop must come before the deauth (quiet link, beacons only):\n%s", s)
	}
	if strings.Count(s, "stream_stop") != 2 { // the event and its mark
		t.Errorf("stream_stop count:\n%s", s)
	}
}

func TestPruneKeepsMarked(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i := 0; i < 10; i++ {
		p := filepath.Join(dir, now.Add(time.Duration(i-10)*time.Minute).Format("20060102-150405")+".pcap")
		os.WriteFile(p, make([]byte, 1000), 0o600)
		mt := now.Add(time.Duration(i-10) * time.Minute)
		os.Chtimes(p, mt, mt)
	}
	r := &recon{marks: []time.Time{now.Add(-9 * time.Minute)}}
	r.prune(dir, 3000, 30*time.Second)
	left, _ := filepath.Glob(filepath.Join(dir, "*.pcap"))
	if len(left) > 5 || len(left) < 3 {
		t.Fatalf("left %d files", len(left))
	}
	keptOld := false
	for _, f := range left {
		if strings.HasPrefix(filepath.Base(f), now.Add(-9*time.Minute).Format("20060102-1504")) {
			keptOld = true
		}
	}
	if !keptOld {
		t.Errorf("marked file pruned: %v", left)
	}
}

// The live parser captures 256 bytes per frame; the byte counts must use the
// on-air length from the pcap record, or a CarPlay stream looks like a trickle.
func TestSnapLengthUsesOrigLen(t *testing.T) {
	full := append(rt(-50, 0), dot11(2, 8, true, false, false, true, hu, phone, hu, make([]byte, 1500))...)
	f, ok := parseFrame(full[:256], len(full))
	if !ok || f.length != len(full)-11 {
		t.Fatalf("length %d, want %d", f.length, len(full)-11)
	}
}
