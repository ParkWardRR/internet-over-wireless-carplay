package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"time"

	"golang.org/x/net/ipv6"
)

// RA option and message constants (RFC 4861, RFC 8106).
const (
	icmpRS        = 133
	icmpRA        = 134
	optSourceLL   = 1
	optPrefixInfo = 3
	optRDNSS      = 25
)

// raParams is what goes into one Router Advertisement.
type raParams struct {
	RouterLife    time.Duration // 0 = "stop using me as a router" (goodbye)
	Prefix        netip.Prefix
	ValidLife     time.Duration
	PreferredLife time.Duration
	DNS           []netip.Addr
	DNSLife       time.Duration
	SourceMAC     net.HardwareAddr
}

func secs(d time.Duration, max uint32) uint32 {
	s := d / time.Second
	if s < 0 {
		return 0
	}
	if uint64(s) > uint64(max) {
		return max
	}
	return uint32(s)
}

// buildRA returns the ICMPv6 Router Advertisement body. The kernel fills in
// the checksum on raw ICMPv6 sockets.
func buildRA(p raParams) []byte {
	b := make([]byte, 16, 128)
	b[0] = icmpRA
	b[4] = 64 // cur hop limit
	b[5] = 0  // M=0 O=0: SLAAC only
	binary.BigEndian.PutUint16(b[6:], uint16(secs(p.RouterLife, 9000)))
	// reachable time and retrans timer stay 0 (unspecified)

	if len(p.SourceMAC) == 6 {
		o := make([]byte, 8)
		o[0], o[1] = optSourceLL, 1
		copy(o[2:], p.SourceMAC)
		b = append(b, o...)
	}
	if p.Prefix.IsValid() {
		o := make([]byte, 32)
		o[0], o[1] = optPrefixInfo, 4
		o[2] = byte(p.Prefix.Bits())
		o[3] = 0xc0 // on-link + autonomous
		binary.BigEndian.PutUint32(o[4:], secs(p.ValidLife, 0xffffffff))
		binary.BigEndian.PutUint32(o[8:], secs(p.PreferredLife, 0xffffffff))
		a := p.Prefix.Masked().Addr().As16()
		copy(o[16:], a[:])
		b = append(b, o...)
	}
	if len(p.DNS) > 0 {
		o := make([]byte, 8+16*len(p.DNS))
		o[0], o[1] = optRDNSS, byte(1+2*len(p.DNS))
		binary.BigEndian.PutUint32(o[4:], secs(p.DNSLife, 0xffffffff))
		for i, d := range p.DNS {
			a := d.As16()
			copy(o[8+16*i:], a[:])
		}
		b = append(b, o...)
	}
	return b
}

// raSender advertises one car's prefix on one interface and answers Router
// Solicitations from that interface at once.
type raSender struct {
	ifi    *net.Interface
	pc     *ipv6.PacketConn
	params raParams
}

var allNodes = &net.IPAddr{IP: net.ParseIP("ff02::1")}

func newRASender(ifname string, p raParams) (*raSender, error) {
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
	var f ipv6.ICMPFilter
	f.SetAll(true)
	f.Accept(ipv6.ICMPTypeRouterSolicitation)
	if err := pc.SetICMPFilter(&f); err != nil {
		c.Close()
		return nil, err
	}
	if err := pc.SetControlMessage(ipv6.FlagInterface|ipv6.FlagSrc, true); err != nil {
		c.Close()
		return nil, err
	}
	pc.SetMulticastHopLimit(255)
	pc.SetHopLimit(255)
	pc.SetMulticastInterface(ifi)
	pc.SetMulticastLoopback(false)
	// Router Solicitations go to all-routers.
	if err := pc.JoinGroup(ifi, &net.IPAddr{IP: net.ParseIP("ff02::2")}); err != nil {
		c.Close()
		return nil, fmt.Errorf("join ff02::2 on %s: %w", ifname, err)
	}
	return &raSender{ifi: ifi, pc: pc, params: p}, nil
}

func (s *raSender) send(dst net.Addr, routerLife time.Duration) error {
	p := s.params
	p.RouterLife = routerLife
	cm := &ipv6.ControlMessage{IfIndex: s.ifi.Index, HopLimit: 255}
	_, err := s.pc.WriteTo(buildRA(p), cm, dst)
	return err
}

// advertise multicasts an RA with the normal router lifetime.
func (s *raSender) advertise() error { return s.send(allNodes, s.params.RouterLife) }

// goodbye multicasts an RA with router lifetime 0: phones drop the default
// route at once and fall back to cellular.
func (s *raSender) goodbye() error { return s.send(allNodes, 0) }

// serveRS answers each Router Solicitation on our interface with a unicast RA
// (or multicast if the solicitor has no address yet), until the socket closes.
func (s *raSender) serveRS(onRS func(src net.IP)) {
	buf := make([]byte, 1500)
	for {
		n, cm, src, err := s.pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if n < 1 || buf[0] != icmpRS || cm == nil || cm.IfIndex != s.ifi.Index {
			continue
		}
		var dst net.Addr = allNodes
		if a, ok := src.(*net.IPAddr); ok && !a.IP.IsUnspecified() {
			dst = &net.IPAddr{IP: a.IP, Zone: s.ifi.Name}
		}
		s.send(dst, s.params.RouterLife)
		if onRS != nil {
			ip := net.IP(nil)
			if a, ok := src.(*net.IPAddr); ok {
				ip = a.IP
			}
			onRS(ip)
		}
	}
}

func (s *raSender) close() { s.pc.Close() }
