package main

import (
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
	sta, up := cfg.STA, cfg.Uplink
	b.WriteString("table inet carplayd {}\ndelete table inet carplayd\ntable inet carplayd {\n")

	if cfg.Firewall == "nft" {
		b.WriteString("\tchain input {\n\t\ttype filter hook input priority -10; policy accept;\n")
		// Neighbour discovery and RS must reach us; nothing else from the car link
		// (no SSH, no metrics, no label printer, no DNS on the box).
		fmt.Fprintf(&b, "\t\tiifname %q meta l4proto ipv6-icmp accept\n", sta)
		fmt.Fprintf(&b, "\t\tiifname %q drop\n\t}\n", sta)

		b.WriteString("\tchain forward {\n\t\ttype filter hook forward priority -10; policy accept;\n")
		fmt.Fprintf(&b, "\t\tiifname %q meta nfproto ipv4 drop\n", sta)
		var nets []string
		for _, p := range outermost(append(slices.Clone(privateNets), block...)) {
			nets = append(nets, p.String())
		}
		fmt.Fprintf(&b, "\t\tiifname %q ip6 daddr { %s } reject\n", sta, strings.Join(nets, ", "))
		if up != "" {
			fmt.Fprintf(&b, "\t\tiifname %q oifname != %q reject\n", sta, up)
		}
		b.WriteString("\t}\n")
	}

	if cfg.NAT66 == "nft" {
		var srcs []string
		for _, c := range cfg.Cars {
			srcs = append(srcs, c.Prefix.String())
		}
		b.WriteString("\tchain postrouting {\n\t\ttype nat hook postrouting priority srcnat; policy accept;\n")
		fmt.Fprintf(&b, "\t\toifname %q ip6 saddr { %s } masquerade\n\t}\n", up, strings.Join(srcs, ", "))
	}
	b.WriteString("}\n")
	return b.String()
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
