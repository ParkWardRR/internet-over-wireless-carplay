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

// REVISED 2026-11-16. These two tests used to assert that *only* the
// allowlisted /128s were ever accepted and that an empty allowlist permitted
// nothing at all. That was a real bug, not a safety property: DNS64 synthesises
// an AAAA for every IPv4-only name, so the phone got a routable-looking address
// for any IPv4-only *public* site and the firewall rejected it.
// www.reddit.com (no native AAAA; it CNAMEs to Fastly) failed exactly that way
// -- rc=200 from the box, instant reject from the car prefix -- and so,
// silently, had gsp1.apple.com, push and Mapbox, the three hosts the README
// said NAT64 was for.
//
// The invariant those tests were really protecting is "a car cannot reach the
// home LAN", and it is now enforced by rejecting the synthesised form of every
// non-global IPv4 range before the /96 is accepted. That is stronger than an
// enumerated allowlist, because it is derived from the address ranges rather
// than from remembering to list a host. The allowlist keeps its original job:
// it is the only way to reach a *private* address, and nothing else.

// A private host is reachable only if it is on the allowlist.
func TestNAT64AllowlistIsTheOnlyRouteToPrivateHosts(t *testing.T) {
	rs := nftRuleset(nat64Config(t, "10.0.0.21"), nil)
	fc := forwardChain(t, rs)
	if !strings.Contains(fc, "fd5c:ba1e:f32:64::a00:15") {
		t.Errorf("allowed host missing from the forward chain:\n%s", fc)
	}
	// A host that was never allowed must not be named anywhere.
	if strings.Contains(rs, "a00:35") {
		t.Errorf("an un-allowlisted host leaked into the ruleset:\n%s", rs)
	}
	// 10.0.0.0/8 synthesised must be rejected, and that reject must come after
	// the allowlist accept (or the allowed host is dead) and before the /96
	// accept (or the whole homelab is open).
	allow := strings.Index(fc, "fd5c:ba1e:f32:64::a00:15")
	priv := strings.Index(fc, "fd5c:ba1e:f32:64::a00:0/104")
	open := strings.Index(fc, `ip6 daddr fd5c:ba1e:f32:64::/96 counter name "nat64_public" accept`)
	if allow < 0 || priv < 0 || open < 0 {
		t.Fatalf("missing a rule (allow=%d priv=%d open=%d):\n%s", allow, priv, open, fc)
	}
	if !(allow < priv && priv < open) {
		t.Errorf("order must be allowlist, then private reject, then /96: got %d %d %d", allow, priv, open)
	}
}

// With no allowlist, public IPv4 still has to work -- that is the feature --
// while every private range stays shut.
func TestNAT64WithoutAllowlistStillReachesPublicIPv4(t *testing.T) {
	fc := forwardChain(t, nftRuleset(nat64Config(t), nil))
	if !strings.Contains(fc, `ip6 daddr fd5c:ba1e:f32:64::/96 counter name "nat64_public" accept`) {
		t.Fatalf("public IPv4 via NAT64 is unreachable with an empty allowlist:\n%s", fc)
	}
	for _, priv := range []string{
		"fd5c:ba1e:f32:64::a00:0/104",  // 10/8
		"fd5c:ba1e:f32:64::ac10:0/108", // 172.16/12
		"fd5c:ba1e:f32:64::c0a8:0/112", // 192.168/16
	} {
		if !strings.Contains(fc, priv) {
			t.Errorf("%s must still be rejected:\n%s", priv, fc)
		}
		if strings.Index(fc, priv) > strings.Index(fc, `ip6 daddr fd5c:ba1e:f32:64::/96 counter name "nat64_public" accept`) {
			t.Errorf("%s is rejected after the /96 accept, so it is dead code", priv)
		}
	}
	// No /128 accept, because there is nothing allowlisted.
	if strings.Contains(fc, "a00:15") {
		t.Errorf("an empty allowlist produced a host accept:\n%s", fc)
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
