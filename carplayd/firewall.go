package main

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strings"
)

// nftRuleset renders carplayd's own nftables table. It isolates the car
// networks (a phone on the car Wi-Fi reaches the internet and nothing else:
// not the box's services, not the home LAN, not other private ranges) and,
// if asked, NAT66es the car prefixes out of the uplink. It is written as
// "create, delete, recreate" so applying it again replaces it atomically.
//
// On OpenWrt fw4 still has its own forward policy, so the car zone needs a
// forwarding to wan there; this table only adds restrictions (and NAT when
// nat66 = nft).
func nftRuleset(cfg Config, block []netip.Prefix) string {
	var b strings.Builder
	up := cfg.Uplink
	b.WriteString("table inet carplayd {}\ndelete table inet carplayd\ntable inet carplayd {\n")

	if cfg.Firewall == "nft" {
		var nets []string
		for _, p := range outermost(append(slices.Clone(privateNets), block...)) {
			nets = append(nets, p.String())
		}
		// Every car link, not just one: with a radio per car an unlisted STA
		// would be completely unfiltered, which is the opposite of isolation.
		// Cross-car traffic needs no rule of its own -- the car prefixes are
		// ULAs inside fc00::/7, which the private-range reject already covers.
		b.WriteString("\tchain input {\n\t\ttype filter hook input priority -10; policy accept;\n")
		for _, sta := range cfg.stas() {
			// Neighbour discovery and RS must reach us; nothing else from the
			// car link (no SSH, no metrics, no label printer).
			fmt.Fprintf(&b, "\t\tiifname %q meta l4proto ipv6-icmp accept\n", sta)
			// DNS on the box is otherwise deliberately closed. NAT64 is the one
			// case that needs it open: a synthesised AAAA only exists if the
			// phone asks our DNS64 resolver, so with nat64 set the RA advertises
			// the box as the only RDNSS and this is the port it points at.
			if cfg.NAT64.IsValid() {
				fmt.Fprintf(&b, "\t\tiifname %q udp dport 53 accept\n", sta)
				fmt.Fprintf(&b, "\t\tiifname %q tcp dport 53 accept\n", sta)
			}
			fmt.Fprintf(&b, "\t\tiifname %q drop\n", sta)
		}
		b.WriteString("\t}\n")

		// Named counters, declared before the chain that references them.
		//
		// `probe_<car>` is the one that matters. iOS gives a CarPlay network's
		// service rank Never, so it gets a scoped route and no more: system
		// daemons that bind to any interface will use it, apps never will,
		// until iOS validates the network for internet and the owner's
		// "Use For Internet" is on. iOS validates by fetching
		// http://captive.apple.com over that interface -- plain HTTP, port 80,
		// which almost nothing else on an iPhone uses. So this counter staying
		// at zero while a phone holds our prefix means the phone is not going
		// to send app traffic through us, and the box can say so.
		//
		// Measured on the 2026-11-11 trip, which is why this exists: the phone
		// took the prefix, got DNS answers and ran 1.36 MB through us -- all of
		// it APNS, dns.google and Play Services, not one user-facing app -- and
		// the owner had no Wi-Fi icon on CarPlay and no Maps. Nothing in any log
		// distinguished that from working.
		for _, c := range cfg.Cars {
			fmt.Fprintf(&b, "\tcounter probe_%s {}\n\tcounter fwd_%s {}\n", counterID(c.ID), counterID(c.ID))
		}
		if cfg.NAT64.IsValid() {
			b.WriteString("\tcounter nat64_public {}\n")
		}

		b.WriteString("\tchain forward {\n\t\ttype filter hook forward priority -10; policy accept;\n")
		// Counting first, and with no verdict of its own so evaluation falls
		// through: what the phone *tried* to do is the measurement, whether or
		// not a later rule rejects it.
		for _, c := range cfg.Cars {
			sta := c.STA
			if sta == "" {
				sta = cfg.STA
			}
			fmt.Fprintf(&b, "\t\tiifname %q ip6 saddr %s counter name \"fwd_%s\"\n", sta, c.Prefix, counterID(c.ID))
			fmt.Fprintf(&b, "\t\tiifname %q ip6 saddr %s tcp dport 80 counter name \"probe_%s\"\n", sta, c.Prefix, counterID(c.ID))
		}
		// The NAT64 exceptions, before every reject below: these destinations
		// sit inside the NAT64 /96, which is inside fc00::/7, which the
		// private-range reject covers -- and they leave by the translator's
		// tun device, not the uplink, so the oifname reject would catch them
		// too.
		//
		// Three rules in this order, and the order is the whole design:
		//
		//  1. the nat64_allow hosts, one /128 each. These are *private* IPv4
		//     addresses on the home LAN, so they have to be accepted before the
		//     private-range reject in 2. Widening this hole is a config change
		//     and never an accident.
		//  2. reject the synthesised form of every non-global IPv4 range. This
		//     is what keeps the car out of the home LAN once 3 opens the /96:
		//     without it, "accept the NAT64 prefix" would also hand the phone
		//     every RFC 1918 host behind the box.
		//  3. accept the rest of the /96 -- the public IPv4 internet, reached
		//     through our own translator, which masquerades out the uplink.
		//
		// Rule 3 is the fix for a real break: DNS64 synthesises an AAAA for
		// *every* IPv4-only name, but only the nat64_allow /128s were ever
		// accepted, so the phone got a routable-looking address for any
		// IPv4-only site and the firewall rejected it. www.reddit.com (no
		// native AAAA; it CNAMEs to Fastly) failed exactly this way, measured
		// 2026-11-16: rc=200 from the box, instant reject from the car prefix.
		var synth []string
		for _, a := range cfg.NAT64Allow {
			synth = append(synth, nat64Addr(cfg.NAT64, a).String())
		}
		var synthPrivate []string
		if cfg.NAT64.IsValid() {
			for _, v4 := range nonGlobalV4 {
				synthPrivate = append(synthPrivate, nat64Net(cfg.NAT64, v4).String())
			}
		}
		for _, sta := range cfg.stas() {
			if len(synth) > 0 {
				fmt.Fprintf(&b, "\t\tiifname %q ip6 daddr { %s } accept\n", sta, strings.Join(synth, ", "))
			}
			if len(synthPrivate) > 0 {
				fmt.Fprintf(&b, "\t\tiifname %q ip6 daddr { %s } reject\n", sta, strings.Join(synthPrivate, ", "))
				// Counted, because the fix is otherwise invisible: a nonzero
				// nat64_public after a car session is proof a phone really did
				// reach public IPv4 through the translator, which is the thing
				// that was silently broken.
				fmt.Fprintf(&b, "\t\tiifname %q ip6 daddr %s counter name \"nat64_public\" accept\n", sta, cfg.NAT64)
			}
			fmt.Fprintf(&b, "\t\tiifname %q meta nfproto ipv4 drop\n", sta)
			fmt.Fprintf(&b, "\t\tiifname %q ip6 daddr { %s } reject\n", sta, strings.Join(nets, ", "))
			if up != "" {
				fmt.Fprintf(&b, "\t\tiifname %q oifname != %q reject\n", sta, up)
			}
		}
		b.WriteString("\t}\n")
	}

	if cfg.NAT66 == "nft" {
		var srcs []string
		for _, c := range cfg.Cars {
			srcs = append(srcs, c.Prefix.String())
		}
		b.WriteString("\tchain postrouting {\n\t\ttype nat hook postrouting priority srcnat; policy accept;\n")
		fmt.Fprintf(&b, "\t\toifname %q ip6 saddr { %s } counter masquerade\n\t}\n", up, strings.Join(srcs, ", "))
	}
	b.WriteString("}\n")
	return b.String()
}

// nat64Addr embeds an IPv4 address in a NAT64 /96, which RFC 6052 §2.2 defines
// as the four octets in the last four bytes and no suffix. 10.0.0.21 under
// fd5c:ba1e:f32:64::/96 is fd5c:ba1e:f32:64::a00:15 -- the same arithmetic
// the DNS64 resolver does when it synthesises the AAAA, so the address the
// phone dials is the address this ruleset permits.
func nat64Addr(prefix netip.Prefix, v4 netip.Addr) netip.Addr {
	b := prefix.Masked().Addr().As16()
	v := v4.As4()
	copy(b[12:], v[:])
	return netip.AddrFrom16(b)
}

// nonGlobalV4 is every IPv4 range a phone on a car link must not reach through
// NAT64. Synthesised into the /96 these become the prefixes rejected just
// before the /96 itself is accepted, so opening NAT64 to the public internet
// does not also open the home LAN. 172.16.0.0/12 is deliberately wider than the
// home LAN's /16, and covers the car's own 172.16.224.0/24 as well.
var nonGlobalV4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

// nat64Net embeds an IPv4 *prefix* in a NAT64 /96: 10.0.0.0/8 under
// fd5c:ba1e:f32:64::/96 is fd5c:ba1e:f32:64::a00:0/104, because the four IPv4
// octets occupy the last 32 bits (RFC 6052 2.2) and so an n-bit IPv4 prefix is
// a (96+n)-bit IPv6 prefix.
func nat64Net(prefix, v4 netip.Prefix) netip.Prefix {
	return netip.PrefixFrom(nat64Addr(prefix, v4.Masked().Addr()), prefix.Bits()+v4.Bits())
}

// counterID makes an nftables identifier out of a car ID, which is free-form
// in the config file ("A", "F32", "the blue one").
func counterID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unnamed"
	}
	return b.String()
}

// nftCounter is one named counter's running total.
type nftCounter struct {
	Packets int64
	Bytes   int64
}

// parseNftCounters reads `nft -j list counters table inet carplayd`.
func parseNftCounters(out []byte) map[string]nftCounter {
	var doc struct {
		Nftables []struct {
			Counter *struct {
				Name    string `json:"name"`
				Packets int64  `json:"packets"`
				Bytes   int64  `json:"bytes"`
			} `json:"counter"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil
	}
	m := map[string]nftCounter{}
	for _, e := range doc.Nftables {
		if e.Counter != nil {
			m[e.Counter.Name] = nftCounter{e.Counter.Packets, e.Counter.Bytes}
		}
	}
	return m
}

// stas lists the distinct STA netdevs serving cars, in config order. With one
// radio this is cfg.STA; with a radio per car the firewall must name every one.
func (cfg Config) stas() []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cfg.Cars {
		if c.STA != "" && !seen[c.STA] {
			seen[c.STA] = true
			out = append(out, c.STA)
		}
	}
	if len(out) == 0 {
		out = []string{cfg.STA}
	}
	return out
}

// homePrefixes turns `ip -6 route show dev <uplink>` into the prefixes a car
// must not reach: every global on-link prefix, widened to /56 (the usual
// delegation size, so sibling home VLANs are covered too), plus extra.
func homePrefixes(routes string, extra []netip.Prefix) []netip.Prefix {
	seen := map[netip.Prefix]bool{}
	var out []netip.Prefix
	add := func(p netip.Prefix) {
		p = p.Masked()
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, l := range strings.Split(routes, "\n") {
		f := strings.Fields(l)
		if len(f) == 0 || f[0] == "default" || slices.Contains(f, "via") { // on-link only: routes via a gateway are the internet
			continue
		}
		p, err := netip.ParsePrefix(f[0])
		if err != nil || !p.Addr().Is6() || !p.Addr().IsGlobalUnicast() || p.Addr().IsPrivate() || p.Bits() >= 128 {
			continue
		}
		if p.Bits() > 56 {
			p = netip.PrefixFrom(p.Addr(), 56)
		}
		add(p)
	}
	for _, p := range extra {
		add(p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr().Less(out[j].Addr()) })
	return out
}

// privateNets are never reachable from a car: ULA (the home's and the other
// car's), link-local and multicast.
var privateNets = []netip.Prefix{
	netip.MustParsePrefix("fc00::/7"), netip.MustParsePrefix("fe80::/10"), netip.MustParsePrefix("ff00::/8"),
}

// outermost drops prefixes inside another one in the list (nft rejects
// overlapping intervals in a set) and sorts the rest.
func outermost(ps []netip.Prefix) []netip.Prefix {
	var out []netip.Prefix
	for i, p := range ps {
		inside := false
		for j, q := range ps {
			if i != j && q.Bits() <= p.Bits() && q.Contains(p.Addr()) && (q.Bits() < p.Bits() || j < i) {
				inside = true
				break
			}
		}
		if !inside {
			out = append(out, p.Masked())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr().Less(out[j].Addr()) })
	return out
}
