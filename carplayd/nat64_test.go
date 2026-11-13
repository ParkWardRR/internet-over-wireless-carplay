package main

import (
	"net/netip"
	"strings"
	"testing"
)

// RFC 6052 §2.2: under a /96 the four IPv4 octets are the last four bytes and
// there is no suffix. The DNS64 resolver synthesises with the same arithmetic,
// so if this is wrong the phone dials an address the ruleset does not permit
// and the failure looks like a firewall bug rather than a maths bug.
func TestNAT64AddrEmbedsIPv4PerRFC6052(t *testing.T) {
	for _, c := range []struct {
		prefix, v4, want string
	}{
		{"fd5c:ba1e:f32:64::/96", "10.0.0.21", "fd5c:ba1e:f32:64::a00:15"},
		{"fd5c:ba1e:f32:64::/96", "10.0.0.53", "fd5c:ba1e:f32:64::a00:35"},
		{"fd5c:ba1e:f32:64::/96", "0.0.0.0", "fd5c:ba1e:f32:64::"},
		{"fd5c:ba1e:f32:64::/96", "255.255.255.255", "fd5c:ba1e:f32:64::ffff:ffff"},
		{"64:ff9b::/96", "8.8.8.8", "64:ff9b::808:808"},
	} {
		got := nat64Addr(netip.MustParsePrefix(c.prefix), netip.MustParseAddr(c.v4))
		if got != netip.MustParseAddr(c.want) {
			t.Errorf("nat64Addr(%s, %s) = %s, want %s", c.prefix, c.v4, got, c.want)
		}
	}
}

// A prefix with dirt in the host bits must not shift the embedding.
func TestNAT64AddrMasksThePrefix(t *testing.T) {
	got := nat64Addr(netip.MustParsePrefix("fd5c:ba1e:f32:64::dead:beef/96"), netip.MustParseAddr("10.0.0.21"))
	if want := netip.MustParseAddr("fd5c:ba1e:f32:64::a00:15"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// globals splices extra top-level keys in ahead of sampleConf's [car] sections,
// where a key after a section header would be parsed as that car's.
func globals(extra string) string {
	return strings.Replace(sampleConf, "[car A]", extra+"[car A]", 1)
}

// sta has to be set in the config text, not on the struct: parseConfig copies
// cfg.STA into every car that did not name its own, so a later assignment to
// cfg.STA leaves cfg.stas() still reporting the default.
func nat64Config(t *testing.T, allow ...string) Config {
	t.Helper()
	cfg := mustConfig(t, globals("sta = wlan0\n"))
	cfg.Firewall, cfg.NAT66, cfg.Uplink = "nft", "nft", "eth0"
	cfg.NAT64 = netip.MustParsePrefix("fd5c:ba1e:f32:64::/96")
	for _, a := range allow {
		cfg.NAT64Allow = append(cfg.NAT64Allow, netip.MustParseAddr(a))
	}
	return cfg
}

// The whole point of the exception: it has to be evaluated before the rejects
// that would otherwise catch it. The NAT64 /96 is inside fc00::/7, which the
// private-range reject covers, and the translated packet leaves by a tun device
// and not the uplink, so the oifname reject would catch it too. An accept rule
// placed after either one is dead code, and the symptom is silent -- DNS64
// answers fine and the connection just hangs.
func TestNAT64AcceptPrecedesEveryReject(t *testing.T) {
	rs := nftRuleset(nat64Config(t, "10.0.0.21"), nil)
	// Only the forward chain: the input chain's own drop is a different hook,
	// and comparing across chains would compare rules that never interact.
	fwd := forwardChain(t, rs)
	lines := strings.Split(fwd, "\n")

	accept, firstReject := -1, -1
	for i, l := range lines {
		if !strings.Contains(l, `iifname "wlan0"`) {
			continue
		}
		if strings.Contains(l, "a00:15") && strings.Contains(l, "accept") {
			accept = i
		}
		if (strings.Contains(l, "reject") || strings.Contains(l, "drop")) && firstReject < 0 {
			firstReject = i
		}
	}
	if accept < 0 {
		t.Fatalf("no accept rule for the allowed host:\n%s", rs)
	}
	if firstReject < 0 {
		t.Fatalf("no reject rules at all, so isolation is gone:\n%s", rs)
	}
	if accept > firstReject {
		t.Errorf("NAT64 accept at line %d comes after a reject at line %d, so it never matches:\n%s", accept, firstReject, fwd)
	}
}

// forwardChain returns just the body of `chain forward { ... }`.
func forwardChain(t *testing.T, rs string) string {
	t.Helper()
	_, after, ok := strings.Cut(rs, "chain forward {")
	if !ok {
		t.Fatalf("no forward chain in the ruleset:\n%s", rs)
	}
	body, _, ok := strings.Cut(after, "\n\t}")
	if !ok {
		t.Fatalf("forward chain is never closed:\n%s", rs)
	}
	return body
}

// Only the named hosts, one /128 each. Permitting the /96 would quietly turn an
// allowlist into "the whole homelab", which is the thing the config is for.
func TestNAT64AcceptsOnlyAllowlistedHosts(t *testing.T) {
	rs := nftRuleset(nat64Config(t, "10.0.0.21"), nil)
	if !strings.Contains(rs, "fd5c:ba1e:f32:64::a00:15") {
		t.Errorf("allowed host missing from the ruleset:\n%s", rs)
	}
	for _, l := range strings.Split(rs, "\n") {
		if strings.Contains(l, "accept") && strings.Contains(l, "fd5c:ba1e:f32:64::/96") {
			t.Errorf("the whole NAT64 prefix is accepted, not just the allowlist: %s", strings.TrimSpace(l))
		}
	}
	// A host that was never allowed must not appear anywhere.
	if strings.Contains(rs, "a00:35") {
		t.Errorf("an un-allowlisted host leaked into the ruleset:\n%s", rs)
	}
}

// With no allowlist there is nothing to permit, and the ruleset must stay shut
// rather than fall back to something broader.
func TestNAT64WithoutAllowlistPermitsNothing(t *testing.T) {
	rs := nftRuleset(nat64Config(t), nil)
	for _, l := range strings.Split(rs, "\n") {
		if strings.Contains(l, "ip6 daddr") && strings.Contains(l, "accept") {
			t.Errorf("empty allowlist still produced a forwarding accept: %s", strings.TrimSpace(l))
		}
	}
}

// Port 53 to the box is opened only for NAT64, because DNS64 is the only reason
// the box needs to answer the car link at all.
func TestNAT64OpensDNSOnlyWhenEnabled(t *testing.T) {
	on := nftRuleset(nat64Config(t, "10.0.0.21"), nil)
	if !strings.Contains(on, "udp dport 53 accept") || !strings.Contains(on, "tcp dport 53 accept") {
		t.Errorf("nat64 is on but the resolver is unreachable from the car:\n%s", on)
	}
	// The accept must precede the blanket input drop, or it is dead code.
	if i, j := strings.Index(on, "udp dport 53 accept"), strings.Index(on, `iifname "wlan0" drop`); i > j {
		t.Errorf("DNS accept at %d comes after the input drop at %d:\n%s", i, j, on)
	}

	off := mustConfig(t, globals("sta = wlan0\n"))
	off.Firewall, off.NAT66, off.Uplink = "nft", "nft", "eth0"
	rs := nftRuleset(off, nil)
	if strings.Contains(rs, "dport 53") {
		t.Errorf("nat64 is off but DNS on the box is reachable from the car:\n%s", rs)
	}
	if strings.Contains(rs, "accept") && strings.Contains(rs, "fd5c:ba1e:f32:64::") {
		t.Errorf("nat64 is off but a NAT64 exception was emitted anyway:\n%s", rs)
	}
}

// Isolation must survive the feature: turning NAT64 on may not drop any of the
// rejects that keep a car off the home LAN.
func TestNAT64KeepsTheRestOfIsolation(t *testing.T) {
	rs := nftRuleset(nat64Config(t, "10.0.0.21"), nil)
	for _, want := range []string{
		`iifname "wlan0" meta nfproto ipv4 drop`,
		`iifname "wlan0" oifname != "eth0" reject`,
		"fc00::/7",
		`iifname "wlan0" drop`,
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("NAT64 removed an isolation rule, %q is gone:\n%s", want, rs)
		}
	}
}

// Advertising a public resolver alongside ours would make resolution depend on
// which server the phone picked: ours synthesises the AAAA, theirs returns the
// unroutable A. The list is replaced, not appended to.
func TestRAAdvertisesOnlyTheBoxWhenNAT64IsOn(t *testing.T) {
	cfg := nat64Config(t, "10.0.0.21")
	for i := range cfg.Cars {
		car := &cfg.Cars[i]
		got := raDNSFor(cfg, car)
		want := car.Prefix.Addr().Next()
		if len(got) != 1 {
			t.Fatalf("car %s: advertised %d resolvers, want exactly 1 (ours): %v", car.ID, len(got), got)
		}
		if got[0] != want {
			t.Errorf("car %s: advertised %s, want the box at %s", car.ID, got[0], want)
		}
		// cfg.DNS is still the upstream list, so health checks keep testing
		// the real internet rather than our own resolver.
		for _, d := range cfg.DNS {
			if got[0] == d {
				t.Errorf("car %s: advertised an upstream resolver %s", car.ID, d)
			}
		}
	}
}

func TestRAKeepsConfiguredDNSWhenNAT64IsOff(t *testing.T) {
	cfg := mustConfig(t, sampleConf)
	got := raDNSFor(cfg, &cfg.Cars[0])
	if len(got) != len(cfg.DNS) {
		t.Fatalf("advertised %v, want the configured %v", got, cfg.DNS)
	}
	for i := range got {
		if got[i] != cfg.DNS[i] {
			t.Errorf("advertised %v, want the configured %v", got, cfg.DNS)
		}
	}
}

func TestNAT64ConfigRejectsBadInput(t *testing.T) {
	for _, c := range []struct{ name, conf, want string }{
		{"not a /96", "nat64 = fd5c:ba1e:f32:64::/64\n", "must be a /96"},
		{"not IPv6", "nat64 = 10.0.0.0/96\n", "not an IPv6 prefix"},
		{"allow is not IPv4", "nat64 = fd5c:ba1e:f32:64::/96\nnat64_allow = ::1\n", "not an IPv4 address"},
		{"allow without nat64", "nat64_allow = 10.0.0.21\n", "needs nat64"},
		// RFC 6052 §3.1: the well-known prefix must not translate a non-global
		// address. Every host worth reaching here is RFC 1918, and reusing
		// 64:ff9b::/96 would collide with any real NAT64 the phone meets.
		{"well-known prefix, private target", "nat64 = 64:ff9b::/96\nnat64_allow = 10.0.0.21\n", "RFC 6052"},
	} {
		if _, err := parseConfig(strings.NewReader(globals(c.conf))); err == nil {
			t.Errorf("%s: accepted, want an error mentioning %q", c.name, c.want)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q, want it to mention %q", c.name, err, c.want)
		}
	}
}

func TestNAT64ConfigAcceptsAGoodBlock(t *testing.T) {
	cfg, err := parseConfig(strings.NewReader(globals(
		"nat64 = fd5c:ba1e:f32:64::/96\nnat64_allow = 10.0.0.21, 10.0.0.53\n")))
	if err != nil {
		t.Fatalf("rejected a valid block: %v", err)
	}
	if cfg.NAT64 != netip.MustParsePrefix("fd5c:ba1e:f32:64::/96") {
		t.Errorf("nat64 = %s", cfg.NAT64)
	}
	if len(cfg.NAT64Allow) != 2 {
		t.Fatalf("nat64_allow = %v, want 2 hosts", cfg.NAT64Allow)
	}
	// The well-known prefix is still fine for a global target.
	if _, err := parseConfig(strings.NewReader(globals(
		"nat64 = 64:ff9b::/96\nnat64_allow = 8.8.8.8\n"))); err != nil {
		t.Errorf("rejected the well-known prefix with a global target: %v", err)
	}
}
