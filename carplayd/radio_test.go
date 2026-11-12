package main

import (
	"strings"
	"testing"
)

func radioConfig(t *testing.T, body string) Config {
	t.Helper()
	cfg, err := parseConfig(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	return cfg
}

// The single-radio case must be untouched by the multi-radio work: one group,
// the historically configured STA, and the historical control directory.
func TestOneRadioIsUnchanged(t *testing.T) {
	cfg := radioConfig(t, `
phy = phy0
sta = wlan0
[car A]
ssid = BMW00001 CarPlay
psk = 12345678
[car B]
ssid = BMW00002 CarPlay
psk = 12345678
`)
	r := cfg.radios()
	if len(r) != 1 || len(r[0]) != 2 {
		t.Fatalf("want one radio serving two cars, got %d groups: %+v", len(r), r)
	}
	for _, c := range cfg.Cars {
		if c.Phy != "phy0" || c.STA != "wlan0" {
			t.Errorf("car %s: got phy=%s sta=%s, want phy0/wlan0", c.ID, c.Phy, c.STA)
		}
	}
	if got := cfg.ctrlDirFor("phy0"); got != cfg.CtrlDir {
		t.Errorf("control dir changed for a single radio: %s", got)
	}
}

// A radio per car is what lets both cars be served at once.
func TestRadioPerCarMakesIndependentLinks(t *testing.T) {
	cfg := radioConfig(t, `
phy = phy0
sta = wlan0
[car A]
ssid = BMW00001 CarPlay
psk = 12345678
phy = phy1
[car B]
ssid = BMW00002 CarPlay
psk = 12345678
phy = phy2
`)
	r := cfg.radios()
	if len(r) != 2 {
		t.Fatalf("want two independent links, got %d: %+v", len(r), r)
	}
	// Each non-default phy gets its own derived netdev, never the shared one.
	want := map[string]string{"A": "phy1-sta0", "B": "phy2-sta0"}
	for _, c := range cfg.Cars {
		if c.STA != want[c.ID] {
			t.Errorf("car %s: got sta=%s, want %s", c.ID, c.STA, want[c.ID])
		}
	}
	// Control sockets must not collide once there is more than one instance.
	a, b := cfg.ctrlDirFor("phy1"), cfg.ctrlDirFor("phy2")
	if a == b {
		t.Errorf("both radios share control dir %s", a)
	}
}

// Ten cars on ten radios: the grouping has to scale without special cases.
func TestManyRadiosGroupOnePerCar(t *testing.T) {
	var b strings.Builder
	b.WriteString("phy = phy0\nsta = wlan0\n")
	ids := "ABCDEFGHIJKL"
	for i := 0; i < 12; i++ {
		id := string(ids[i])
		b.WriteString("[car " + id + "]\nssid = BMW0000" + id + " CarPlay\npsk = 12345678\nphy = phy" + itoa(i+1) + "\n")
	}
	cfg := radioConfig(t, b.String())
	r := cfg.radios()
	if len(r) != 12 {
		t.Fatalf("want 12 independent links, got %d", len(r))
	}
	seenSTA := map[string]bool{}
	for _, g := range r {
		if len(g) != 1 {
			t.Errorf("group has %d cars, want 1", len(g))
		}
		if seenSTA[g[0].STA] {
			t.Errorf("sta %s reused across radios", g[0].STA)
		}
		seenSTA[g[0].STA] = true
	}
	// Distinct prefixes are what keep twelve cars' phones apart.
	seenPrefix := map[string]bool{}
	for _, c := range cfg.Cars {
		if seenPrefix[c.Prefix.String()] {
			t.Errorf("prefix %s reused", c.Prefix)
		}
		seenPrefix[c.Prefix.String()] = true
	}
}

// Physically impossible assignments must be rejected at parse time, not
// discovered as two wpa_supplicants fighting over one netdev at 3am.
func TestImpossibleRadioAssignmentsRejected(t *testing.T) {
	cases := map[string]string{
		"two stas on one phy": `
phy = phy0
sta = wlan0
[car A]
ssid = BMW00001 CarPlay
psk = 12345678
phy = phy1
sta = alpha
[car B]
ssid = BMW00002 CarPlay
psk = 12345678
phy = phy1
sta = beta
`,
		"one sta on two phys": `
phy = phy0
sta = wlan0
[car A]
ssid = BMW00001 CarPlay
psk = 12345678
phy = phy1
sta = shared
[car B]
ssid = BMW00002 CarPlay
psk = 12345678
phy = phy2
sta = shared
`,
	}
	for name, body := range cases {
		if _, err := parseConfig(strings.NewReader(body)); err == nil {
			t.Errorf("%s: accepted an impossible radio assignment", name)
		}
	}
}
