package main

import (
	"net/http/httptest"
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

// With a radio per car, every car link must be filtered. An STA the firewall
// does not name is wide open: a phone on it could reach the box's own services
// and the home LAN. Cross-car traffic needs no explicit rule, because the car
// prefixes are ULAs inside fc00::/7 which the private-range reject covers.
func TestFirewallCoversEveryCarLink(t *testing.T) {
	cfg := radioConfig(t, `
phy = phy0
sta = wlan0
uplink = eth0
nat66 = nft
firewall = nft
[car A]
ssid = BMW00001 CarPlay
psk = 12345678
phy = phy1
[car B]
ssid = BMW00002 CarPlay
psk = 12345678
phy = phy2
[car C]
ssid = BMW00003 CarPlay
psk = 12345678
phy = phy3
`)
	rs := nftRuleset(cfg, nil)
	for _, sta := range []string{"phy1-sta0", "phy2-sta0", "phy3-sta0"} {
		for _, want := range []string{
			`iifname "` + sta + `" meta l4proto ipv6-icmp accept`,
			`iifname "` + sta + `" drop`,
			`iifname "` + sta + `" meta nfproto ipv4 drop`,
			`iifname "` + sta + `" oifname != "eth0" reject`,
		} {
			if !strings.Contains(rs, want) {
				t.Errorf("ruleset is missing %q\n%s", want, rs)
			}
		}
		// The ULA reject is what keeps the cars apart from each other.
		if !strings.Contains(rs, `iifname "`+sta+`" ip6 daddr {`) {
			t.Errorf("no destination reject for %s", sta)
		}
	}
	// Every car's prefix must be masqueraded, or that car has no internet.
	for _, c := range cfg.Cars {
		if !strings.Contains(rs, c.Prefix.String()) {
			t.Errorf("car %s prefix %s not masqueraded", c.ID, c.Prefix)
		}
	}
	if !strings.Contains(rs, "fc00::/7") {
		t.Error("fc00::/7 missing: cars could reach each other and the home ULA")
	}
}

// The single-radio ruleset must not change shape.
func TestFirewallSingleRadioUnchanged(t *testing.T) {
	cfg := radioConfig(t, `
phy = phy0
sta = wlan0
uplink = eth0
firewall = nft
[car A]
ssid = BMW00001 CarPlay
psk = 12345678
`)
	rs := nftRuleset(cfg, nil)
	if !strings.Contains(rs, `iifname "wlan0" drop`) {
		t.Errorf("single-radio ruleset lost its STA rules:\n%s", rs)
	}
	if strings.Contains(rs, "phy0-sta0") {
		t.Errorf("single-radio ruleset invented a derived STA name:\n%s", rs)
	}
}

// One daemon per radio, each confined to its own cars and interface, sharing
// exactly the things that must be shared. Getting this wrong is how two links
// end up fighting over one control socket or writing interleaved log lines.
func TestBuildLinksIsolatesRadiosAndSharesTheRest(t *testing.T) {
	cfg := radioConfig(t, `
phy = phy0
sta = wlan0
uplink = eth0
firewall = nft
[car A]
ssid = BMW00001 CarPlay
psk = 12345678
phy = phy1
[car B]
ssid = BMW00002 CarPlay
psk = 12345678
phy = phy2
`)
	out, m, prof := &evWriter{}, newMetrics(), newProfiles("")
	links := buildLinks(cfg, out, m, prof)
	if len(links) != 2 {
		t.Fatalf("want 2 links, got %d", len(links))
	}

	// Exactly one owner, or the firewall is built twice (or never).
	owners := 0
	for _, d := range links {
		if d.owner {
			owners++
		}
	}
	if owners != 1 || !links[0].owner {
		t.Errorf("want exactly the first link owning shared state, got %d owners", owners)
	}

	ctrl := map[string]bool{}
	for i, d := range links {
		// Each link sees only its own car.
		if len(d.cfg.Cars) != 1 {
			t.Errorf("link %d got %d cars, want 1", i, len(d.cfg.Cars))
		}
		if d.cfg.STA != d.cfg.Cars[0].STA || d.cfg.Phy != d.cfg.Cars[0].Phy {
			t.Errorf("link %d radio mismatch: cfg %s/%s vs car %s/%s",
				i, d.cfg.Phy, d.cfg.STA, d.cfg.Cars[0].Phy, d.cfg.Cars[0].STA)
		}
		// Control sockets must not collide.
		if ctrl[d.cfg.CtrlDir] {
			t.Errorf("link %d reuses control dir %s", i, d.cfg.CtrlDir)
		}
		ctrl[d.cfg.CtrlDir] = true
		// Shared singletons really are shared.
		if d.out != out || d.m != m || d.prof != prof {
			t.Errorf("link %d does not share the log writer, metrics or profiles", i)
		}
		// Per-link maps must be distinct, or one link's hold-offs leak.
		if i > 0 && (&d.held == &links[0].held) {
			t.Errorf("link %d shares the held map with link 0", i)
		}
		// The firewall view stays whole, or rules would name only this car.
		if len(d.fw.Cars) != 2 {
			t.Errorf("link %d firewall config sees %d cars, want all 2", i, len(d.fw.Cars))
		}
	}
}

// The single-radio box must still build exactly one link, owning everything.
func TestBuildLinksSingleRadio(t *testing.T) {
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
	links := buildLinks(cfg, &evWriter{}, newMetrics(), newProfiles(""))
	if len(links) != 1 {
		t.Fatalf("want 1 link, got %d", len(links))
	}
	if !links[0].owner || len(links[0].cfg.Cars) != 2 || links[0].cfg.CtrlDir != cfg.CtrlDir {
		t.Errorf("single-radio link changed shape: owner=%t cars=%d ctrl=%s",
			links[0].owner, len(links[0].cfg.Cars), links[0].cfg.CtrlDir)
	}
}

// Every configured car must have metric series from the start. If they only
// appear once a car has been served, an alert on "car B is not serving" has
// nothing to read until the first time it works.
func TestMetricsPreRegisterEveryCar(t *testing.T) {
	m := newMetrics()
	for _, id := range []string{"A", "B"} {
		m.setCar(id, func(*carMetric) {})
	}
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`carplayd_serving{car="A"} 0`,
		`carplayd_serving{car="B"} 0`,
		`carplayd_signal_dbm{car="A"}`,
		`carplayd_phones{car="B"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q\n%s", want, body)
		}
	}
}
