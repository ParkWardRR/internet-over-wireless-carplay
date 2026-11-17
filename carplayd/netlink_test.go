package main

import (
	"net"
	"net/netip"
	"strings"
	"testing"
)

// parseNeigh builds a neighbour list from the text `ip -6 neigh show` used to
// print. The daemon reads rtnetlink now and never parses this, but the fixtures
// stay in this shape because they are the format every event log and every
// captured session already uses, and they are far easier to read in a test than
// a slice of structs.
func parseNeigh(out string) []neighbour {
	var ns []neighbour
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		a, err := netip.ParseAddr(f[0])
		if err != nil {
			continue
		}
		n := neighbour{addr: a, state: f[len(f)-1]}
		for i := 1; i+1 < len(f); i++ {
			if f[i] == "lladdr" {
				n.mac = strings.ToLower(f[i+1])
			}
		}
		ns = append(ns, n)
	}
	return ns
}

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", s, err)
	}
	return n
}

// Which routes count as "this link's own networks" decides what goes into the
// car's block list, so it is tested directly. This is the judgement that used
// to be made by grepping iproute2's output for "via".
func TestOnLinkRoute(t *testing.T) {
	for _, tc := range []struct {
		name string
		dst  string
		gw   string
		want string // "" means not on-link
	}{
		{"on-link /64", "2001:db8:7f00:1a2b::/64", "", "2001:db8:7f00:1a2b::/64"},
		{"on-link ULA", "fd00:0:0:5::/64", "", "fd00:0:0:5::/64"},
		{"via a gateway is the internet", "2001:db8:ffff::/48", "fe80::1", ""},
		{"default route", "", "fe80::1", ""},
		{"default route with no gateway", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dst *net.IPNet
			if tc.dst != "" {
				dst = mustCIDR(t, tc.dst)
			}
			var gw net.IP
			if tc.gw != "" {
				gw = net.ParseIP(tc.gw)
			}
			got, ok := onLinkRoute(dst, gw)
			if tc.want == "" {
				if ok {
					t.Fatalf("onLinkRoute = %v, want not on-link", got)
				}
				return
			}
			if !ok || got.String() != tc.want {
				t.Fatalf("onLinkRoute = %v %v, want %s", got, ok, tc.want)
			}
		})
	}
}

// The kernel reports a NUD bitmask; the rules and the log are written against
// the words iproute2 printed, and FAILED/INCOMPLETE in particular decide who
// gets a withdrawal RA.
func TestNeighStateNames(t *testing.T) {
	for _, tc := range []struct {
		state int
		want  string
	}{
		{0x02, "REACHABLE"},
		{0x04, "STALE"},
		{0x08, "DELAY"},
		{0x10, "PROBE"},
		{0x20, "FAILED"},
		{0x01, "INCOMPLETE"},
		{0x80, "PERMANENT"},
		{0x00, "NONE"},
	} {
		if got := neighState(tc.state); got != tc.want {
			t.Errorf("neighState(%#x) = %s, want %s", tc.state, got, tc.want)
		}
	}
}

// A /64 address must survive the round trip to the kernel's representation and
// back, because this is how the served prefix's ::1 is added and removed.
func TestPrefixIPNetRoundTrip(t *testing.T) {
	for _, s := range []string{"fd5c:ba1e:f32:a::1/64", "2001:db8::1/128", "fd00::/8"} {
		p := netip.MustParsePrefix(s)
		back, ok := ipNetToPrefix(prefixToIPNet(p))
		if !ok || back.String() != p.String() {
			t.Errorf("round trip %s = %v %v", s, back, ok)
		}
	}
}

func TestFormatNeigh(t *testing.T) {
	ns := parseNeigh("fd5c:ba1e:f32:a::22 lladdr 6a:1f:00:00:00:02 DELAY\nfd5c:ba1e:f32:a::33 FAILED\n")
	want := "fd5c:ba1e:f32:a::22 lladdr 6a:1f:00:00:00:02 DELAY | fd5c:ba1e:f32:a::33 FAILED"
	if got := formatNeigh(ns); got != want {
		t.Errorf("formatNeigh =\n%s\nwant\n%s", got, want)
	}
}
