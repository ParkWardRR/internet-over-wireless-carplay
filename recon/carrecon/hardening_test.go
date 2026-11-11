package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// eapolFrame builds a radiotap + 802.11 data frame carrying an EAPOL-Key with
// the given Key Information, so the handshake-message classifier can be tested
// without a capture.
func eapolFrame(keyInfo uint16, qos bool) []byte {
	rt := []byte{0, 0, 8, 0, 0, 0, 0, 0} // radiotap: version 0, len 8, no fields
	fc0 := byte(0x08)                    // data frame, subtype 0
	if qos {
		fc0 = 0x88 // QoS data, subtype 8 -> 2 extra header bytes
	}
	w := make([]byte, 0, 64)
	w = append(w, fc0, 0x01) // toDS
	w = append(w, 0, 0)      // duration
	w = append(w, 0xbc, 0x30, 0x7e, 0x00, 0x00, 0x01)
	w = append(w, 0xaa, 0xbb, 0xcc, 0x00, 0x00, 0x02)
	w = append(w, 0xbc, 0x30, 0x7e, 0x00, 0x00, 0x01)
	w = append(w, 0, 0) // sequence control -> 24 bytes
	if qos {
		w = append(w, 0, 0)
	}
	w = append(w, 0xaa, 0xaa, 0x03, 0x00, 0x00, 0x00, 0x88, 0x8e) // SNAP, EAPOL
	w = append(w, 1, 3, 0, 95)                                    // 802.1X: version, type 3 (Key), length
	w = append(w, 2)                                              // descriptor type
	ki := make([]byte, 2)
	binary.BigEndian.PutUint16(ki, keyInfo)
	w = append(w, ki...)
	w = append(w, make([]byte, 16)...) // key length, replay counter, ...
	return append(rt, w...)
}

// Wireshark needs all four handshake messages to decrypt a station's traffic,
// so the log has to say which ones were captured. The classification follows
// the Key Information bits: MIC absent is M1, MIC without Secure is M2, and
// once Secure is set the Ack bit separates M3 from M4.
func TestEAPOLHandshakeMessage(t *testing.T) {
	const (
		keyType = 1 << 3
		install = 1 << 6
		ack     = 1 << 7
		mic     = 1 << 8
		secure  = 1 << 9
	)
	cases := []struct {
		name    string
		keyInfo uint16
		want    int
	}{
		{"M1 ack, no mic", keyType | ack | 2, 1},
		{"M2 mic, not secure", keyType | mic | 2, 2},
		{"M3 install+ack+mic+secure", keyType | install | ack | mic | secure | 2, 3},
		{"M4 mic+secure only", keyType | mic | secure | 2, 4},
	}
	for _, c := range cases {
		for _, qos := range []bool{false, true} {
			f, ok := parseFrame(eapolFrame(c.keyInfo, qos), 0)
			if !ok {
				t.Fatalf("%s (qos=%t): frame did not parse", c.name, qos)
			}
			if !f.eapol {
				t.Errorf("%s (qos=%t): not detected as EAPOL", c.name, qos)
			}
			if f.eapolMsg != c.want {
				t.Errorf("%s (qos=%t): got m=%d, want m=%d", c.name, qos, f.eapolMsg, c.want)
			}
		}
	}
}

// A silently stalled AR9271 leaves the interface up and tcpdump running with
// nothing to read, so the only evidence is that frames stopped arriving. The
// watchdog has to notice that and kick the capture; if it did not, a drive
// would record nothing and say so nowhere.
func TestWatchStallKicksWhenFramesStop(t *testing.T) {
	ev, err := os.CreateTemp(t.TempDir(), "ev")
	if err != nil {
		t.Fatal(err)
	}
	r := &recon{ev: ev}
	// Last frame is already older than the stall threshold.
	r.lastFrame.Store(time.Now().Add(-2 * stallAfter).UnixNano())

	kicked := make(chan struct{}, 4)
	stop := make(chan struct{})
	// iface non-empty: the test path restarts the capture without touching
	// the adapter (no iw/modprobe in a test environment).
	go r.watchStall("mon0", func() { kicked <- struct{}{} }, stop)
	select {
	case <-kicked:
	case <-time.After(3 * stallCheck):
		close(stop)
		t.Fatal("watchdog did not kick after frames stopped")
	}
	close(stop)

	b, _ := os.ReadFile(ev.Name())
	if !strings.Contains(string(b), " stall ") {
		t.Errorf("no stall event logged, got: %q", string(b))
	}
}

// A live capture must not be treated as stalled.
func TestWatchStallQuietWhenFramesArrive(t *testing.T) {
	r := &recon{}
	stop := make(chan struct{})
	defer close(stop)
	kicked := make(chan struct{}, 1)
	go r.watchStall("mon0", func() { kicked <- struct{}{} }, stop)
	// Keep reporting frames for longer than one check interval.
	deadline := time.After(3 * stallCheck)
	for {
		select {
		case <-deadline:
			return
		case <-kicked:
			t.Fatal("watchdog kicked while frames were still arriving")
		default:
			r.lastFrame.Store(time.Now().UnixNano())
			time.Sleep(200 * time.Millisecond)
		}
	}
}

// The stream floor decides when a CarPlay session counts as dead. A recon
// built without one (tests, older callers) must still use the default rather
// than treating every second of silence as a live stream.
func TestMinStreamFallsBackToDefault(t *testing.T) {
	if got := (&recon{}).minStream(); got != defaultStreamMin {
		t.Errorf("unset: got %d, want %d", got, defaultStreamMin)
	}
	if got := (&recon{streamMin: 1234}).minStream(); got != 1234 {
		t.Errorf("configured: got %d, want 1234", got)
	}
}

// ringStatus names the newest capture file. It compares basenames, because a
// full path sorts below a timestamped basename and the field silently reported
// the oldest file instead.
func TestRingStatusReportsNewestFile(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"20261111-152345.pcap", "20261111-152445.pcap", "20261111-230458.pcap"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ev, err := os.CreateTemp(t.TempDir(), "ev")
	if err != nil {
		t.Fatal(err)
	}
	r := &recon{ev: ev}
	r.ringStatus(dir)
	b, _ := os.ReadFile(ev.Name())
	if !strings.Contains(string(b), "newest=20261111-230458.pcap") {
		t.Errorf("wrong newest file: %q", string(b))
	}
	if !strings.Contains(string(b), "files=3") {
		t.Errorf("wrong file count: %q", string(b))
	}
}
