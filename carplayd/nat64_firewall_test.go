package main

import (
	"net/netip"
	"strings"
	"testing"
)

// nat64Cfg is a two-car config with NAT64 on and three allowlisted home hosts,
// matching what runs on the box.
func nat64Cfg() Config {
	return Config{
		Firewall: "nft",
		NAT66:    "nft",
		Uplink:   "eth0",
		NAT64:    netip.MustParsePrefix("fd5c:ba1e:f32:64::/96"),
		NAT64Allow: []netip.Addr{
			netip.MustParseAddr("172.16.1.69"),
			netip.MustParseAddr("172.16.3.6"),
			netip.MustParseAddr("172.16.4.21"),
		},
		Cars: []Car{
			{ID: "A", STA: "cara", Prefix: netip.MustParsePrefix("fd5c:ba1e:f32:a::/64")},
			{ID: "B", STA: "carb", Prefix: netip.MustParsePrefix("fd5c:ba1e:f32:b::/64")},
		},
	}
}

func TestNAT64Net(t *testing.T) {
	p := netip.MustParsePrefix("fd5c:ba1e:f32:64::/96")
	cases := map[string]string{
		"10.0.0.0/8":     "fd5c:ba1e:f32:64::a00:0/104",
		"172.16.0.0/12":  "fd5c:ba1e:f32:64::ac10:0/108",
		"192.168.0.0/16": "fd5c:ba1e:f32:64::c0a8:0/112",
		"127.0.0.0/8":    "fd5c:ba1e:f32:64::7f00:0/104",
		"0.0.0.0/8":      "fd5c:ba1e:f32:64::/104",
		"224.0.0.0/4":    "fd5c:ba1e:f32:64::e000:0/100",
	}
	for in, want := range cases {
		got := nat64Net(p, netip.MustParsePrefix(in)).String()
		if got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}

// The break this fixes: an IPv4-only public site is synthesised into the /96
// and must be reachable. www.reddit.com resolved to fd5c:ba1e:f32:64::9765:818c
// (151.101.129.140, Fastly) and was rejected.
func TestPublicIPv4ViaNAT64IsAccepted(t *testing.T) {
	rs := nftRuleset(nat64Cfg(), nil)
	for _, sta := range []string{"cara", "carb"} {
		want := `iifname "` + sta + `" ip6 daddr fd5c:ba1e:f32:64::/96 counter name "nat64_public" accept`
		if !strings.Contains(rs, want) {
			t.Errorf("missing the /96 accept for %s:\n%s", sta, rs)
		}
	}
}

func TestSynthesisedPrivateRangesAreRejected(t *testing.T) {
	rs := nftRuleset(nat64Cfg(), nil)
	// Opening the /96 must not open the home LAN with it.
	for _, want := range []string{
		"fd5c:ba1e:f32:64::a00:0/104",  // 10/8
		"fd5c:ba1e:f32:64::ac10:0/108", // 172.16/12, the home LAN and the car's own net
		"fd5c:ba1e:f32:64::c0a8:0/112", // 192.168/16
		"fd5c:ba1e:f32:64::7f00:0/104", // loopback
		"fd5c:ba1e:f32:64::a9fe:0/112", // link-local
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("%s is not rejected:\n%s", want, rs)
		}
	}
}

// Order is the whole design: allowlist accept, then private reject, then the
// /96 accept. Any other order either seals the allowlisted home hosts out or
// hands the car the entire home LAN.
func TestNAT64RuleOrder(t *testing.T) {
	rs := nftRuleset(nat64Cfg(), nil)
	for _, sta := range []string{"cara", "carb"} {
		allow := strings.Index(rs, `iifname "`+sta+`" ip6 daddr { fd5c:ba1e:f32:64::ac10:145`)
		priv := strings.Index(rs, `iifname "`+sta+`" ip6 daddr { fd5c:ba1e:f32:64::/104`)
		open := strings.Index(rs, `iifname "`+sta+`" ip6 daddr fd5c:ba1e:f32:64::/96 counter name "nat64_public" accept`)
		ipv4 := strings.Index(rs, `iifname "`+sta+`" meta nfproto ipv4 drop`)
		if allow < 0 || priv < 0 || open < 0 || ipv4 < 0 {
			t.Fatalf("%s: missing a rule (allow=%d priv=%d open=%d ipv4=%d)\n%s", sta, allow, priv, open, ipv4, rs)
		}
		if !(allow < priv && priv < open && open < ipv4) {
			t.Errorf("%s: wrong order allow=%d priv=%d open=%d ipv4=%d", sta, allow, priv, open, ipv4)
		}
	}
}

// The allowlisted hosts are private addresses, so they only work if their /128
// accept precedes the private-range reject that now exists.
func TestAllowlistedHomeHostsStillReachable(t *testing.T) {
	rs := nftRuleset(nat64Cfg(), nil)
	for _, a := range []string{
		"fd5c:ba1e:f32:64::ac10:145", // 172.16.1.69
		"fd5c:ba1e:f32:64::ac10:306", // 172.16.3.6
		"fd5c:ba1e:f32:64::ac10:415", // 172.16.4.21
	} {
		if !strings.Contains(rs, a) {
			t.Errorf("allowlisted host %s missing:\n%s", a, rs)
		}
	}
	// and each must appear before the 172.16/12 reject
	for _, sta := range []string{"cara", "carb"} {
		allow := strings.Index(rs, `iifname "`+sta+`" ip6 daddr { fd5c:ba1e:f32:64::ac10:145`)
		priv := strings.Index(rs, `iifname "`+sta+`" ip6 daddr { fd5c:ba1e:f32:64::/104`)
		if allow > priv {
			t.Errorf("%s: the allowlist must precede the private reject", sta)
		}
	}
}

// With NAT64 off, none of this may appear: a box without a translator must not
// advertise or permit a prefix that goes nowhere.
func TestNoNAT64MeansNoNAT64Rules(t *testing.T) {
	cfg := nat64Cfg()
	cfg.NAT64 = netip.Prefix{}
	cfg.NAT64Allow = nil
	rs := nftRuleset(cfg, nil)
	if strings.Contains(rs, "f32:64::") {
		t.Fatalf("NAT64 rules leaked into an unconfigured box:\n%s", rs)
	}
	// The home-LAN protection must still be there.
	if !strings.Contains(rs, "fc00::/7") {
		t.Fatalf("private-range reject lost:\n%s", rs)
	}
}

// The generic ULA reject must survive: native IPv6 ULAs other than the NAT64
// prefix are still forbidden, which is what keeps a car off the home LAN's own
// ULA addressing.
func TestULARejectSurvivesTheNAT64Opening(t *testing.T) {
	rs := nftRuleset(nat64Cfg(), nil)
	for _, sta := range []string{"cara", "carb"} {
		i := strings.Index(rs, `iifname "`+sta+`" ip6 daddr { fc00::/7`)
		if i < 0 {
			// the set may be ordered differently; just require fc00::/7 on a
			// reject line for this interface
			found := false
			for _, line := range strings.Split(rs, "\n") {
				if strings.Contains(line, `iifname "`+sta+`"`) && strings.Contains(line, "fc00::/7") && strings.Contains(line, "reject") {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: fc00::/7 is no longer rejected:\n%s", sta, rs)
			}
		}
	}
}

// The counter has to be declared, or nft refuses the whole table and the box
// loses its isolation along with the fix.
func TestNAT64PublicCounterIsDeclared(t *testing.T) {
	rs := nftRuleset(nat64Cfg(), nil)
	if !strings.Contains(rs, "counter nat64_public {}") {
		t.Fatalf("counter used but never declared:\n%s", rs)
	}
	decl := strings.Index(rs, "counter nat64_public {}")
	use := strings.Index(rs, `counter name "nat64_public"`)
	if decl > use {
		t.Errorf("declaration must precede use: decl=%d use=%d", decl, use)
	}
}

func TestNoNAT64MeansNoPublicCounter(t *testing.T) {
	cfg := nat64Cfg()
	cfg.NAT64, cfg.NAT64Allow = netip.Prefix{}, nil
	if rs := nftRuleset(cfg, nil); strings.Contains(rs, "nat64_public") {
		t.Fatalf("counter declared on a box with no translator:\n%s", rs)
	}
}
