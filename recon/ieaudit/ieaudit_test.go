package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- generic element parsing -------------------------------------------------

func TestParseElementsRejectsMalformed(t *testing.T) {
	cases := []struct {
		name, hex, want string
	}{
		{"trailing id", "0001610b", "trailing octet"},
		{"length overruns", "0005616263", "declares length 5 but only 3 octets remain"},
		{"length overruns at end", "6b09", "declares length 9 but only 0 octets remain"},
		{"empty extension element", "ff00", "empty body"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := decodeHex(c.hex)
			if err != nil {
				t.Fatal(err)
			}
			_, err = parseElements(b)
			if err == nil {
				t.Fatalf("parsed %s without complaint", c.hex)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %q, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestParseElementsReturnsWhatItGotBeforeFailing(t *testing.T) {
	// A good SSID element, then a length that runs off the end.
	b, _ := decodeHex("000363617200ff")
	els, err := parseElements(b)
	if err == nil {
		t.Fatal("want an error for the truncated second element")
	}
	if len(els) != 1 || els[0].ID != 0 || string(els[0].Body) != "car" {
		t.Fatalf("want the first element back, got %+v", els)
	}
}

func TestExtensionElementKeepsItsID(t *testing.T) {
	b, _ := decodeHex("ff0323aabb")
	els, err := parseElements(b)
	if err != nil {
		t.Fatal(err)
	}
	if !els[0].HasExt || els[0].ExtID != 0x23 || len(els[0].Body) != 2 {
		t.Fatalf("got %+v", els[0])
	}
}

// --- 802.11u Interworking ----------------------------------------------------

func TestInterworkingAcceptsOnlyLegalLengths(t *testing.T) {
	for n := 0; n <= 10; n++ {
		legal := n == 1 || n == 3 || n == 7 || n == 9
		_, err := parseInterworking(make([]byte, n))
		if legal && err != nil {
			t.Errorf("length %d should parse: %v", n, err)
		}
		if !legal && err == nil {
			t.Errorf("length %d should be rejected", n)
		}
	}
}

func TestInterworkingBits(t *testing.T) {
	cases := []struct {
		hex      string
		internet bool
		anType   byte
		wantIn   string
		venue    bool
		vGroup   byte
	}{
		// What the F32's NBT EVO actually sends (docs/test-log.md, 2026-11-02):
		// private, venue group vehicular, Internet bit clear.
		{"000a01", false, 0, "internet=0", true, 10},
		// Trial B: the same AP with hostapd's internet=1.
		{"10", true, 0, "internet=1", false, 0},
	}
	for _, c := range cases {
		b, err := decodeHex(c.hex)
		if err != nil {
			t.Fatal(err)
		}
		w, err := parseInterworking(b)
		if err != nil {
			t.Fatal(err)
		}
		if w.Internet != c.internet || w.AccessNetworkType != c.anType {
			t.Errorf("%s: got internet=%v type=%d", c.hex, w.Internet, w.AccessNetworkType)
		}
		if w.HasVenue != c.venue || (c.venue && w.VenueGroup != c.vGroup) {
			t.Errorf("%s: venue %v/%d", c.hex, w.HasVenue, w.VenueGroup)
		}
		if !strings.Contains(w.String(), c.wantIn) {
			t.Errorf("%s: %q should mention %q", c.hex, w.String(), c.wantIn)
		}
	}
}

func TestInterworkingOptionalFieldPlacement(t *testing.T) {
	// Length 7 is ANO + HESSID with no venue info; length 3 is ANO + venue with
	// no HESSID. Getting these the wrong way round is the classic 802.11u bug.
	b, _ := decodeHex("13" + "001122334455")
	w, err := parseInterworking(b)
	if err != nil {
		t.Fatal(err)
	}
	if w.HasVenue {
		t.Error("length 7 must not be read as carrying venue info")
	}
	if !w.HasHESSID || !bytes.Equal(w.HESSID, []byte{0, 0x11, 0x22, 0x33, 0x44, 0x55}) {
		t.Errorf("hessid %x", w.HESSID)
	}
	if !w.Internet || w.AccessNetworkType != 3 {
		t.Errorf("internet=%v type=%d", w.Internet, w.AccessNetworkType)
	}

	b, _ = decodeHex("20" + "0a01" + "001122334455")
	w, err = parseInterworking(b)
	if err != nil {
		t.Fatal(err)
	}
	if !w.HasVenue || w.VenueGroup != 10 || !w.HasHESSID {
		t.Errorf("length 9 should carry both: %+v", w)
	}
	if !w.ASRA || w.Internet {
		t.Errorf("ANO 0x20 is ASRA set, Internet clear: got asra=%v internet=%v", w.ASRA, w.Internet)
	}
}

func TestInterworkingNamesReservedTypes(t *testing.T) {
	w, _ := parseInterworking([]byte{0x07})
	if !strings.Contains(w.String(), "reserved(7)") {
		t.Errorf("got %q", w.String())
	}
}

// --- Apple Device IE ---------------------------------------------------------

func TestAppleIEMatchesTheSwiftSelfTest(t *testing.T) {
	// The same bytes scripts/car-ie.swift --selftest decodes: AirPlay, CarPlay
	// and 2.4 GHz set, InternetAccess clear, then Interworking with internet=1.
	lines, err := describeElements(mustHex(t, cannedBlob))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"AirPlay", "CarPlay", "2.4GHz", "flags=8022", "internet=1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q missing from:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "InternetAccess") {
		t.Errorf("the canned flags have the internet bit clear:\n%s", joined)
	}
	if !strings.Contains(joined, "names unverified") {
		t.Error("the flag names are reverse-engineered; the output must say so")
	}
}

func TestAppleFlagBitNumbering(t *testing.T) {
	// Bit 0 is the most significant bit of flag octet 0, so bit 11 is 0x10 in
	// octet 1. If this ever changes, every claim about the internet bit moves.
	a, err := parseAppleIE(mustHex(t, "0002"+"0010"))
	if err != nil {
		t.Fatal(err)
	}
	if !a.FlagSet(internetAccessBit) {
		t.Fatalf("bit %d should be set in %x", internetAccessBit, a.Flags)
	}
	if appleFlagNames[internetAccessBit] != "InternetAccess" {
		t.Fatalf("bit %d is named %q", internetAccessBit, appleFlagNames[internetAccessBit])
	}
	if a.FlagSet(10) || a.FlagSet(12) {
		t.Error("only bit 11 should be set")
	}
}

func TestAppleIERejectsMalformed(t *testing.T) {
	cases := []struct{ name, hex, want string }{
		{"trailing sub-id", "0002" + "8022" + "01", "trailing octet"},
		{"sub length overruns", "0009" + "8022", "declares length 9"},
		{"empty flags", "0000", "flags sub-element"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseAppleIE(mustHex(t, c.hex))
			if err == nil {
				t.Fatalf("parsed %s without complaint", c.hex)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %q, want %q", err, c.want)
			}
		})
	}
}

func TestAppleIERejectsAnEmptyBody(t *testing.T) {
	if _, err := parseAppleIE(nil); err == nil || !strings.Contains(err.Error(), "no sub-elements") {
		t.Fatalf("got %v", err)
	}
}

func TestAppleIEWithholdsIdentifiers(t *testing.T) {
	// sub 6 is a Bluetooth MAC and sub 7 a device ID: neither may be printed.
	a, err := parseAppleIE(mustHex(t, "0606"+"001122334455"+"0702abcd"))
	if err != nil {
		t.Fatal(err)
	}
	s := a.String()
	for _, leak := range []string{"001122334455", "abcd"} {
		if strings.Contains(s, leak) {
			t.Fatalf("%q leaks %q", s, leak)
		}
	}
	if !strings.Contains(s, "withheld") {
		t.Fatalf("got %q", s)
	}
}

func TestVendorElementTooShortForItsOUI(t *testing.T) {
	_, _, _, err := vendorOUI(element{ID: 221, Body: []byte{0x00, 0xa0, 0x40}})
	if err == nil || !strings.Contains(err.Error(), "at least 4") {
		t.Fatalf("got %v", err)
	}
}

func TestNonAppleVendorElementIsNotDecodedAsApple(t *testing.T) {
	// A Microsoft WMM/WME element, which every AP sends.
	lines, err := describeElements(mustHex(t, "dd070050f2020001"+"00"))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "not the Apple Device IE") {
		t.Fatalf("got %v", lines)
	}
}

// --- pcap --------------------------------------------------------------------

func TestPcapRejectsWhatItCannotRead(t *testing.T) {
	pcapng := []byte{0x0a, 0x0d, 0x0d, 0x0a, 0, 0, 0, 0x1c}
	_, err := newPcapReader(bytes.NewReader(append(pcapng, make([]byte, 16)...)))
	if err == nil || !strings.Contains(err.Error(), "pcapng is not supported") {
		t.Fatalf("got %v", err)
	}

	ethernet := pcapHeader(1)
	_, err = newPcapReader(bytes.NewReader(ethernet))
	if err == nil || !strings.Contains(err.Error(), "not 802.11") {
		t.Fatalf("got %v", err)
	}
}

func TestPcapReadsBothByteOrders(t *testing.T) {
	for _, big := range []bool{false, true} {
		h := pcapHeader(linkIEEE80211)
		if big {
			h = pcapHeaderBE(linkIEEE80211)
		}
		f := append(h, pcapRecord(beacon(8, testBSSID, "CarTest", mustHex(t, "6b0110")), big)...)
		p, err := newPcapReader(bytes.NewReader(f))
		if err != nil {
			t.Fatal(err)
		}
		pk, err := p.next()
		if err != nil {
			t.Fatal(err)
		}
		if _, _, elems, ok := mgmtBody(pk.Data); !ok || len(elems) == 0 {
			t.Fatalf("big=%v: did not get a management frame back", big)
		}
	}
}

// --- the gate ----------------------------------------------------------------

var testBSSID = []byte{0x02, 0x00, 0x5e, 0x11, 0x22, 0x33}

func TestCheckPassesWhenTheTreatmentReachedTheAir(t *testing.T) {
	path := writePcap(t, "pass.pcap", linkIEEE80211Radiotap,
		beacon(8, testBSSID, "CarTest12345", mustHex(t, "6b0110")),
		beacon(8, testBSSID, "CarTest12345", mustHex(t, "6b0110")),
		beacon(5, testBSSID, "CarTest12345", mustHex(t, "6b0110")),
	)
	out, err := check(path, "1", "--bssid", "02:00:5e:11:22:33")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.HasPrefix(out, "PASS internet=1") {
		t.Fatalf("got %q", out)
	}
	if strings.Contains(out, "12345") || strings.Contains(out, "11:22:33") {
		t.Fatalf("output is not sanitized: %q", out)
	}
	if !strings.Contains(out, "2 beacons, 1 probe responses") {
		t.Fatalf("got %q", out)
	}
}

func TestCheckFailsWhenTheBitIsWrong(t *testing.T) {
	path := writePcap(t, "wrongbit.pcap", linkIEEE80211Radiotap,
		beacon(8, testBSSID, "CarTest", mustHex(t, "6b0100")),
		beacon(5, testBSSID, "CarTest", mustHex(t, "6b0100")),
	)
	out, err := check(path, "1", "--ssid", "CarTest")
	assertGateFail(t, err, out, "on air")
	if !strings.Contains(out, "internet=0") {
		t.Fatalf("the failure should show what was actually transmitted: %q", out)
	}
}

func TestCheckFailsWhenOnlyBeaconsCarryTheTreatment(t *testing.T) {
	// hostapd building the beacon and the probe response from different
	// templates is a real failure mode, and the one a config file cannot show.
	path := writePcap(t, "probeonly.pcap", linkIEEE80211Radiotap,
		beacon(8, testBSSID, "CarTest", mustHex(t, "6b0110")),
		beacon(5, testBSSID, "CarTest", mustHex(t, "6b0100")),
	)
	out, err := check(path, "1", "--ssid", "CarTest")
	assertGateFail(t, err, out, "2 different Interworking elements")
}

func TestCheckFailsWithoutProbeResponses(t *testing.T) {
	path := writePcap(t, "nobprobe.pcap", linkIEEE80211Radiotap,
		beacon(8, testBSSID, "CarTest", mustHex(t, "6b0110")),
	)
	out, err := check(path, "1", "--ssid", "CarTest")
	assertGateFail(t, err, out, "no probe responses captured")
}

func TestCheckFailsWhenTheElementIsAbsent(t *testing.T) {
	path := writePcap(t, "noiw.pcap", linkIEEE80211Radiotap,
		beacon(8, testBSSID, "CarTest", nil),
		beacon(5, testBSSID, "CarTest", nil),
	)
	out, err := check(path, "0", "--ssid", "CarTest")
	assertGateFail(t, err, out, "no Interworking element at all")
}

func TestCheckFailsOnMalformedElements(t *testing.T) {
	path := writePcap(t, "bad.pcap", linkIEEE80211Radiotap,
		beacon(8, testBSSID, "CarTest", mustHex(t, "6b02100a")),
		beacon(5, testBSSID, "CarTest", mustHex(t, "6b02100a")),
	)
	out, err := check(path, "1", "--ssid", "CarTest")
	assertGateFail(t, err, out, "malformed elements on the air")
	if !strings.Contains(out, "allows 1, 3, 7 or 9") {
		t.Fatalf("got %q", out)
	}
}

func TestCheckRefusesAnAmbiguousFilter(t *testing.T) {
	path := writePcap(t, "two.pcap", linkIEEE80211Radiotap,
		beacon(8, testBSSID, "CarTest", mustHex(t, "6b0110")),
	)
	if _, err := check(path, "1"); err == nil || !strings.Contains(err.Error(), "--bssid or --ssid") {
		t.Fatalf("got %v", err)
	}
	var g gateFail
	if _, err := check(path, "2", "--ssid", "CarTest"); err == nil || errors.As(err, &g) {
		t.Fatalf("a bad --internet value is a usage error, not a gate failure: %v", err)
	}
}

func TestCheckSeparatesTwoBSSIDs(t *testing.T) {
	other := []byte{0x02, 0x00, 0x5e, 0x99, 0x88, 0x77}
	path := writePcap(t, "mixed.pcap", linkIEEE80211Radiotap,
		beacon(8, testBSSID, "CarTest", mustHex(t, "6b0110")),
		beacon(5, testBSSID, "CarTest", mustHex(t, "6b0110")),
		beacon(8, other, "Neighbour", mustHex(t, "6b0100")),
		beacon(5, other, "Neighbour", mustHex(t, "6b0100")),
	)
	out, err := check(path, "1", "--bssid", "02:00:5e:11:22:33")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.HasPrefix(out, "PASS") {
		t.Fatalf("got %q", out)
	}
	// Without a filter, `pcap` should show both and `check` should refuse.
	var b strings.Builder
	if err := cmdPcap([]string{path}, map[string]string{}, &b); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(b.String(), "beacons,"); n != 2 {
		t.Fatalf("want both BSSIDs listed, got:\n%s", b.String())
	}
}

func TestCheckFlagsATruncatedCapture(t *testing.T) {
	// tcpdump's default snap length used to cut management frames short, which
	// looks exactly like an AP that stopped sending the element.
	f := beacon(8, testBSSID, "CarTest", mustHex(t, "6b0110"))
	rec := pcapRecordTruncated(f, len(f)-4)
	path := filepath.Join(t.TempDir(), "trunc.pcap")
	body := append(pcapHeader(linkIEEE80211), rec...)
	body = append(body, pcapRecord(beacon(5, testBSSID, "CarTest", mustHex(t, "6b0110")), false)...)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := check(path, "1", "--bssid", "02:00:5e:11:22:33")
	assertGateFail(t, err, out, "malformed elements on the air")
	if !strings.Contains(out, "capture truncated") {
		t.Fatalf("got %q", out)
	}
}

func TestPcapReportsNothingMatched(t *testing.T) {
	path := writePcap(t, "empty.pcap", linkIEEE80211Radiotap)
	err := cmdPcap([]string{path}, map[string]string{}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "monitor mode") {
		t.Fatalf("got %v", err)
	}
}

// --- masking -----------------------------------------------------------------

func TestMasking(t *testing.T) {
	t.Cleanup(func() { unmask = false })
	unmask = false
	if got := maskSSID("BMW00000 CarPlay"); strings.Contains(got, "00000") {
		t.Fatalf("got %q", got)
	}
	if got := maskSSID("CarTest"); got != "CarTest" {
		t.Fatalf("a digitless SSID should survive: %q", got)
	}
	if got := maskMAC(testBSSID); got != "02:00:5e:xx:xx:xx" {
		t.Fatalf("got %q", got)
	}
	unmask = true
	if got := maskSSID("BMW00000 CarPlay"); got != "BMW00000 CarPlay" {
		t.Fatalf("got %q", got)
	}
	if got := maskMAC(testBSSID); got != "02:00:5e:11:22:33" {
		t.Fatalf("got %q", got)
	}
}

// --- helpers -----------------------------------------------------------------

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := decodeHex(s)
	if err != nil {
		t.Fatalf("bad test hex %q: %v", s, err)
	}
	return b
}

func check(path, internet string, extra ...string) (string, error) {
	var b strings.Builder
	args := append([]string{path, "--internet", internet}, extra...)
	var pos []string
	opts := map[string]string{}
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			opts[strings.TrimPrefix(args[i], "--")] = args[i+1]
			i++
			continue
		}
		pos = append(pos, args[i])
	}
	err := cmdCheck(pos, opts, &b)
	return b.String(), err
}

func assertGateFail(t *testing.T, err error, out, want string) {
	t.Helper()
	var g gateFail
	if !errors.As(err, &g) {
		t.Fatalf("want a gate failure, got %v\n%s", err, out)
	}
	if !strings.Contains(out, want) {
		t.Fatalf("want the output to mention %q, got:\n%s", want, out)
	}
	if !strings.HasPrefix(out, "FAIL") && !strings.Contains(out, "\nFAIL") {
		t.Fatalf("a failure must say so on its own line:\n%s", out)
	}
}

// beacon builds a management frame: subtype 8 (beacon) or 5 (probe response).
func beacon(subtype int, bssid []byte, ssid string, extraIEs []byte) []byte {
	f := []byte{byte(subtype<<4) | 0x00, 0x00, 0, 0}
	f = append(f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff) // A1
	f = append(f, bssid...)                           // A2
	f = append(f, bssid...)                           // A3
	f = append(f, 0, 0)                               // sequence control
	f = append(f, make([]byte, 8)...)                 // timestamp
	f = append(f, 0x64, 0x00)                         // beacon interval
	f = append(f, 0x11, 0x04)                         // capability
	f = append(f, 0x00, byte(len(ssid)))              // SSID element
	f = append(f, ssid...)
	return append(f, extraIEs...)
}

func pcapHeader(link uint32) []byte {
	h := make([]byte, 24)
	binary.LittleEndian.PutUint32(h[0:], 0xa1b2c3d4)
	binary.LittleEndian.PutUint16(h[4:], 2)
	binary.LittleEndian.PutUint16(h[6:], 4)
	binary.LittleEndian.PutUint32(h[16:], 65535)
	binary.LittleEndian.PutUint32(h[20:], link)
	return h
}

func pcapHeaderBE(link uint32) []byte {
	h := make([]byte, 24)
	binary.BigEndian.PutUint32(h[0:], 0xa1b2c3d4)
	binary.BigEndian.PutUint16(h[4:], 2)
	binary.BigEndian.PutUint16(h[6:], 4)
	binary.BigEndian.PutUint32(h[16:], 65535)
	binary.BigEndian.PutUint32(h[20:], link)
	return h
}

func pcapRecord(frame []byte, big bool) []byte {
	bo := binary.ByteOrder(binary.LittleEndian)
	if big {
		bo = binary.BigEndian
	}
	h := make([]byte, 16)
	bo.PutUint32(h[0:], 1762000000)
	bo.PutUint32(h[4:], 0)
	bo.PutUint32(h[8:], uint32(len(frame)))
	bo.PutUint32(h[12:], uint32(len(frame)))
	return append(h, frame...)
}

func pcapRecordTruncated(frame []byte, incl int) []byte {
	h := make([]byte, 16)
	binary.LittleEndian.PutUint32(h[0:], 1762000000)
	binary.LittleEndian.PutUint32(h[8:], uint32(incl))
	binary.LittleEndian.PutUint32(h[12:], uint32(len(frame)))
	return append(h, frame[:incl]...)
}

// radiotapHeader is the shortest legal one: version 0, no fields present.
var radiotapHeader = []byte{0x00, 0x00, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00}

func writePcap(t *testing.T, name string, link uint32, frames ...[]byte) string {
	t.Helper()
	body := pcapHeader(link)
	for _, f := range frames {
		if link == linkIEEE80211Radiotap {
			f = append(append([]byte{}, radiotapHeader...), f...)
		}
		body = append(body, pcapRecord(f, false)...)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
