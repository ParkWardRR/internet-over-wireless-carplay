package main

import (
	"encoding/binary"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

const sampleConf = `
# test config
phy = phy0
dns = 2001:db8:ffff::53
hunt = 500ms
[car A]
ssid = BMW00001 CarPlay
psk = TestPSKcarA
channel = 11
priority = 1
[car B]
ssid = BMW00002 CarPlay
psk = TestPSKcarB
channel = 6
join = -78
leave = -82
rejoin = -75
`

func mustConfig(t *testing.T, s string) Config {
	t.Helper()
	cfg, err := parseConfig(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestParseConfig(t *testing.T) {
	cfg := mustConfig(t, sampleConf)
	if len(cfg.Cars) != 2 {
		t.Fatalf("cars = %d", len(cfg.Cars))
	}
	a, b := cfg.Cars[0], cfg.Cars[1]
	if a.SSID != "BMW00001 CarPlay" || a.Freq() != 2462 || a.Priority != 1 {
		t.Errorf("car A = %+v", a)
	}
	if b.Freq() != 2437 || b.Join != -78 || b.Leave != -82 || b.Rejoin != -75 {
		t.Errorf("car B = %+v", b)
	}
	// Per-car default prefixes keep the two car networks apart.
	if a.Prefix.String() != "fd5c:ba1e:f32:a::/64" || b.Prefix.String() != "fd5c:ba1e:f32:b::/64" {
		t.Errorf("prefixes %s %s", a.Prefix, b.Prefix)
	}
	if len(cfg.DNS) != 1 || cfg.DNS[0].String() != "2001:db8:ffff::53" {
		t.Errorf("dns %v", cfg.DNS)
	}
	if cfg.Hunt != 500*time.Millisecond {
		t.Errorf("hunt %v", cfg.Hunt)
	}
}

func TestParseConfigErrors(t *testing.T) {
	for name, s := range map[string]string{
		"no cars":        "phy = phy0\n",
		"short psk":      "[car A]\nssid = x\npsk = short\n",
		"5 GHz channel":  "[car A]\nssid = x\npsk = 12345678\nchannel = 36\n",
		"bad hysteresis": "[car A]\nssid = x\npsk = 12345678\njoin = -90\nleave = -80\n",
		"not a /64":      "[car A]\nssid = x\npsk = 12345678\nprefix = fd00::/48\n",
		"unknown key":    "[car A]\nssid = x\npsk = 12345678\ncolour = red\n",
		"bad section":    "[truck A]\n",
		"v4 dns":         "dns = 1.1.1.1\n[car A]\nssid = x\npsk = 12345678\n",
	} {
		if _, err := parseConfig(strings.NewReader(s)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestBuildRA(t *testing.T) {
	mac, _ := net.ParseMAC("02:00:00:00:00:01")
	p := raParams{
		RouterLife: 60 * time.Second, Prefix: netip.MustParsePrefix("fd5c:ba1e:f32:a::/64"),
		ValidLife: 24 * time.Hour, PreferredLife: 30 * time.Minute,
		DNS:     []netip.Addr{netip.MustParseAddr("2001:db8:ffff::53"), netip.MustParseAddr("2001:db8::1")},
		DNSLife: 9 * time.Second, SourceMAC: mac,
	}
	b := buildRA(p)
	if b[0] != icmpRA || b[1] != 0 {
		t.Fatalf("type/code %d/%d", b[0], b[1])
	}
	if lt := binary.BigEndian.Uint16(b[6:]); lt != 60 {
		t.Errorf("router lifetime %d", lt)
	}
	if b[5] != 0 {
		t.Errorf("flags %#x, want SLAAC only", b[5])
	}
	// Walk the options.
	opts := map[byte][]byte{}
	for o := b[16:]; len(o) > 0; {
		if len(o) < 2 || o[1] == 0 || int(o[1])*8 > len(o) {
			t.Fatalf("malformed option at %x", o)
		}
		opts[o[0]] = o[:int(o[1])*8]
		o = o[int(o[1])*8:]
	}
	if sl := opts[optSourceLL]; len(sl) != 8 || net.HardwareAddr(sl[2:8]).String() != mac.String() {
		t.Errorf("source LL option %x", sl)
	}
	pi := opts[optPrefixInfo]
	if len(pi) != 32 || pi[2] != 64 || pi[3] != 0xc0 {
		t.Fatalf("prefix option %x", pi)
	}
	if v, pr := binary.BigEndian.Uint32(pi[4:]), binary.BigEndian.Uint32(pi[8:]); v != 86400 || pr != 1800 {
		t.Errorf("prefix lifetimes %d/%d", v, pr)
	}
	if got := netip.AddrFrom16([16]byte(pi[16:32])); got.String() != "fd5c:ba1e:f32:a::" {
		t.Errorf("prefix %s", got)
	}
	rd := opts[optRDNSS]
	if len(rd) != 40 || rd[1] != 5 || binary.BigEndian.Uint32(rd[4:]) != 9 {
		t.Fatalf("rdnss %x", rd)
	}
	if got := netip.AddrFrom16([16]byte(rd[8:24])); got.String() != "2001:db8:ffff::53" {
		t.Errorf("rdnss[0] %s", got)
	}
}

func TestGoodbyeRA(t *testing.T) {
	b := buildRA(raParams{RouterLife: 0, Prefix: netip.MustParsePrefix("fd5c:ba1e:f32:a::/64"), ValidLife: time.Hour})
	if lt := binary.BigEndian.Uint16(b[6:]); lt != 0 {
		t.Errorf("goodbye router lifetime %d", lt)
	}
	// Router lifetime is capped at 9000 s (RFC 4861).
	b = buildRA(raParams{RouterLife: 5 * time.Hour})
	if lt := binary.BigEndian.Uint16(b[6:]); lt != 9000 {
		t.Errorf("capped lifetime %d", lt)
	}
}

func TestParseScanResults(t *testing.T) {
	r := "bssid / frequency / signal level / flags / ssid\n" +
		"02:00:00:00:01:00\t2462\t-48\t[WPA-PSK-CCMP+TKIP][WPA2-PSK-CCMP+TKIP][ESS]\tBMW00001 CarPlay\n" +
		"02:00:00:00:02:00\t2437\t-71\t[WPA2-PSK-CCMP][ESS]\tBMW00002 CarPlay\n" +
		"aa:bb:cc:dd:ee:ff\t2412\t-30\t[ESS]\t\n"
	s := parseScanResults(r)
	if len(s) != 3 || s[0].Signal != -48 || s[0].Freq != 2462 || s[0].SSID != "BMW00001 CarPlay" || s[2].SSID != "" {
		t.Fatalf("%+v", s)
	}
}

func scan(entries ...scanEntry) []scanEntry { return entries }

func TestPick(t *testing.T) {
	cfg := mustConfig(t, sampleConf)
	a := scanEntry{BSSID: "02:00:00:00:01:00", Freq: 2462, Signal: -60, SSID: "BMW00001 CarPlay"}
	b := scanEntry{BSSID: "02:00:00:00:02:00", Freq: 2437, Signal: -50, SSID: "BMW00002 CarPlay"}
	held := map[string]bool{}

	if c, _, ok := pick(cfg.Cars, held, scan(b)); !ok || c.ID != "B" {
		t.Errorf("only B seen: got %v %v", c.ID, ok)
	}
	// Both seen: A has higher priority even though B is stronger.
	if c, _, ok := pick(cfg.Cars, held, scan(a, b)); !ok || c.ID != "A" {
		t.Errorf("both seen: got %v", c.ID)
	}
	// Too weak to join (A joins at -80 by default).
	weakA := a
	weakA.Signal = -85
	if _, _, ok := pick(cfg.Cars, held, scan(weakA)); ok {
		t.Error("joined below threshold")
	}
	// Wrong channel: same SSID elsewhere is not our car.
	otherCh := a
	otherCh.Freq = 2412
	if _, _, ok := pick(cfg.Cars, held, scan(otherCh)); ok {
		t.Error("joined on the wrong channel")
	}
	// Hysteresis: after leaving A, -78 (above join -80) is not enough; -76 (rejoin) is.
	held["A"] = true
	midA := a
	midA.Signal = -78
	if _, _, ok := pick(cfg.Cars, held, scan(midA)); ok {
		t.Error("rejoined below the rejoin threshold")
	}
	midA.Signal = -76
	if c, _, ok := pick(cfg.Cars, held, scan(midA)); !ok || c.ID != "A" {
		t.Error("did not rejoin at the rejoin threshold")
	}
	// Absent from a scan releases the hold.
	releaseHolds(cfg.Cars, held, scan(b))
	if held["A"] {
		t.Error("hold not released when A disappeared")
	}
}

func TestPickBSSIDPin(t *testing.T) {
	cfg := mustConfig(t, "[car A]\nssid = BMW00001 CarPlay\npsk = 12345678\nbssid = BC:30:7E:00:00:01\n")
	right := scanEntry{BSSID: "bc:30:7e:00:00:01", Freq: 2462, Signal: -60, SSID: "BMW00001 CarPlay"}
	spoof := right
	spoof.BSSID = "02:00:00:00:00:99"
	spoof.Signal = -30
	c, e, ok := pick(cfg.Cars, map[string]bool{}, scan(spoof, right))
	if !ok || c.ID != "A" || e.BSSID != right.BSSID {
		t.Errorf("pinned BSSID: got %v %+v", ok, e)
	}
}

func TestLeaveCheck(t *testing.T) {
	t0 := time.Unix(1000, 0)
	below, leave := leaveCheck(time.Time{}, t0, -90, -84, 3*time.Second)
	if leave || below != t0 {
		t.Fatal("left at once")
	}
	if _, leave = leaveCheck(below, t0.Add(2*time.Second), -90, -84, 3*time.Second); leave {
		t.Error("left after 2 s")
	}
	if _, leave = leaveCheck(below, t0.Add(3*time.Second), -88, -84, 3*time.Second); !leave {
		t.Error("did not leave after 3 s below")
	}
	// A good sample resets the timer.
	if b, leave := leaveCheck(below, t0.Add(2*time.Second), -70, -84, 3*time.Second); leave || !b.IsZero() {
		t.Error("good sample did not reset")
	}
}

func TestWPAConfig(t *testing.T) {
	cfg := mustConfig(t, sampleConf)
	w := wpaConfig(cfg)
	for _, want := range []string{
		`ssid="BMW00001 CarPlay"`, "scan_freq=2462", "freq_list=2437", `id_str="B"`,
		"disabled=1", `bgscan=""`, "ctrl_interface=/var/run/carplayd",
	} {
		if !strings.Contains(w, want) {
			t.Errorf("wpa config missing %q", want)
		}
	}
	if strings.Count(w, "network={") != 2 {
		t.Error("want one network per car")
	}
}

func TestShouldSwitch(t *testing.T) {
	t0 := time.Unix(10000, 0)
	idle := 60 * time.Second
	// Other car seen before we started serving: never (no ping-pong).
	if shouldSwitch(t0.Add(5*time.Minute), t0, time.Time{}, idle, t0.Add(-time.Second)) {
		t.Error("switched to a car that was already there")
	}
	// Other car arrives later, served car never had a phone: switch after idle.
	if shouldSwitch(t0.Add(59*time.Second), t0, time.Time{}, idle, t0.Add(10*time.Second)) {
		t.Error("switched before idle")
	}
	if !shouldSwitch(t0.Add(61*time.Second), t0, time.Time{}, idle, t0.Add(10*time.Second)) {
		t.Error("did not switch after idle")
	}
	// A phone seen recently keeps us on the served car.
	if shouldSwitch(t0.Add(90*time.Second), t0, t0.Add(80*time.Second), idle, t0.Add(10*time.Second)) {
		t.Error("abandoned a car whose phone was seen 10 s ago")
	}
	if !shouldSwitch(t0.Add(150*time.Second), t0, t0.Add(80*time.Second), idle, t0.Add(10*time.Second)) {
		t.Error("did not switch 70 s after the phone left")
	}
	// Nothing else seen.
	if shouldSwitch(t0.Add(time.Hour), t0, time.Time{}, idle, time.Time{}) {
		t.Error("switched to nothing")
	}
}

func TestPhonesIn(t *testing.T) {
	neigh := "fd5c:ba1e:f32:a:0:ff:fe00:300 lladdr 02:00:00:00:03:00 REACHABLE\n" +
		"fd5c:ba1e:f32:a::99 lladdr 02:00:00:00:03:01 FAILED\n" +
		"fe80::ff:fe00:100 lladdr 02:00:00:00:01:00 STALE\n" +
		"fd5c:ba1e:f32:b::7 lladdr 02:00:00:00:04:00 STALE\n" +
		"fd5c:ba1e:f32:a:1::5 lladdr 02:00:00:00:03:02 STALE\n"
	if n := phonesIn(neigh, netip.MustParsePrefix("fd5c:ba1e:f32:a::/64")); n != 2 {
		t.Errorf("phones = %d, want 2", n)
	}
}

func TestPhonesInCompressed(t *testing.T) {
	// fd00:1::5 is NOT in fd00::/64 even though the strings share "fd00:".
	if n := phonesIn("fd00:1::5 lladdr 02:00:00:00:00:01 REACHABLE\nfd00::7 lladdr 02:00:00:00:00:02 REACHABLE\n", netip.MustParsePrefix("fd00::/64")); n != 1 {
		t.Errorf("phones = %d, want 1", n)
	}
}
