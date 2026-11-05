package main

import (
	"encoding/binary"
	"math/rand"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// raOpts splits an RA body into its options by type.
func raOpts(t *testing.T, b []byte) map[byte][]byte {
	t.Helper()
	opts := map[byte][]byte{}
	for o := b[16:]; len(o) > 0; {
		if len(o) < 2 || o[1] == 0 || int(o[1])*8 > len(o) {
			t.Fatalf("malformed option at %x", o)
		}
		opts[o[0]] = o[:int(o[1])*8]
		o = o[int(o[1])*8:]
	}
	return opts
}

func testParams() raParams {
	return raParams{
		RouterLife: 20 * time.Second, Prefix: netip.MustParsePrefix("fd5c:ba1e:f32:a::/64"),
		ValidLife: 24 * time.Hour, PreferredLife: 30 * time.Minute,
		DNS: []netip.Addr{netip.MustParseAddr("2001:db8:ffff::53")}, DNSLife: 12 * time.Second,
	}
}

// A withdrawal must remove the route, the DNS servers and new use of the
// address, not only the router lifetime (the review's P0).
func TestWithdrawnRA(t *testing.T) {
	b := buildRA(testParams().withdrawn())
	if lt := binary.BigEndian.Uint16(b[6:]); lt != 0 {
		t.Errorf("router lifetime %d, want 0", lt)
	}
	o := raOpts(t, b)
	if l := binary.BigEndian.Uint32(o[optRDNSS][4:]); l != 0 {
		t.Errorf("RDNSS lifetime %d, want 0", l)
	}
	pi := o[optPrefixInfo]
	if v, pr := binary.BigEndian.Uint32(pi[4:]), binary.BigEndian.Uint32(pi[8:]); v != 86400 || pr != 0 {
		t.Errorf("prefix valid/preferred %d/%d, want 86400/0", v, pr)
	}
	// The normal RA is untouched.
	if l := binary.BigEndian.Uint32(raOpts(t, buildRA(testParams()))[optRDNSS][4:]); l != 12 {
		t.Errorf("normal RDNSS lifetime %d", l)
	}
}

func TestRATimingConstants(t *testing.T) {
	if m := minRtrAdv(4 * time.Second); m != 3*time.Second {
		t.Errorf("min for 4 s = %v", m)
	}
	if m := minRtrAdv(30 * time.Second); m != 10*time.Second {
		t.Errorf("min for 30 s = %v", m)
	}
	r := rand.New(rand.NewSource(1))
	lo, hi := time.Hour, time.Duration(0)
	for i := 0; i < 2000; i++ {
		d := nextUnsolicited(r, 3*time.Second, 4*time.Second)
		if d < 3*time.Second || d > 4*time.Second {
			t.Fatalf("unsolicited interval %v outside [3s, 4s]", d)
		}
		lo, hi = min(lo, d), max(hi, d)
		s := solicitedDelay(r)
		if s < 0 || s > 500*time.Millisecond {
			t.Fatalf("solicited delay %v outside [0, 0.5s]", s)
		}
	}
	if lo > 3100*time.Millisecond || hi < 3900*time.Millisecond {
		t.Errorf("intervals not spread: %v..%v", lo, hi)
	}
}

func TestMcastAt(t *testing.T) {
	t0 := time.Unix(1000, 0)
	if at := mcastAt(t0, time.Time{}, 0); !at.Equal(t0) {
		t.Errorf("first multicast at %v", at)
	}
	if at := mcastAt(t0.Add(time.Second), t0, 200*time.Millisecond); !at.Equal(t0.Add(3200 * time.Millisecond)) {
		t.Errorf("rate-limited multicast at %v, want last+3s+delay", at.Sub(t0))
	}
	if at := mcastAt(t0.Add(5*time.Second), t0, 200*time.Millisecond); !at.Equal(t0.Add(5200 * time.Millisecond)) {
		t.Errorf("free multicast at %v", at.Sub(t0))
	}
}

func TestHealthHysteresis(t *testing.T) {
	h := health{ok: true}
	if h.update(false, 2) || !h.ok {
		t.Fatal("one failure must not withdraw")
	}
	if !h.update(false, 2) || h.ok {
		t.Fatal("two failures must withdraw")
	}
	if h.update(false, 2) {
		t.Fatal("still down: no change")
	}
	if !h.update(true, 2) || !h.ok {
		t.Fatal("one success must resume")
	}
}

func TestDNSLifeBoundedByRouter(t *testing.T) {
	c := defaultConfig()
	if l := dnsLife(c); l != 12*time.Second {
		t.Errorf("default RDNSS lifetime %v, want 3 × 4 s", l)
	}
	c.RouterLife = 8 * time.Second
	if l := dnsLife(c); l != 8*time.Second {
		t.Errorf("RDNSS lifetime %v must not exceed router lifetime", l)
	}
}

func TestConfigRFCLimits(t *testing.T) {
	car := "[car A]\nssid = x\npsk = 12345678\n"
	for name, s := range map[string]string{
		"ra_interval below 4 s":      "ra_interval = 1s\n" + car,
		"ra_eager removed":           "ra_eager = 1s\n" + car,
		"router lifetime < interval": "router_lifetime = 2s\n" + car,
		"nft nat without uplink":     "nat66 = nft\n" + car,
		"bad block":                  "block = 10.0.0.0/8\n" + car,
		"bad health target":          "health_target = dns.google:53\n" + car,
	} {
		if _, err := parseConfig(strings.NewReader(s)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	c := mustConfig(t, "dns = 2001:db8::53, 2001:db8::54\nblock = 2001:db8:100::/48\n"+car)
	if strings.Join(c.HealthTargets, " ") != "[2001:db8::53]:53 [2001:db8::54]:53" {
		t.Errorf("default health targets %v", c.HealthTargets)
	}
	if c.RAInterval != 4*time.Second || c.Firewall != "nft" || !c.Health || len(c.Block) != 1 {
		t.Errorf("defaults %+v", c)
	}
}

// One iPhone holds several addresses in our prefix (stable + temporary); it
// is still one phone.
func TestPhonesInCountsDevices(t *testing.T) {
	neigh := "fd5c:ba1e:f32:a:1c2b:3d4e:5f60:7182 lladdr 6a:1f:00:00:00:01 REACHABLE\n" +
		"fd5c:ba1e:f32:a:9999:8888:7777:6666 lladdr 6a:1f:00:00:00:01 STALE\n" +
		"fe80::681f:ff:fe00:1 lladdr 6a:1f:00:00:00:01 router REACHABLE\n" +
		"fd5c:ba1e:f32:a::22 lladdr 6a:1f:00:00:00:02 DELAY\n" +
		"fd5c:ba1e:f32:a::33 FAILED\n"
	if n := phonesIn(neigh, netip.MustParsePrefix("fd5c:ba1e:f32:a::/64")); n != 2 {
		t.Errorf("phones = %d, want 2 devices", n)
	}
	got := withdrawTargets(neigh, netip.MustParsePrefix("fd5c:ba1e:f32:a::/64"))
	want := "fe80::681f:ff:fe00:1 fd5c:ba1e:f32:a::22"
	var s []string
	for _, a := range got {
		s = append(s, a.String())
	}
	if strings.Join(s, " ") != want {
		t.Errorf("withdraw targets %v, want %s (link-local preferred, one per device)", s, want)
	}
}

func TestHomePrefixes(t *testing.T) {
	routes := "2001:db8:7f00:1a2b::/64 proto ra metric 100 pref medium\n" +
		"2001:db8:7f00:1a2c::/64 proto kernel metric 256 pref medium\n" +
		"fd00:0:0:5::/64 proto kernel metric 256 pref medium\n" +
		"fe80::/64 proto kernel metric 256 pref medium\n" +
		"2001:db8:ffff::/48 via fe80::1 metric 1024 pref medium\n" +
		"default via fe80::1 proto ra metric 100 pref medium\n"
	got := homePrefixes(routes, []netip.Prefix{netip.MustParsePrefix("2001:db8:100::/48")})
	var s []string
	for _, p := range got {
		s = append(s, p.String())
	}
	// Both home /64s collapse into their /56; the ULA, link-local, the routed
	// prefix and the default route are not "home".
	if strings.Join(s, " ") != "2001:db8:100::/48 2001:db8:7f00:1a00::/56" {
		t.Errorf("home prefixes %v", s)
	}
}

func TestOutermost(t *testing.T) {
	in := []netip.Prefix{
		netip.MustParsePrefix("fc00::/7"), netip.MustParsePrefix("fd00:1::/48"),
		netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2001:db8:5::/48"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	var s []string
	for _, p := range outermost(in) {
		s = append(s, p.String())
	}
	if strings.Join(s, " ") != "2001:db8::/32 fc00::/7" {
		t.Errorf("outermost %v", s)
	}
}

func TestNFTRuleset(t *testing.T) {
	c := mustConfig(t, "sta = wlan0\nuplink = eth0\nnat66 = nft\n[car A]\nssid = x\npsk = 12345678\n[car B]\nssid = y\npsk = 12345678\n")
	r := nftRuleset(c, []netip.Prefix{netip.MustParsePrefix("2001:db8:7f00:1a00::/56")})
	for _, want := range []string{
		"table inet carplayd {}\ndelete table inet carplayd\n", // atomic replace
		`iifname "wlan0" meta l4proto ipv6-icmp accept`,        // ND and RS only...
		`iifname "wlan0" drop`,                                 // ...nothing else to the box
		`iifname "wlan0" meta nfproto ipv4 drop`,
		`iifname "wlan0" ip6 daddr { 2001:db8:7f00:1a00::/56, fc00::/7, fe80::/10, ff00::/8 } reject`,
		`iifname "wlan0" oifname != "eth0" reject`,
		`oifname "eth0" ip6 saddr { fd5c:ba1e:f32:a::/64, fd5c:ba1e:f32:b::/64 } counter masquerade`,
	} {
		if !strings.Contains(r, want) {
			t.Errorf("ruleset lacks %q:\n%s", want, r)
		}
	}
	c.Firewall, c.NAT66 = "none", "nft"
	if r := nftRuleset(c, nil); strings.Contains(r, "chain input") || !strings.Contains(r, "masquerade") {
		t.Errorf("firewall=none must leave only NAT:\n%s", r)
	}
}

func TestCheckUpstream(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	deadAddr := dead.Addr().String()
	dead.Close()
	if r := checkUpstream([]string{deadAddr, ln.Addr().String()}, time.Second); !r.ok {
		t.Errorf("one live target must be enough: %+v", r)
	}
	if r := checkUpstream([]string{deadAddr}, time.Second); r.ok {
		t.Errorf("dead target reported healthy")
	}
}

type sentRA struct {
	at    time.Time
	dst   string
	route uint16
	dns   uint32
}

// The scheduler end to end, with the socket replaced: every multicast RA at
// least 3 s after the previous one whatever triggers it, solicited unicast
// answers within 0.5 s, withdrawals reaching known hosts at once.
func TestRASchedulerRFC4861(t *testing.T) {
	if testing.Short() {
		t.Skip("takes ~8 s of real time")
	}
	var mu sync.Mutex
	var sent []sentRA
	s := &raSender{
		ifi: &net.Interface{Index: 1, Name: "test0"}, min: 3 * time.Second, max: 4 * time.Second,
		onEvent: func(string, ...string) {}, params: testParams(), rng: rand.New(rand.NewSource(2)),
		own: map[netip.Addr]bool{}, known: map[netip.Addr]time.Time{}, answered: map[netip.Addr]time.Time{},
	}
	s.out = func(dst net.Addr, p raParams) error {
		b := buildRA(p)
		mu.Lock()
		sent = append(sent, sentRA{time.Now(), dst.(*net.IPAddr).IP.String(), binary.BigEndian.Uint16(b[6:]), binary.BigEndian.Uint32(raOpts(t, b)[optRDNSS][4:])})
		mu.Unlock()
		return nil
	}
	phone := netip.MustParseAddr("fe80::681f:ff:fe00:1")
	t0 := time.Now()
	s.start(false)
	time.Sleep(100 * time.Millisecond)
	s.mu.Lock()
	s.solicitedLocked(netip.IPv6Unspecified()) // RS from :: → multicast, but not before t0+3 s
	s.solicitedLocked(phone)                   // RS from the phone → unicast within 0.5 s
	s.mu.Unlock()
	time.Sleep(900 * time.Millisecond)
	s.setWithdrawn(true, nil) // upstream lost at t0+1 s
	time.Sleep(3 * time.Second)
	s.setWithdrawn(false, nil) // back at t0+4 s
	time.Sleep(200 * time.Millisecond)
	s.goodbye(nil) // leaving at ~t0+4.2 s: must wait for the 3 s spacing
	elapsed := time.Since(t0)

	mu.Lock()
	defer mu.Unlock()
	var mcast []sentRA
	var uni []sentRA
	for _, r := range sent {
		if r.dst == "ff02::1" {
			mcast = append(mcast, r)
		} else {
			uni = append(uni, r)
		}
	}
	for i := 1; i < len(mcast); i++ {
		if gap := mcast[i].at.Sub(mcast[i-1].at); gap < 3*time.Second-20*time.Millisecond {
			t.Errorf("multicast RAs %d→%d only %v apart", i-1, i, gap)
		}
	}
	if len(mcast) < 3 {
		t.Fatalf("want ≥3 multicasts (start, rate-limited, final), got %d: %+v", len(mcast), sent)
	}
	if last := mcast[len(mcast)-1]; last.route != 0 || last.dns != 0 {
		t.Errorf("final multicast route/dns lifetimes %d/%d, want 0/0", last.route, last.dns)
	}
	if len(uni) == 0 || uni[0].dst != phone.String() || uni[0].at.Sub(t0) > 650*time.Millisecond || uni[0].route != 20 {
		t.Errorf("first unicast %+v: want the phone, ≤0.5 s after its RS, normal route", uni)
	}
	var withdrawn, resumed bool
	for _, u := range uni {
		withdrawn = withdrawn || (u.route == 0 && u.dns == 0 && u.at.Sub(t0) < 1100*time.Millisecond)
		resumed = resumed || (u.route == 20 && u.at.Sub(t0) > 4*time.Second && u.at.Sub(t0) < 4100*time.Millisecond)
	}
	if !withdrawn || !resumed {
		t.Errorf("withdraw/resume unicasts to the known phone missing: %+v", uni)
	}
	for _, r := range sent {
		t.Logf("%6.3fs %-22s route=%d dns=%d", r.at.Sub(t0).Seconds(), r.dst, r.route, r.dns)
	}
	if elapsed > 7500*time.Millisecond {
		t.Errorf("goodbye took until %v", elapsed)
	}
}

func TestSurveyDue(t *testing.T) {
	t0 := time.Unix(1000, 0)
	if surveyDue(t0, t0.Add(time.Hour), 0) {
		t.Error("survey off must never log")
	}
	if surveyDue(t0, t0.Add(9*time.Second), 10*time.Second) || !surveyDue(t0, t0.Add(10*time.Second), 10*time.Second) {
		t.Error("survey interval not respected")
	}
	if c := mustConfig(t, "survey = 10s\n[car A]\nssid = x\npsk = 12345678\n"); c.Survey != 10*time.Second {
		t.Errorf("survey %v", c.Survey)
	}
}

// The F32's real departure (2026-11-04): parked around -75, then falling. With
// drop = 6 the trend leave fires while the link is still usable, where the
// absolute -85 threshold never did (the link died at -84).
func TestTrendLeaveOnDeparture(t *testing.T) {
	t0 := time.Unix(1000, 0)
	sigs := []int{-75, -75, -74, -73, -73, -74, -75, -74, -73, -75, -74, -73, -74, -75, -74, -73, -75, -74, -76, -78, -80, -81, -82, -83, -84}
	var s []sample
	var below time.Time
	left := -1
	for i, v := range sigs {
		now := t0.Add(time.Duration(i) * time.Second)
		s = append(trimSamples(s, now), sample{now, v})
		base, ok := baselineOf(s, now)
		if !ok {
			continue
		}
		var leave bool
		if below, leave = leaveCheck(below, now, v, base-6, 2*time.Second); leave {
			left = i
			break
		}
	}
	if left < 0 || sigs[left] < -84 {
		t.Fatalf("trend leave at index %d", left)
	}
	if sigs[left] > -80 {
		t.Errorf("left too early at %d dBm", sigs[left])
	}
}

// A brief dip while parked (2 polls, under leave_for = 2 s) must not leave.
func TestTrendIgnoresBriefDip(t *testing.T) {
	t0 := time.Unix(1000, 0)
	var s []sample
	var below time.Time
	for i := 0; i < 40; i++ {
		v := -75
		if i == 25 || i == 26 {
			v = -84
		}
		now := t0.Add(time.Duration(i) * time.Second)
		s = append(trimSamples(s, now), sample{now, v})
		if base, ok := baselineOf(s, now); ok {
			var leave bool
			if below, leave = leaveCheck(below, now, v, base-6, 2*time.Second); leave {
				t.Fatalf("left on a 2 s dip at poll %d", i)
			}
		}
	}
}

func TestRelaxRejoinAndStation(t *testing.T) {
	cars := []Car{{ID: "A", Rejoin: -77}, {ID: "B", Rejoin: -77}}
	r := relaxRejoin(cars, map[string]int{"A": -53})
	if r[0].Rejoin != -53 || r[1].Rejoin != -77 || cars[0].Rejoin != -77 {
		t.Errorf("relaxRejoin %+v (orig %+v)", r, cars)
	}
	st := iwStation("Station bc:30:7e:00:00:01 (on wlan0)\n\tinactive time:\t20 ms\n\trx bytes:\t123\n\ttx retries:\t7\n\ttx failed:\t1\n\ttx bitrate:\t1.0 MBit/s\n")
	if st["tx_retries"] != "7" || st["tx_failed"] != "1" || st["tx_bitrate"] != "1.0" || st["rx_bytes"] != "123" {
		t.Errorf("station %v", st)
	}
}
