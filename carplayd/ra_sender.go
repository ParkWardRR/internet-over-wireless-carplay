package main

import (
	"fmt"
	"math/rand"
	"net"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/net/ipv6"
)

// raSender advertises one car's prefix on one interface. Every RA goes out
// through it, so RFC 4861's timing holds across all of them:
//   - periodic multicast RAs at a random [MinRtrAdvInterval, MaxRtrAdvInterval];
//   - consecutive multicast RAs at least MIN_DELAY_BETWEEN_RAS (3 s) apart;
//   - a Router Solicitation answered after a random 0–0.5 s, unicast to the
//     solicitor when it has an address (RFC 7772), else by a multicast RA;
//   - a new node's first MLD report treated like a Router Solicitation: a
//     phone's first RS is often lost while its Wi-Fi handshake finishes, but
//     it reports its multicast groups again a moment later.
//
// withdraw/resume flip between the normal RA and a withdrawn one (router,
// RDNSS and preferred lifetimes 0) without leaving the link.
type raSender struct {
	ifi      *net.Interface
	pc       *ipv6.PacketConn
	own      map[netip.Addr]bool // our addresses: ignore our own reports
	min, max time.Duration
	onEvent  func(name string, args ...string)
	out      func(dst net.Addr, p raParams) error // tests: replaces the socket

	mu        sync.Mutex
	params    raParams
	withdrawn bool
	closed    bool
	rng       *rand.Rand
	lastMcast time.Time
	next      time.Time // next scheduled multicast
	timer     *time.Timer
	known     map[netip.Addr]time.Time // link-local solicitors, for unicast withdrawals
	answered  map[netip.Addr]time.Time // last implicit-RS answer per MLD reporter
}

var (
	allNodes   = &net.IPAddr{IP: net.ParseIP("ff02::1")}
	allRouters = &net.IPAddr{IP: net.ParseIP("ff02::2")}
	allMLDv2   = &net.IPAddr{IP: net.ParseIP("ff02::16")}
)

const (
	icmpMLDv1Report = 131
	icmpMLDv2Report = 143
	knownFor        = 10 * time.Minute // forget a solicitor after this long
	mldAnswerEvery  = 5 * time.Second  // answer one reporter's MLD at most this often (a phone re-joining must be answered)
)

func newRASender(ifname string, p raParams, max time.Duration, onEvent func(string, ...string)) (*raSender, error) {
	ifi, err := net.InterfaceByName(ifname)
	if err != nil {
		return nil, err
	}
	p.SourceMAC = ifi.HardwareAddr
	c, err := net.ListenPacket("ip6:ipv6-icmp", "::")
	if err != nil {
		return nil, fmt.Errorf("raw ICMPv6 socket: %w", err)
	}
	pc := ipv6.NewPacketConn(c)
	fail := func(err error) (*raSender, error) { c.Close(); return nil, err }
	var f ipv6.ICMPFilter
	f.SetAll(true)
	f.Accept(ipv6.ICMPTypeRouterSolicitation)
	f.Accept(ipv6.ICMPTypeMulticastListenerReport)
	f.Accept(ipv6.ICMPTypeVersion2MulticastListenerReport)
	if err := pc.SetICMPFilter(&f); err != nil {
		return fail(err)
	}
	if err := pc.SetControlMessage(ipv6.FlagInterface|ipv6.FlagSrc|ipv6.FlagDst, true); err != nil {
		return fail(err)
	}
	pc.SetMulticastHopLimit(255)
	pc.SetHopLimit(255)
	pc.SetMulticastInterface(ifi)
	pc.SetMulticastLoopback(false)
	if err := pc.JoinGroup(ifi, allRouters); err != nil { // Router Solicitations
		return fail(fmt.Errorf("join ff02::2 on %s: %w", ifname, err))
	}
	if err := pc.JoinGroup(ifi, allMLDv2); err != nil { // MLDv2 reports
		return fail(fmt.Errorf("join ff02::16 on %s: %w", ifname, err))
	}
	own := map[netip.Addr]bool{}
	if as, err := ifi.Addrs(); err == nil {
		for _, a := range as {
			if n, ok := a.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(n.IP); ok {
					own[ip.Unmap()] = true
				}
			}
		}
	}
	if onEvent == nil {
		onEvent = func(string, ...string) {}
	}
	return &raSender{
		ifi: ifi, pc: pc, own: own, min: minRtrAdv(max), max: max, onEvent: onEvent,
		params: p, rng: rand.New(rand.NewSource(time.Now().UnixNano())),
		known: map[netip.Addr]time.Time{}, answered: map[netip.Addr]time.Time{},
	}, nil
}

// current is the RA to send now: normal, or withdrawn.
func (s *raSender) current() raParams {
	if s.withdrawn {
		return s.params.withdrawn()
	}
	return s.params
}

// start sends the first multicast RA, starts the periodic timer and the
// listener. withdrawn: start out advertising no route (upstream is down).
func (s *raSender) start(withdrawn bool) {
	s.mu.Lock()
	s.withdrawn = withdrawn
	s.timer = time.AfterFunc(time.Hour, s.fire)
	s.mcastLocked()
	s.mu.Unlock()
	if s.pc != nil {
		go s.listen()
	}
}

func (s *raSender) write(dst net.Addr, p raParams) error {
	if s.out != nil {
		return s.out(dst, p)
	}
	cm := &ipv6.ControlMessage{IfIndex: s.ifi.Index, HopLimit: 255}
	_, err := s.pc.WriteTo(buildRA(p), cm, dst)
	return err
}

// mcastLocked multicasts the current RA and schedules the next periodic one.
func (s *raSender) mcastLocked() {
	if err := s.write(allNodes, s.current()); err != nil {
		s.onEvent("error", "ra_send", err.Error())
	}
	now := time.Now()
	s.lastMcast = now
	s.next = now.Add(nextUnsolicited(s.rng, s.min, s.max))
	s.timer.Reset(time.Until(s.next))
}

// fire is the timer: send the scheduled multicast, unless the rate limit
// says not yet (only if the clock moved; mcastAt already respects it).
func (s *raSender) fire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if at := mcastAt(time.Now(), s.lastMcast, 0); time.Until(at) > 0 {
		s.next = at
		s.timer.Reset(time.Until(at))
		return
	}
	s.mcastLocked()
}

// requestMcastLocked moves the next multicast earlier if allowed: `delay`
// from now, but never within MIN_DELAY_BETWEEN_RAS of the last one.
func (s *raSender) requestMcastLocked(delay time.Duration) {
	at := mcastAt(time.Now(), s.lastMcast, delay)
	if at.Before(s.next) {
		s.next = at
		s.timer.Reset(time.Until(at))
	}
}

func (s *raSender) unicastLocked(dst netip.Addr) {
	ip := net.IP(dst.AsSlice())
	zone := ""
	if dst.IsLinkLocalUnicast() {
		zone = s.ifi.Name
	}
	if err := s.write(&net.IPAddr{IP: ip, Zone: zone}, s.current()); err != nil {
		s.onEvent("error", "ra_unicast", dst.String(), err.Error())
	}
}

// solicited answers a solicitor (or an implicit one) after the random delay.
func (s *raSender) solicitedLocked(src netip.Addr) {
	d := solicitedDelay(s.rng)
	if !src.IsValid() || src.IsUnspecified() {
		s.requestMcastLocked(d)
		return
	}
	if src.IsLinkLocalUnicast() {
		s.known[src] = time.Now()
	}
	time.AfterFunc(d, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.closed {
			s.unicastLocked(src)
		}
	})
}

func (s *raSender) listen() {
	buf := make([]byte, 1500)
	for {
		n, cm, from, err := s.pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if n < 1 || cm == nil || cm.IfIndex != s.ifi.Index {
			continue
		}
		var src netip.Addr
		if a, ok := from.(*net.IPAddr); ok {
			src, _ = netip.AddrFromSlice(a.IP)
			src = src.Unmap()
		}
		if s.own[src] {
			continue
		}
		s.mu.Lock()
		switch buf[0] {
		case icmpRS:
			s.onEvent("rs", src.String())
			s.solicitedLocked(src)
		case icmpMLDv1Report, icmpMLDv2Report:
			now := time.Now()
			if src.IsUnspecified() || now.Sub(s.answered[src]) >= mldAnswerEvery {
				s.answered[src] = now
				s.onEvent("mld", src.String())
				s.solicitedLocked(src)
			}
		}
		s.mu.Unlock()
	}
}

// targetsLocked: everyone we know is on the link, for unicast withdrawals.
func (s *raSender) targetsLocked(extra []netip.Addr) []netip.Addr {
	seen := map[netip.Addr]bool{}
	var out []netip.Addr
	now := time.Now()
	for a, t := range s.known {
		if now.Sub(t) > knownFor {
			delete(s.known, a)
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	for _, a := range extra {
		if !seen[a] && !s.own[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

// setWithdrawn switches between advertising and withdrawing the route while
// staying on the link (upstream health). Known hosts get a unicast RA at
// once; everyone else gets the next multicast, as soon as the rate limit allows.
func (s *raSender) setWithdrawn(w bool, extra []netip.Addr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.withdrawn == w {
		return
	}
	s.withdrawn = w
	for _, a := range s.targetsLocked(extra) {
		s.unicastLocked(a)
	}
	s.requestMcastLocked(0)
}

// goodbye withdraws everything before we leave the link (RFC 4861 6.2.5):
// unicast to each known host twice, then one final multicast RA as soon as
// MIN_DELAY_BETWEEN_RAS allows (waits at most 3 s). The sender is closed after.
func (s *raSender) goodbye(extra []netip.Addr) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.withdrawn = true
	s.timer.Stop()
	targets := s.targetsLocked(extra)
	for _, a := range targets {
		s.unicastLocked(a)
	}
	wait := time.Until(mcastAt(time.Now(), s.lastMcast, 0))
	s.mu.Unlock()

	time.Sleep(100 * time.Millisecond)
	s.mu.Lock()
	for _, a := range targets { // again: one can be lost on a fading link
		s.unicastLocked(a)
	}
	s.mu.Unlock()
	if wait > 100*time.Millisecond {
		time.Sleep(wait - 100*time.Millisecond)
	}
	s.mu.Lock()
	if err := s.write(allNodes, s.current()); err != nil {
		s.onEvent("error", "ra_send", err.Error())
	}
	s.lastMcast = time.Now()
	s.mu.Unlock()
	s.close()
}

func (s *raSender) close() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		if s.timer != nil {
			s.timer.Stop()
		}
		if s.pc != nil {
			s.pc.Close()
		}
	}
	s.mu.Unlock()
}
